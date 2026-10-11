package persistence_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Diaszano/quota-guardian/internal/adapters/persistence"
	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

func TestRestartPreservesWaiting(t *testing.T) {
	ctx := context.Background()
	store, stateDir, cleanup := setupTestStore(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	futureReset := now.Add(2 * time.Hour)
	pastReset := now.Add(-10 * time.Minute)

	// Session 1: WAITING_FOR_RESET
	s1, err := store.Create(ctx, ports.NewSession{
		Provider:  "codex",
		ContextID: "ctx-waiting",
		Mode:      domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/usr/bin/codex",
			ProviderVersion: "1.0.0",
			Cwd:             "/workspace",
			Profile:         "default",
		},
		Consent: domain.Consent{
			Revision:              1,
			Enabled:               true,
			BackgroundAllowed:     true,
			ApprovedContextDigest: "digest",
			ApprovedAt:            &now,
		},
	})
	if err != nil {
		t.Fatalf("failed to create session 1: %v", err)
	}

	// Advance s1 to WAITING_FOR_RESET
	s1 = advanceToWaitingForReset(t, store, s1, futureReset)
	// Artificially simulate prior retry count = 2
	_, err = store.DB().ExecContext(ctx, "UPDATE sessions SET retry_count = 2 WHERE id = ?", s1.ID)
	if err != nil {
		t.Fatalf("failed to update retry_count: %v", err)
	}

	// Session 2: READY_TO_RESUME
	s2, err := store.Create(ctx, ports.NewSession{
		Provider:  "codex",
		ContextID: "ctx-ready",
		Mode:      domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/usr/bin/codex",
			ProviderVersion: "1.0.0",
			Cwd:             "/workspace",
			Profile:         "default",
		},
	})
	if err != nil {
		t.Fatalf("failed to create session 2: %v", err)
	}
	s2 = advanceToReadyToResume(t, store, s2, pastReset)
	_, err = store.DB().ExecContext(ctx, "UPDATE sessions SET retry_count = 1 WHERE id = ?", s2.ID)
	if err != nil {
		t.Fatalf("failed to update retry_count: %v", err)
	}

	// Close store to simulate daemon shutdown
	if err := store.Close(); err != nil {
		t.Fatalf("failed to close store: %v", err)
	}

	// Reopen store in new daemon instance
	reopenedStore, err := persistence.Open(ctx, stateDir)
	if err != nil {
		t.Fatalf("failed to reopen store: %v", err)
	}
	defer reopenedStore.Close()

	// Run Reconcile on new daemon instance with new epoch
	reconcileEpoch := "daemon-epoch-restart-1"
	if err := reopenedStore.Reconcile(ctx, reconcileEpoch, now); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	// Verify Session 1: WAITING_FOR_RESET preserves next_retry_at, retry_count, and consent
	gotS1, err := reopenedStore.Get(ctx, s1.ID)
	if err != nil {
		t.Fatalf("failed to get s1 after restart: %v", err)
	}
	if gotS1.State != domain.WaitingForReset {
		t.Errorf("expected s1 state WAITING_FOR_RESET, got %s", gotS1.State)
	}
	if gotS1.NextRetryAt == nil || !gotS1.NextRetryAt.Equal(futureReset) {
		t.Errorf("expected s1 NextRetryAt %v, got %v", futureReset, gotS1.NextRetryAt)
	}
	if gotS1.RetryCount != 2 {
		t.Errorf("expected s1 RetryCount 2, got %d", gotS1.RetryCount)
	}
	if !gotS1.Consent.Enabled || !gotS1.Consent.BackgroundAllowed {
		t.Errorf("expected s1 consent preserved, got %+v", gotS1.Consent)
	}

	// Verify Session 2: READY_TO_RESUME preserves state and retry_count
	gotS2, err := reopenedStore.Get(ctx, s2.ID)
	if err != nil {
		t.Fatalf("failed to get s2 after restart: %v", err)
	}
	if gotS2.State != domain.ReadyToResume {
		t.Errorf("expected s2 state READY_TO_RESUME, got %s", gotS2.State)
	}
	if gotS2.RetryCount != 1 {
		t.Errorf("expected s2 RetryCount 1, got %d", gotS2.RetryCount)
	}
}

func TestRestartUnfinishedAttemptsUncertain(t *testing.T) {
	ctx := context.Background()
	store, stateDir, cleanup := setupTestStore(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	oldEpoch := "old-epoch-crash"

	// Create 3 sessions
	var sessions []domain.Session
	for i := 0; i < 3; i++ {
		s, err := store.Create(ctx, ports.NewSession{
			Provider:  "codex",
			ContextID: filepath.Join("ctx", newTestID()),
			Mode:      domain.ModeManaged,
			Context: domain.ExecutionContext{
				ExecutablePath:  "/usr/bin/codex",
				ProviderVersion: "1.0.0",
				Cwd:             "/workspace",
				Profile:         "default",
			},
		})
		if err != nil {
			t.Fatalf("failed to create session %d: %v", i, err)
		}
		sessions = append(sessions, s)
	}

	// Attempt 1: claimed, nil PID
	att1, claimed, err := store.ClaimStart(ctx, ports.ClaimRequest{
		SessionID:     sessions[0].ID,
		OwnerEpoch:    oldEpoch,
		LeaseDuration: 10 * time.Minute,
		Now:           now,
	})
	if err != nil || !claimed {
		t.Fatalf("failed to claim attempt 1: claimed=%v, err=%v", claimed, err)
	}

	// Attempt 2: launching, nil PID
	att2, claimed, err := store.ClaimStart(ctx, ports.ClaimRequest{
		SessionID:     sessions[1].ID,
		OwnerEpoch:    oldEpoch,
		LeaseDuration: 10 * time.Minute,
		Now:           now,
	})
	if err != nil || !claimed {
		t.Fatalf("failed to claim attempt 2: claimed=%v, err=%v", claimed, err)
	}
	if err := store.MarkLaunching(ctx, att1); err == nil {
		// intentional check on wrong att ID
	}
	if err := store.MarkLaunching(ctx, att2); err != nil {
		t.Fatalf("failed to mark launching: %v", err)
	}

	// Attempt 3: active, recorded PID
	att3, claimed, err := store.ClaimStart(ctx, ports.ClaimRequest{
		SessionID:     sessions[2].ID,
		OwnerEpoch:    oldEpoch,
		LeaseDuration: 10 * time.Minute,
		Now:           now,
	})
	if err != nil || !claimed {
		t.Fatalf("failed to claim attempt 3: claimed=%v, err=%v", claimed, err)
	}
	if err := store.MarkLaunching(ctx, att3); err != nil {
		t.Fatalf("failed to mark launching att3: %v", err)
	}
	if err := store.RecordIdentity(ctx, att3, domain.ProcessIdentity{
		PID:        4567,
		BootID:     "boot-xyz",
		StartTicks: 12345,
	}); err != nil {
		t.Fatalf("failed to record identity att3: %v", err)
	}

	// Crash: close store and reopen with new epoch
	if err := store.Close(); err != nil {
		t.Fatalf("failed to close store: %v", err)
	}
	reopenedStore, err := persistence.Open(ctx, stateDir)
	if err != nil {
		t.Fatalf("failed to reopen store: %v", err)
	}
	defer reopenedStore.Close()

	newEpoch := "new-epoch-active"
	if err := reopenedStore.Reconcile(ctx, newEpoch, now.Add(time.Second)); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	// Assert: all 3 attempts became 'uncertain'
	for _, attID := range []string{att1.ID, att2.ID, att3.ID} {
		var phase string
		err := reopenedStore.DB().QueryRowContext(ctx, "SELECT phase FROM attempts WHERE id = ?", attID).Scan(&phase)
		if err != nil {
			t.Fatalf("failed to query attempt %s: %v", attID, err)
		}
		if phase != string(domain.AttemptPhaseUncertain) {
			t.Errorf("attempt %s expected phase uncertain, got %s", attID, phase)
		}
	}

	// Assert: all 3 sessions became 'NEEDS_ATTENTION' with crash_recovery_uncertain
	for i, s := range sessions {
		sess, err := reopenedStore.Get(ctx, s.ID)
		if err != nil {
			t.Fatalf("failed to get session %d: %v", i, err)
		}
		if sess.State != domain.NeedsAttention {
			t.Errorf("session %s expected state NEEDS_ATTENTION, got %s", s.ID, sess.State)
		}
		if sess.LastError == nil || sess.LastError.Code != "crash_recovery_uncertain" {
			t.Errorf("session %s expected last_error code 'crash_recovery_uncertain', got %v", s.ID, sess.LastError)
		}

		// Assert: no new attempt claimable!
		_, canClaimStart, _ := reopenedStore.ClaimStart(ctx, ports.ClaimRequest{
			SessionID:     s.ID,
			OwnerEpoch:    newEpoch,
			LeaseDuration: 5 * time.Minute,
			Now:           now.Add(time.Minute),
		})
		if canClaimStart {
			t.Errorf("session %s must NOT be claimable via ClaimStart while in NEEDS_ATTENTION with uncertain attempt", s.ID)
		}

		_, canClaimDue, _ := reopenedStore.ClaimDue(ctx, ports.ClaimRequest{
			SessionID:     s.ID,
			OwnerEpoch:    newEpoch,
			LeaseDuration: 5 * time.Minute,
			Now:           now.Add(time.Minute),
		})
		if canClaimDue {
			t.Errorf("session %s must NOT be claimable via ClaimDue while in NEEDS_ATTENTION with uncertain attempt", s.ID)
		}
	}
}

func TestExpiredLeaseDoesNotReleaseAttempt(t *testing.T) {
	ctx := context.Background()
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	currentEpoch := "epoch-alpha"

	s, err := store.Create(ctx, ports.NewSession{
		Provider:  "codex",
		ContextID: "ctx-lease-expire",
		Mode:      domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/usr/bin/codex",
			ProviderVersion: "1.0.0",
			Cwd:             "/workspace",
			Profile:         "default",
		},
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Claim with lease duration of 1 millisecond
	att, claimed, err := store.ClaimStart(ctx, ports.ClaimRequest{
		SessionID:     s.ID,
		OwnerEpoch:    currentEpoch,
		LeaseDuration: time.Millisecond,
		Now:           now,
	})
	if err != nil || !claimed {
		t.Fatalf("failed to claim attempt: %v", err)
	}

	// Time is now after lease deadline
	afterLease := now.Add(5 * time.Minute)

	// Even with expired lease, before reconcile, ClaimDue and ClaimStart MUST NOT release attempt
	_, canClaimStart, _ := store.ClaimStart(ctx, ports.ClaimRequest{
		SessionID:     s.ID,
		OwnerEpoch:    currentEpoch,
		LeaseDuration: 5 * time.Minute,
		Now:           afterLease,
	})
	if canClaimStart {
		t.Fatal("ClaimStart must NOT succeed on session with open attempt even if lease expired")
	}

	_, canClaimDue, _ := store.ClaimDue(ctx, ports.ClaimRequest{
		SessionID:     s.ID,
		OwnerEpoch:    currentEpoch,
		LeaseDuration: 5 * time.Minute,
		Now:           afterLease,
	})
	if canClaimDue {
		t.Fatal("ClaimDue must NOT succeed on session with open attempt even if lease expired")
	}

	// Now run Reconcile
	if err := store.Reconcile(ctx, currentEpoch, afterLease); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	// Attempt must become uncertain and session must become NEEDS_ATTENTION
	var phase string
	err = store.DB().QueryRowContext(ctx, "SELECT phase FROM attempts WHERE id = ?", att.ID).Scan(&phase)
	if err != nil {
		t.Fatalf("failed to query attempt phase: %v", err)
	}
	if phase != string(domain.AttemptPhaseUncertain) {
		t.Fatalf("expected attempt phase uncertain after expired lease reconcile, got %s", phase)
	}

	sess, err := store.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	if sess.State != domain.NeedsAttention {
		t.Fatalf("expected session state NEEDS_ATTENTION, got %s", sess.State)
	}

	// ClaimDue and ClaimStart MUST STILL block!
	_, canClaimStart, _ = store.ClaimStart(ctx, ports.ClaimRequest{
		SessionID:     s.ID,
		OwnerEpoch:    currentEpoch,
		LeaseDuration: 5 * time.Minute,
		Now:           afterLease.Add(time.Minute),
	})
	if canClaimStart {
		t.Fatal("ClaimStart must NOT succeed on uncertain attempt")
	}

	_, canClaimDue, _ = store.ClaimDue(ctx, ports.ClaimRequest{
		SessionID:     s.ID,
		OwnerEpoch:    currentEpoch,
		LeaseDuration: 5 * time.Minute,
		Now:           afterLease.Add(time.Minute),
	})
	if canClaimDue {
		t.Fatal("ClaimDue must NOT succeed on uncertain attempt")
	}
}

func TestResolveUncertainAttempt(t *testing.T) {
	ctx := context.Background()
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	epoch1 := "epoch-1"

	// 1. Session with uncertain attempt and running process cannot be resolved by manual ack alone
	s1, err := store.Create(ctx, ports.NewSession{
		Provider:  "codex",
		ContextID: "ctx-running-proc",
		Mode:      domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/usr/bin/codex",
			ProviderVersion: "1.0.0",
			Cwd:             "/workspace",
			Profile:         "default",
		},
	})
	if err != nil {
		t.Fatalf("failed to create session 1: %v", err)
	}
	att1, _, err := store.ClaimStart(ctx, ports.ClaimRequest{
		SessionID:     s1.ID,
		OwnerEpoch:    epoch1,
		LeaseDuration: 5 * time.Minute,
		Now:           now,
	})
	if err != nil {
		t.Fatalf("failed to claim start: %v", err)
	}
	if err := store.MarkLaunching(ctx, att1); err != nil {
		t.Fatalf("failed to mark launching: %v", err)
	}
	if err := store.RecordIdentity(ctx, att1, domain.ProcessIdentity{
		PID:        9991,
		BootID:     "boot-current",
		StartTicks: 5555,
	}); err != nil {
		t.Fatalf("failed to record identity: %v", err)
	}

	// Reconcile with new epoch -> marks attempt uncertain, session NEEDS_ATTENTION
	if err := store.Reconcile(ctx, "epoch-2", now.Add(time.Minute)); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	// Set process checker simulating process STILL RUNNING
	store.SetProcessChecker(func(pid int, bootID string, startTicks uint64) bool {
		return true // alive!
	})

	// Operator tries to resolve with abandon / mark_failed while process is alive
	err = store.ResolveUncertain(ctx, ports.ResolveUncertainRequest{
		SessionID:   s1.ID,
		AttemptID:   att1.ID,
		Resolution:  ports.ResolutionAbandon,
		Operator:    "operator-dias",
		Reason:      "manual acknowledgement without stopping process",
		Destination: domain.Failed,
	})
	if err == nil {
		t.Fatal("expected ResolveUncertain to FAIL when process is still running")
	}

	// Session must remain in NEEDS_ATTENTION
	gotS1, err := store.Get(ctx, s1.ID)
	if err != nil {
		t.Fatalf("failed to get s1: %v", err)
	}
	if gotS1.State != domain.NeedsAttention {
		t.Fatalf("session must remain in NEEDS_ATTENTION, got %s", gotS1.State)
	}

	// 2. Process exits / verified dead -> operator can explicitly resolve
	store.SetProcessChecker(func(pid int, bootID string, startTicks uint64) bool {
		return false // dead
	})

	err = store.ResolveUncertain(ctx, ports.ResolveUncertainRequest{
		SessionID:   s1.ID,
		AttemptID:   att1.ID,
		Resolution:  ports.ResolutionAbandon,
		Operator:    "operator-dias",
		Reason:      "process confirmed terminated and unrecoverable",
		Destination: domain.Failed,
	})
	if err != nil {
		t.Fatalf("ResolveUncertain failed after process terminated: %v", err)
	}

	// Verify attempt phase is now finished, outcome_code is abandon
	var phase, outcome string
	err = store.DB().QueryRowContext(ctx, "SELECT phase, outcome_code FROM attempts WHERE id = ?", att1.ID).Scan(&phase, &outcome)
	if err != nil {
		t.Fatalf("failed to query attempt: %v", err)
	}
	if phase != string(domain.AttemptPhaseFinished) {
		t.Errorf("expected attempt phase finished, got %s", phase)
	}
	if outcome != string(ports.ResolutionAbandon) {
		t.Errorf("expected outcome_code %s, got %s", ports.ResolutionAbandon, outcome)
	}

	// Verify session transitioned to FAILED
	gotS1, err = store.Get(ctx, s1.ID)
	if err != nil {
		t.Fatalf("failed to get s1: %v", err)
	}
	if gotS1.State != domain.Failed {
		t.Errorf("expected s1 state FAILED, got %s", gotS1.State)
	}

	// Verify audited session event was recorded
	var eventCount int
	err = store.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM session_events
		WHERE session_id = ? AND event_source = 'operator:operator-dias' AND state_to = 'FAILED'
	`, s1.ID).Scan(&eventCount)
	if err != nil {
		t.Fatalf("failed to query session events: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("expected 1 audited event from operator:operator-dias, got %d", eventCount)
	}

	// 3. Resolution with verified_settled allows resuming when reset time arrives
	s2, err := store.Create(ctx, ports.NewSession{
		Provider:  "codex",
		ContextID: "ctx-verified-settled",
		Mode:      domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/usr/bin/codex",
			ProviderVersion: "1.0.0",
			Cwd:             "/workspace",
			Profile:         "default",
		},
	})
	if err != nil {
		t.Fatalf("failed to create session 2: %v", err)
	}
	att2, _, err := store.ClaimStart(ctx, ports.ClaimRequest{
		SessionID:     s2.ID,
		OwnerEpoch:    epoch1,
		LeaseDuration: 5 * time.Minute,
		Now:           now,
	})
	if err != nil {
		t.Fatalf("failed to claim att2: %v", err)
	}

	// Reconcile marks it uncertain
	if err := store.Reconcile(ctx, "epoch-3", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	resetAt := now.Add(10 * time.Minute)
	err = store.ResolveUncertain(ctx, ports.ResolveUncertainRequest{
		SessionID:  s2.ID,
		AttemptID:  att2.ID,
		Resolution: ports.ResolutionVerifiedSettled,
		Evidence: domain.Evidence{
			Failure: domain.Failure{
				Category: domain.Quota,
				ResetAt:  &resetAt,
			},
		},
		Operator:    "operator-alice",
		Reason:      "provider quota reset verified",
		Destination: domain.WaitingForReset,
	})
	if err != nil {
		t.Fatalf("ResolveUncertain with verified_settled failed: %v", err)
	}

	gotS2, err := store.Get(ctx, s2.ID)
	if err != nil {
		t.Fatalf("failed to get s2: %v", err)
	}
	if gotS2.State != domain.WaitingForReset {
		t.Errorf("expected s2 state WAITING_FOR_RESET, got %s", gotS2.State)
	}
	if gotS2.NextRetryAt == nil || !gotS2.NextRetryAt.Equal(resetAt) {
		t.Errorf("expected s2 NextRetryAt %v, got %v", resetAt, gotS2.NextRetryAt)
	}

	// Before reset time: ClaimDue returns false
	_, canClaimBefore, err := store.ClaimDue(ctx, ports.ClaimRequest{
		SessionID:     s2.ID,
		OwnerEpoch:    "epoch-3",
		LeaseDuration: 5 * time.Minute,
		Now:           resetAt.Add(-time.Minute),
	})
	if err != nil || canClaimBefore {
		t.Errorf("ClaimDue before reset time should be false, got %v, err %v", canClaimBefore, err)
	}

	// After reset time: ClaimDue succeeds!
	_, canClaimAfter, err := store.ClaimDue(ctx, ports.ClaimRequest{
		SessionID:     s2.ID,
		OwnerEpoch:    "epoch-3",
		LeaseDuration: 5 * time.Minute,
		Now:           resetAt.Add(time.Second),
	})
	if err != nil || !canClaimAfter {
		t.Errorf("ClaimDue after reset time should succeed, got %v, err %v", canClaimAfter, err)
	}
}

func advanceToWaitingForReset(t *testing.T, store *persistence.Store, sess domain.Session, resetAt time.Time) domain.Session {
	t.Helper()
	ctx := context.Background()

	sess, err := store.Apply(ctx, domain.Transition{
		ExpectedRevision: sess.Revision,
		Destination:      domain.Running,
		EventID:          newTestID(),
		Source:           "test",
		Evidence: domain.Evidence{
			ExpectedSessionID: sess.ID,
		},
	})
	if err != nil {
		t.Fatalf("failed to transition to RUNNING: %v", err)
	}

	sess, err = store.Apply(ctx, domain.Transition{
		ExpectedRevision: sess.Revision,
		Destination:      domain.QuotaExhausted,
		EventID:          newTestID(),
		Source:           "test",
		Evidence: domain.Evidence{
			ExpectedSessionID: sess.ID,
			Failure: domain.Failure{
				Category: domain.Quota,
				ResetAt:  &resetAt,
			},
			ObservedAt: resetAt,
		},
	})
	if err != nil {
		t.Fatalf("failed to transition to QUOTA_EXHAUSTED: %v", err)
	}

	sess, err = store.Apply(ctx, domain.Transition{
		ExpectedRevision: sess.Revision,
		Destination:      domain.WaitingForReset,
		EventID:          newTestID(),
		Source:           "test",
		Evidence: domain.Evidence{
			ExpectedSessionID: sess.ID,
			Failure: domain.Failure{
				Category: domain.Quota,
				ResetAt:  &resetAt,
			},
			ObservedAt: resetAt,
		},
	})
	if err != nil {
		t.Fatalf("failed to transition to WAITING_FOR_RESET: %v", err)
	}

	return sess
}
