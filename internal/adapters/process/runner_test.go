package process_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Diaszano/quota-guardian/internal/adapters/process"
	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

var fakeCLIBin string

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "fakecli-test-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	binPath := filepath.Join(tmpDir, "fakecli")
	cmd := exec.Command("go", "build", "-o", binPath, "../../testutil/fakecli/main.go")
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("failed to build fakecli: " + string(out))
	}
	fakeCLIBin = binPath

	os.Exit(m.Run())
}

// TestDrainBothStreams floods stderr while returning a stdout result and asserts no deadlock.
func TestDrainBothStreams(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runner := process.NewRunner()

	var (
		mu         sync.Mutex
		stdoutBuf  bytes.Buffer
		stderrSize int
	)

	spec := ports.CommandSpec{
		Path: fakeCLIBin,
		Args: []string{"--mode", "flood-output", "--flood-bytes", "3145728"}, // 3 MiB to stderr
	}

	result, err := runner.Run(ctx, spec, func(id domain.ProcessIdentity) error {
		if id.PID <= 0 {
			t.Errorf("expected positive PID, got %d", id.PID)
		}
		return nil
	}, func(rec ports.StreamRecord) error {
		mu.Lock()
		defer mu.Unlock()
		if rec.Kind == ports.StreamStdout {
			stdoutBuf.Write(rec.Data)
		} else if rec.Kind == ports.StreamStderr {
			stderrSize += len(rec.Data)
		}
		return nil
	})

	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if !result.Reaped {
		t.Errorf("expected process to be reaped")
	}
	if !result.Settled {
		t.Errorf("expected clean settlement")
	}
	if result.SettlementConfidence != ports.SettlementConfirmed {
		t.Errorf("expected confirmed settlement confidence, got %s", result.SettlementConfidence)
	}

	if !strings.Contains(stdoutBuf.String(), `"status":"completed"`) {
		t.Errorf("stdout missing expected result, got: %s", stdoutBuf.String())
	}
	if stderrSize < 3145728 {
		t.Errorf("expected at least 3 MiB on stderr, got %d bytes", stderrSize)
	}
}

// TestCancellationReapsGroup spawns child + grandchild, cancels context, and asserts entire group is killed.
func TestCancellationReapsGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	runner := process.NewRunner()

	var startedPID int
	spec := ports.CommandSpec{
		Path: fakeCLIBin,
		Args: []string{"--mode", "spawn-grandchild"},
	}

	runDone := make(chan struct{})
	var runErr error
	var runResult ports.ProcessResult

	go func() {
		defer close(runDone)
		runResult, runErr = runner.Run(ctx, spec, func(id domain.ProcessIdentity) error {
			startedPID = id.PID
			// Cancel context shortly after start
			go func() {
				time.Sleep(50 * time.Millisecond)
				cancel()
			}()
			return nil
		}, nil)
	}()

	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not terminate within bounded time on cancellation")
	}

	if runErr == nil && runResult.ExitCode == 0 {
		t.Errorf("expected non-zero or error on cancellation, got exit code %d, err %v", runResult.ExitCode, runErr)
	}

	// Verify that the process group was killed and no process remains with startedPID
	if startedPID > 0 {
		// In Linux, kill(-startedPID, 0) checks if any process in process group is alive
		err := syscall.Kill(-startedPID, 0)
		if err == nil {
			t.Errorf("process group %d still has surviving members after cancellation", startedPID)
		}
	}
}

// TestStalePIDNeverSignalled verifies no signal is sent to a stale PID.
func TestStalePIDNeverSignalled(t *testing.T) {
	// 1. Non-existent PID
	err := process.SignalProcess(domain.ProcessIdentity{
		PID:        99999999,
		BootID:     "valid-looking-boot-id",
		StartTicks: 1000,
	}, syscall.SIGTERM)
	if err == nil {
		t.Error("expected error signalling non-existent PID, got nil")
	}

	// 2. Mismatched BootID
	currentPid := os.Getpid()
	err = process.SignalProcess(domain.ProcessIdentity{
		PID:        currentPid,
		BootID:     "00000000-0000-0000-0000-000000000000", // Clearly mismatched boot id
		StartTicks: 1,
	}, syscall.SIGTERM)
	if err == nil {
		t.Error("expected error signalling mismatched boot id, got nil")
	}
	if !errors.Is(err, process.ErrStalePID) {
		t.Errorf("expected ErrStalePID, got %v", err)
	}

	// 3. Mismatched StartTicks
	err = process.SignalProcess(domain.ProcessIdentity{
		PID:        currentPid,
		BootID:     "",         // Will be verified against current boot ID
		StartTicks: 9999999999, // Impossible start ticks for current running test process
	}, syscall.SIGTERM)
	if err == nil {
		t.Error("expected error signalling mismatched start ticks, got nil")
	}
	if !errors.Is(err, process.ErrStalePID) {
		t.Errorf("expected ErrStalePID for mismatched start ticks, got %v", err)
	}
}

// TestCallbackFailureCancels asserts that onStart returning an error immediately kills and reaps the process.
func TestCallbackFailureCancels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runner := process.NewRunner()
	callbackErr := errors.New("simulated store failure recording identity")

	var capturedPID int
	spec := ports.CommandSpec{
		Path: fakeCLIBin,
		Args: []string{"--mode", "sleep-worker", "--sleep", "30s"},
	}

	_, err := runner.Run(ctx, spec, func(id domain.ProcessIdentity) error {
		capturedPID = id.PID
		return callbackErr
	}, nil)

	if !errors.Is(err, callbackErr) {
		t.Fatalf("expected callback error %v, got %v", callbackErr, err)
	}

	if capturedPID <= 0 {
		t.Fatal("expected positive captured PID")
	}

	// Verify the process was killed and does not remain alive
	// Give small moment for OS reap check
	time.Sleep(50 * time.Millisecond)
	err = syscall.Kill(capturedPID, 0)
	if err == nil {
		t.Errorf("process %d is still alive after onStart failure", capturedPID)
	}
}

// TestArgumentsRemainLiteral asserts arguments are passed directly without shell expansion or word splitting.
func TestArgumentsRemainLiteral(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runner := process.NewRunner()

	var stdoutBuf bytes.Buffer
	testArgs := []string{
		"arg with spaces",
		"\"quoted string\"",
		"$PATH",
		"*.go",
		"a;b && c",
	}

	spec := ports.CommandSpec{
		Path: fakeCLIBin,
		Args: append([]string{"--mode", "echo-args", "--"}, testArgs...),
	}

	result, err := runner.Run(ctx, spec, nil, func(rec ports.StreamRecord) error {
		if rec.Kind == ports.StreamStdout {
			stdoutBuf.Write(rec.Data)
		}
		return nil
	})

	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}

	out := stdoutBuf.String()
	for i, arg := range testArgs {
		expectedLine := "ARG[" + string(rune('0'+i)) + "]=" + arg
		if !strings.Contains(out, expectedLine) {
			t.Errorf("expected literal argument %q in output, got:\n%s", expectedLine, out)
		}
	}
}

// TestOversizeRecord asserts streams exceeding 1 MiB limit are split safely into records <= 1 MiB.
func TestOversizeRecord(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runner := process.NewRunner()

	const floodSize = 2500000 // ~2.38 MiB
	spec := ports.CommandSpec{
		Path: fakeCLIBin,
		Args: []string{"--mode", "flood-output", "--flood-bytes", "2500000"},
	}

	var (
		mu           sync.Mutex
		totalBytes   int
		maxChunkSize int
	)

	result, err := runner.Run(ctx, spec, nil, func(rec ports.StreamRecord) error {
		mu.Lock()
		defer mu.Unlock()
		if rec.Kind == ports.StreamStderr {
			totalBytes += len(rec.Data)
			if len(rec.Data) > maxChunkSize {
				maxChunkSize = len(rec.Data)
			}
			if len(rec.Data) > 1024*1024 {
				t.Errorf("record chunk size %d exceeded 1 MiB limit", len(rec.Data))
			}
		}
		return nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if totalBytes < floodSize {
		t.Errorf("expected at least %d bytes, got %d", floodSize, totalBytes)
	}
	if maxChunkSize > 1024*1024 {
		t.Errorf("max chunk size %d exceeded 1 MiB", maxChunkSize)
	}
}

// TestDescendantUncertainty asserts that surviving descendants cause Settled = false.
func TestDescendantUncertainty(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runner := process.NewRunner()

	spec := ports.CommandSpec{
		Path: fakeCLIBin,
		Args: []string{"--mode", "blocked-child"},
	}

	result, err := runner.Run(ctx, spec, nil, nil)
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	// Main process exited 0, but background child is still alive in the group!
	if result.ExitCode != 0 {
		t.Errorf("expected main process exit code 0, got %d", result.ExitCode)
	}
	if result.Settled {
		t.Errorf("expected Settled = false due to surviving child, got true")
	}
	if result.DescendantsReaped {
		t.Errorf("expected DescendantsReaped = false, got true")
	}
	if result.SettlementConfidence != ports.SettlementUncertain {
		t.Errorf("expected settlement confidence %s, got %s", ports.SettlementUncertain, result.SettlementConfidence)
	}
}
