package persistence

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

func TestUnsettledAttemptRemainsOpen(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	session, err := store.Create(ctx, ports.NewSession{
		Provider: "test", ContextID: "workspace", Mode: domain.ModeManaged,
		Context: domain.ExecutionContext{ExecutablePath: "/bin/agent", ProviderVersion: "1", Cwd: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	attempt, claimed, err := store.ClaimStart(ctx, ports.ClaimRequest{SessionID: session.ID, ExpectedRevision: session.Revision, OwnerEpoch: "epoch", Now: now, LeaseDuration: time.Minute})
	if err != nil || !claimed {
		t.Fatalf("ClaimStart() = claimed %v, err %v", claimed, err)
	}
	if err := store.MarkLaunching(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkUncertain(ctx, attempt, domain.Evidence{ObservedAt: now}); err != nil {
		t.Fatal(err)
	}
	var phase string
	if err := store.db.QueryRowContext(ctx, "SELECT phase FROM attempts WHERE id = ?", attempt.ID).Scan(&phase); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if phase != string(domain.AttemptPhaseUncertain) || got.State != domain.NeedsAttention {
		t.Fatalf("phase=%s session_state=%s, want uncertain/needs attention", phase, got.State)
	}
}

func TestCancelRequestBlocksCompletion(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := store.Create(ctx, ports.NewSession{
		Provider: "test", ContextID: "workspace", Mode: domain.ModeManaged,
		Context: domain.ExecutionContext{ExecutablePath: "/bin/agent", ProviderVersion: "1", Cwd: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	attempt, claimed, err := store.ClaimStart(ctx, ports.ClaimRequest{SessionID: session.ID, ExpectedRevision: session.Revision, OwnerEpoch: "epoch", Now: time.Now().UTC(), LeaseDuration: time.Minute})
	if err != nil || !claimed {
		t.Fatalf("ClaimStart() = claimed %v, err %v", claimed, err)
	}
	if _, err := store.Cancel(ctx, session.ID, session.Revision+1); err != nil {
		t.Fatal(err)
	}
	err = store.FinishAttempt(ctx, ports.AttemptResult{AttemptID: attempt.ID, Fence: attempt.Fence, Transition: domain.Transition{
		ExpectedState: domain.Running, Destination: domain.Completed, AttemptID: attempt.ID, Fence: attempt.Fence,
		Evidence: domain.Evidence{Settled: true, TurnCompleted: true},
	}})
	if err == nil {
		t.Fatal("FinishAttempt() completed after cancellation had been requested")
	}
	got, err := store.Get(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.Running || !got.CancelRequested {
		t.Fatalf("state=%s cancel_requested=%v, want RUNNING/true until child settles", got.State, got.CancelRequested)
	}
}
