package tests

import (
	"bufio"
	"context"
	"database/sql"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Diaszano/quota-guardian/internal/adapters/persistence"
	"github.com/Diaszano/quota-guardian/internal/application"
	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

func buildCrashDaemon(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crashdaemon")
	cmd := exec.Command("go", "build", "-o", path, "../internal/testutil/crashdaemon")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build crash helper: %v: %s", err, out)
	}
	return path
}

func createCrashSession(t *testing.T, store *persistence.Store) domain.Session {
	t.Helper()
	return createManagedSession(t, store, "crash")
}

func createManagedSession(t *testing.T, store *persistence.Store, provider string) domain.Session {
	t.Helper()
	session, err := store.Create(context.Background(), ports.NewSession{
		Provider: provider, ContextID: t.TempDir(), Mode: domain.ModeManaged,
		Context: domain.ExecutionContext{ExecutablePath: "/bin/agent", ProviderVersion: "1", Cwd: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func crashAt(t *testing.T, helper, stateDir, sessionID, stage string) {
	t.Helper()
	cmd := exec.Command(helper, stateDir, sessionID, stage)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("wait for %s barrier: %v", stage, err)
	}
	fields := strings.Fields(line)
	if len(fields) != 3 || fields[0] != "READY" {
		t.Fatalf("unexpected crash helper barrier: %q", line)
	}
	childPID, err := strconv.Atoi(fields[2])
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if childPID > 0 {
		_ = syscall.Kill(-childPID, syscall.SIGKILL)
	}
}

func assertUncertainAttempt(t *testing.T, stateDir, sessionID string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(stateDir, "quota-guardian.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	var phase string
	if err := db.QueryRow("SELECT COUNT(*), MIN(phase) FROM attempts WHERE session_id = ?", sessionID).Scan(&count, &phase); err != nil {
		t.Fatal(err)
	}
	if count != 1 || phase != string(domain.AttemptPhaseUncertain) {
		t.Fatalf("attempts=%d phase=%s, want one uncertain attempt", count, phase)
	}
}

func TestCrashWindows(t *testing.T) {
	helper := buildCrashDaemon(t)
	for _, stage := range []string{"claimed", "launching", "spawn", "identity", "exit-before-commit"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			dir := filepath.Join(t.TempDir(), "state")
			store, err := persistence.Open(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			session := createCrashSession(t, store)
			crashAt(t, helper, dir, session.ID, stage)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := persistence.Open(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if err := application.Recover(ctx, reopened, "new-owner-epoch", time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			got, err := reopened.Get(ctx, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != domain.NeedsAttention {
				t.Fatalf("state after %s crash = %s, want NEEDS_ATTENTION", stage, got.State)
			}
			assertUncertainAttempt(t, dir, session.ID)
			if _, claimed, err := reopened.ClaimStart(ctx, ports.ClaimRequest{SessionID: session.ID, ExpectedRevision: got.Revision, OwnerEpoch: "new-owner-epoch", Now: time.Now().UTC(), LeaseDuration: time.Minute}); err != nil || claimed {
				t.Fatalf("claim after crash recovery: claimed=%v err=%v", claimed, err)
			}
		})
	}
}

func TestWaitingSurvivesRebootIdentity(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "state")
	store, err := persistence.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	waiting := createCrashSession(t, store)
	attempt, claimed, err := store.ClaimStart(ctx, ports.ClaimRequest{SessionID: waiting.ID, ExpectedRevision: waiting.Revision, OwnerEpoch: "old", Now: now, LeaseDuration: time.Hour})
	if err != nil || !claimed {
		t.Fatalf("claim waiting fixture: claimed=%v err=%v", claimed, err)
	}
	if err := store.MarkLaunching(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	resetAt := now.Add(time.Hour)
	if err := store.FinishAttempt(ctx, ports.AttemptResult{AttemptID: attempt.ID, Fence: attempt.Fence, FinishedAt: now, Transition: domain.Transition{
		ExpectedState: domain.Running, Destination: domain.QuotaExhausted, AttemptID: attempt.ID, Fence: attempt.Fence,
		Evidence: domain.Evidence{Settled: true, Failure: domain.Failure{Category: domain.Quota, ResetAt: &resetAt}, ObservedAt: now},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(ctx, domain.Transition{ExpectedState: domain.QuotaExhausted, Destination: domain.WaitingForReset, AttemptID: attempt.ID, Fence: attempt.Fence, Evidence: domain.Evidence{Failure: domain.Failure{Category: domain.Quota, ResetAt: &resetAt}, ObservedAt: now}}); err != nil {
		t.Fatal(err)
	}
	before, err := store.Get(ctx, waiting.ID)
	if err != nil {
		t.Fatal(err)
	}
	crashed := createCrashSession(t, store)
	helper := buildCrashDaemon(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	crashAt(t, helper, dir, crashed.ID, "identity")
	reopened, err := persistence.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := application.Recover(ctx, reopened, "new-boot-epoch", now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	after, err := reopened.Get(ctx, waiting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != domain.WaitingForReset || before.NextRetryAt == nil || after.NextRetryAt == nil || !before.NextRetryAt.Equal(*after.NextRetryAt) {
		t.Fatalf("waiting session changed across reboot: before=%+v after=%+v", before, after)
	}
	assertUncertainAttempt(t, dir, crashed.ID)
}
