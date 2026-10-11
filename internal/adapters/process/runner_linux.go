//go:build linux

package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
	"golang.org/x/sys/unix"
)

// ErrStalePID indicates that a process identity does not match the active host process.
var ErrStalePID = errors.New("stale process identity: process has changed or reboot occurred")

const (
	maxRecordSize        = 1024 * 1024 // 1 MiB record limit
	cancellationGraceDur = 500 * time.Millisecond
)

var (
	bootIDOnce sync.Once
	cachedBoot string
)

func init() {
	// Enable child subreaper on Linux so orphaned descendants are reparented
	// to this process, allowing deterministic descendant tracking and reaping.
	_ = unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0)
}

// Runner implements ports.ProcessRunner on Linux platforms.
type Runner struct{}

// NewRunner creates a new Linux ProcessRunner.
func NewRunner() *Runner {
	return &Runner{}
}

// Run spawns and manages a bounded, cancellable external process group.
func (r *Runner) Run(
	ctx context.Context,
	spec ports.CommandSpec,
	onStart func(domain.ProcessIdentity) error,
	onRecord func(ports.StreamRecord) error,
) (ports.ProcessResult, error) {
	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	if len(spec.Env) > 0 {
		cmd.Env = spec.Env
	}

	// Create dedicated process group
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Setup stdin deterministically
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return ports.ProcessResult{}, fmt.Errorf("failed to open stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdinPipe.Close()
		return ports.ProcessResult{}, fmt.Errorf("failed to open stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		return ports.ProcessResult{}, fmt.Errorf("failed to open stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
		return ports.ProcessResult{}, fmt.Errorf("failed to start process: %w", err)
	}

	pid := cmd.Process.Pid
	pgid := pid // Since Setpgid = true, child is group leader with pgid == pid

	// Try to open pidfd for race-free process signaling
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		pidfd = -1
	}
	defer func() {
		if pidfd >= 0 {
			_ = unix.Close(pidfd)
		}
	}()

	// Feed stdin deterministically
	if spec.Stdin != nil {
		go func() {
			defer stdinPipe.Close()
			_, _ = io.Copy(stdinPipe, spec.Stdin)
		}()
	} else {
		_ = stdinPipe.Close()
	}

	// Resolve process identity before onStart
	bootID, _ := getBootID()
	startTicks, _ := getProcessStartTicks(pid)
	identity := domain.ProcessIdentity{
		PID:        pid,
		BootID:     bootID,
		StartTicks: startTicks,
	}

	if onStart != nil {
		if err := onStart(identity); err != nil {
			killProcessGroup(pgid, pidfd, syscall.SIGKILL)
			_ = cmd.Wait()
			reapDescendants(pgid)
			return ports.ProcessResult{
				ExitCode:             -1,
				Reaped:               true,
				Settled:              true,
				DescendantsReaped:    true,
				SettlementConfidence: ports.SettlementConfirmed,
			}, err
		}
	}

	// Drain streams concurrently
	drainErrCh := make(chan error, 2)
	var streamWg sync.WaitGroup
	streamWg.Add(2)

	go func() {
		defer streamWg.Done()
		drainStream(ports.StreamStdout, stdoutPipe, onRecord, drainErrCh)
	}()

	go func() {
		defer streamWg.Done()
		drainStream(ports.StreamStderr, stderrPipe, onRecord, drainErrCh)
	}()

	// Context cancellation supervisor
	waitDone := make(chan struct{})
	var cancelTriggered atomic.Bool
	go func() {
		select {
		case <-ctx.Done():
			cancelTriggered.Store(true)
			killProcessGroup(pgid, pidfd, syscall.SIGTERM)
			select {
			case <-waitDone:
				return
			case <-time.After(cancellationGraceDur):
				killProcessGroup(pgid, pidfd, syscall.SIGKILL)
			}
		case <-waitDone:
			return
		}
	}()

	// Wait for process to exit in background so streams and cancellation can proceed
	waitErrCh := make(chan error, 1)
	go func() {
		waitErrCh <- cmd.Wait()
		close(waitDone)
	}()

	// Wait for streams to finish draining
	streamWg.Wait()
	waitErr := <-waitErrCh

	// Check if any stream draining returned an error
	var drainErr error
	select {
	case drainErr = <-drainErrCh:
	default:
	}

	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				if ws.Signaled() {
					exitCode = 128 + int(ws.Signal())
				} else {
					exitCode = ws.ExitStatus()
				}
			} else {
				exitCode = exitErr.ExitCode()
			}
		} else {
			exitCode = -1
		}
	}

	// If cancellation was triggered, ensure the entire process group is reaped
	if cancelTriggered.Load() {
		killAndReapProcessGroup(pgid)
	}

	// Verify settlement of descendants in the process group
	survivingDescendants := checkProcessGroupLiving(pgid)
	settled := !survivingDescendants
	descendantsReaped := !survivingDescendants
	confidence := ports.SettlementConfirmed

	if survivingDescendants {
		settled = false
		descendantsReaped = false
		confidence = ports.SettlementUncertain

		// Clean up rogue survivors so they don't leak into the system
		killAndReapProcessGroup(pgid)
	}

	result := ports.ProcessResult{
		ExitCode:             exitCode,
		Reaped:               true,
		Settled:              settled,
		DescendantsReaped:    descendantsReaped,
		SettlementConfidence: confidence,
	}

	if cancelTriggered.Load() && ctx.Err() != nil {
		return result, ctx.Err()
	}
	if drainErr != nil {
		return result, drainErr
	}
	return result, nil
}

func drainStream(
	kind ports.StreamKind,
	r io.Reader,
	onRecord func(ports.StreamRecord) error,
	errCh chan<- error,
) {
	buf := make([]byte, maxRecordSize)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if onRecord != nil {
				if rErr := onRecord(ports.StreamRecord{Kind: kind, Data: chunk}); rErr != nil {
					select {
					case errCh <- rErr:
					default:
					}
					return
				}
			}
		}
		if err != nil {
			if err != io.EOF && !errors.Is(err, os.ErrClosed) && !strings.Contains(err.Error(), "file already closed") {
				select {
				case errCh <- err:
				default:
				}
			}
			return
		}
	}
}

func killProcessGroup(pgid int, pidfd int, sig syscall.Signal) {
	if pidfd >= 0 {
		_ = unix.PidfdSendSignal(pidfd, sig, nil, 0)
	}
	if pgid > 0 {
		_ = syscall.Kill(-pgid, sig)
	}
}

func checkProcessGroupLiving(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	reapDescendants(pgid)
	err := syscall.Kill(-pgid, 0)
	return err == nil
}

func killAndReapProcessGroup(pgid int) {
	if pgid <= 0 {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		reapDescendants(pgid)
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func reapDescendants(pgid int) {
	for {
		var ws syscall.WaitStatus
		wpid, err := syscall.Wait4(-pgid, &ws, syscall.WNOHANG, nil)
		if wpid <= 0 || err != nil {
			break
		}
	}
}

// SignalProcess delivers a signal to an existing process identity only after verifying
// that the PID is not stale (verifies BootID, existence in /proc, and StartTicks).
func SignalProcess(identity domain.ProcessIdentity, sig syscall.Signal) error {
	if identity.PID <= 0 {
		return fmt.Errorf("%w: invalid PID %d", ErrStalePID, identity.PID)
	}

	// 1. Verify BootID if known
	currentBoot, err := getBootID()
	if err == nil && currentBoot != "" && identity.BootID != "" {
		if identity.BootID != currentBoot {
			return fmt.Errorf("%w: boot ID mismatch (persisted %s, current %s)", ErrStalePID, identity.BootID, currentBoot)
		}
	}

	// 2. Check existence in /proc
	procStatPath := fmt.Sprintf("/proc/%d/stat", identity.PID)
	if _, err := os.Stat(procStatPath); err != nil {
		return fmt.Errorf("%w: process %d does not exist: %v", ErrStalePID, identity.PID, err)
	}

	// 3. Verify StartTicks
	actualTicks, err := getProcessStartTicks(identity.PID)
	if err != nil {
		return fmt.Errorf("%w: unable to read start ticks for PID %d: %v", ErrStalePID, identity.PID, err)
	}
	if identity.StartTicks > 0 && actualTicks != identity.StartTicks {
		return fmt.Errorf("%w: start ticks mismatch for PID %d (persisted %d, actual %d)", ErrStalePID, identity.PID, identity.StartTicks, actualTicks)
	}

	// 4. Prefer held pidfd
	pidfd, err := unix.PidfdOpen(identity.PID, 0)
	if err == nil {
		defer unix.Close(pidfd)
		return unix.PidfdSendSignal(pidfd, sig, nil, 0)
	}

	return syscall.Kill(identity.PID, sig)
}

func getBootID() (string, error) {
	var readErr error
	bootIDOnce.Do(func() {
		data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
		if err != nil {
			readErr = err
			return
		}
		cachedBoot = strings.TrimSpace(string(data))
	})
	return cachedBoot, readErr
}

func getProcessStartTicks(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	lastParen := bytes.LastIndexByte(data, ')')
	if lastParen == -1 || lastParen+2 >= len(data) {
		return 0, errors.New("malformed /proc/pid/stat")
	}
	fields := bytes.Fields(data[lastParen+2:])
	// field 3 (state) is index 0
	// field 22 (starttime) is index 19
	if len(fields) <= 19 {
		return 0, errors.New("insufficient fields in /proc/pid/stat")
	}
	return strconv.ParseUint(string(fields[19]), 10, 64)
}
