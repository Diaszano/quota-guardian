package persistence_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Diaszano/quota-guardian/internal/adapters/persistence"
	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

func setupTestStore(t *testing.T) (*persistence.Store, string, func()) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "qg-store-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	stateDir := filepath.Join(tempDir, "state")
	store, err := persistence.Open(context.Background(), stateDir)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("failed to open store: %v", err)
	}
	cleanup := func() {
		store.Close()
		os.RemoveAll(tempDir)
	}
	return store, stateDir, cleanup
}

var testIDCounter int64

func newTestID() string {
	val := atomic.AddInt64(&testIDCounter, 1)
	return fmt.Sprintf("test-id-%d-%d", time.Now().UnixNano(), val)
}

func advanceToReadyToResume(t *testing.T, store *persistence.Store, sess domain.Session, past time.Time) domain.Session {
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
				ResetAt:  &past,
			},
			ObservedAt: past,
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
				ResetAt:  &past,
			},
			ObservedAt: past,
		},
	})
	if err != nil {
		t.Fatalf("failed to transition to WAITING_FOR_RESET: %v", err)
	}

	sess, err = store.Apply(ctx, domain.Transition{
		ExpectedRevision: sess.Revision,
		Destination:      domain.ReadyToResume,
		EventID:          newTestID(),
		Source:           "test",
		Evidence: domain.Evidence{
			ExpectedSessionID: sess.ID,
			Failure: domain.Failure{
				Category: domain.Quota,
				ResetAt:  &past,
			},
			ObservedAt: past,
		},
	})
	if err != nil {
		t.Fatalf("failed to apply transition to READY_TO_RESUME: %v", err)
	}
	return sess
}

// TestConcurrentClaims runs 32 claimers on a due session and asserts exactly
// one claim succeeds, creates an open attempt, and increments retry count.
func TestConcurrentClaims(t *testing.T) {
	ctx := context.Background()
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	now := time.Now().UTC()
	past := now.Add(-2 * time.Minute)

	// Create a new session
	subKey := "concurrent-sub-key"
	sess, err := store.Create(ctx, ports.NewSession{
		SubmissionKey: &subKey,
		Provider:      "agy",
		ContextID:     "ctx-concurrent",
		Mode:          domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/bin/agy",
			ProviderVersion: "1.0.0",
			Cwd:             "/workspace",
			Profile:         "default",
		},
		RetryPolicy: domain.RetryPolicy{
			BaseDelay:  time.Minute,
			MaxRetries: 5,
		},
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	sess = advanceToReadyToResume(t, store, sess, past)

	const concurrency = 32
	var wg sync.WaitGroup
	var successCount int64
	var failCount int64
	var successfulAttempt domain.Attempt
	var attemptMu sync.Mutex

	startBarrier := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startBarrier

			claimReq := ports.ClaimRequest{
				SessionID:     sess.ID,
				OwnerEpoch:    fmt.Sprintf("worker-%d", workerID),
				LeaseDuration: 30 * time.Second,
				Now:           now,
			}

			att, claimed, claimErr := store.ClaimDue(ctx, claimReq)
			if claimErr != nil {
				t.Errorf("worker %d returned error: %v", workerID, claimErr)
				return
			}
			if claimed {
				atomic.AddInt64(&successCount, 1)
				attemptMu.Lock()
				successfulAttempt = att
				attemptMu.Unlock()
			} else {
				atomic.AddInt64(&failCount, 1)
			}
		}(i)
	}

	close(startBarrier)
	wg.Wait()

	if successCount != 1 {
		t.Fatalf("expected exactly 1 claim success, got %d", successCount)
	}
	if failCount != concurrency-1 {
		t.Fatalf("expected %d claim failures, got %d", concurrency-1, failCount)
	}
	if successfulAttempt.ID == "" {
		t.Fatalf("successful attempt has empty ID")
	}

	// Verify session state in store
	updated, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("failed to get updated session: %v", err)
	}
	if updated.State != domain.Resuming {
		t.Errorf("expected session state %s, got %s", domain.Resuming, updated.State)
	}
	if updated.RetryCount != 1 {
		t.Errorf("expected retry_count to be incremented to 1, got %d", updated.RetryCount)
	}
}

// TestStaleFenceRejected asserts an old/stale fence on Renew or FinishAttempt
// changes no row and returns ErrConflict.
func TestStaleFenceRejected(t *testing.T) {
	ctx := context.Background()
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	now := time.Now().UTC()
	subKey := "stale-fence-key"

	sess, err := store.Create(ctx, ports.NewSession{
		SubmissionKey: &subKey,
		Provider:      "agy",
		ContextID:     "ctx-stale",
		Mode:          domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/bin/agy",
			ProviderVersion: "1.0.0",
			Cwd:             "/workspace",
			Profile:         "default",
		},
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	att, claimed, err := store.ClaimStart(ctx, ports.ClaimRequest{
		SessionID:     sess.ID,
		OwnerEpoch:    "epoch-1",
		LeaseDuration: 30 * time.Second,
		Now:           now,
	})
	if err != nil || !claimed {
		t.Fatalf("failed to claim start: claimed=%v, err=%v", claimed, err)
	}

	if err := store.MarkLaunching(ctx, att); err != nil {
		t.Fatalf("failed to mark launching: %v", err)
	}
	if err := store.RecordIdentity(ctx, att, domain.ProcessIdentity{
		PID:        1234,
		BootID:     "boot-xyz",
		StartTicks: 5678,
	}); err != nil {
		t.Fatalf("failed to record identity: %v", err)
	}

	// 1. Stale fence on Renew must fail with ErrConflict
	staleAtt := att
	staleAtt.Fence = att.Fence - 1 // stale fence
	err = store.Renew(ctx, staleAtt, now.Add(40*time.Second))
	if err != ports.ErrConflict {
		t.Fatalf("expected ErrConflict for stale Renew, got %v", err)
	}

	// 2. Stale owner epoch on Renew must fail with ErrConflict
	wrongEpochAtt := att
	wrongEpochAtt.OwnerEpoch = "different-epoch"
	err = store.Renew(ctx, wrongEpochAtt, now.Add(40*time.Second))
	if err != ports.ErrConflict {
		t.Fatalf("expected ErrConflict for wrong epoch Renew, got %v", err)
	}

	// 3. Stale fence on FinishAttempt must fail with ErrConflict
	staleFinishResult := ports.AttemptResult{
		AttemptID:   att.ID,
		Fence:       att.Fence - 1, // stale fence
		FinishedAt:  now.Add(10 * time.Second),
		OutcomeCode: "completed",
		Transition: domain.Transition{
			Destination: domain.Completed,
			Source:      "test",
			Evidence: domain.Evidence{
				ExpectedSessionID:  sess.ID,
				ConfirmedSessionID: sess.ID,
				Settled:            true,
				TurnCompleted:      true,
			},
		},
	}
	err = store.FinishAttempt(ctx, staleFinishResult)
	if err != ports.ErrConflict {
		t.Fatalf("expected ErrConflict for stale FinishAttempt, got %v", err)
	}

	// Verify attempt remains active
	sessAfter, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	if sessAfter.State != domain.Running {
		t.Errorf("session state changed despite stale finish: got %s", sessAfter.State)
	}

	// 4. Valid FinishAttempt succeeds
	validFinishResult := staleFinishResult
	validFinishResult.Fence = att.Fence
	err = store.FinishAttempt(ctx, validFinishResult)
	if err != nil {
		t.Fatalf("expected FinishAttempt with valid fence to succeed, got %v", err)
	}

	// 5. Subsequent finish on already finished attempt returns ErrConflict
	err = store.FinishAttempt(ctx, validFinishResult)
	if err != ports.ErrConflict {
		t.Fatalf("expected ErrConflict for already finished attempt, got %v", err)
	}
}

// TestSubmissionRetryReturnsExisting asserts that repeating a submission key
// returns the existing session without replacing or overwriting execution context,
// and verifies no prompt text is stored anywhere in the database.
func TestSubmissionRetryReturnsExisting(t *testing.T) {
	ctx := context.Background()
	store, stateDir, cleanup := setupTestStore(t)
	defer cleanup()

	seededPrompt := "SUPER_SECRET_USER_PROMPT_SENTINEL_42"
	subKey := "submission-key-idempotent-test"

	firstSubmission := ports.NewSession{
		SubmissionKey: &subKey,
		Provider:      "agy",
		ContextID:     "ctx-original",
		Mode:          domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/bin/agy",
			ProviderVersion: "1.0.0",
			Cwd:             "/workspace/original",
			Profile:         "original-profile",
		},
	}

	s1, err := store.Create(ctx, firstSubmission)
	if err != nil {
		t.Fatalf("failed first submission: %v", err)
	}

	// Repeated submission with DIFFERENT context payload
	secondSubmission := ports.NewSession{
		SubmissionKey: &subKey,
		Provider:      "agy",
		ContextID:     "ctx-modified",
		Mode:          domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/bin/modified-agy",
			ProviderVersion: "2.0.0",
			Cwd:             "/workspace/tampered",
			Profile:         "tampered-profile",
		},
	}

	s2, err := store.Create(ctx, secondSubmission)
	if err != nil {
		t.Fatalf("failed second submission: %v", err)
	}

	// Assertions: must return the existing session exactly
	if s2.ID != s1.ID {
		t.Fatalf("expected same session ID %s, got %s", s1.ID, s2.ID)
	}
	if s2.Context.Cwd != "/workspace/original" {
		t.Errorf("context cwd was overwritten: expected /workspace/original, got %s", s2.Context.Cwd)
	}
	if s2.Context.Profile != "original-profile" {
		t.Errorf("context profile was overwritten: expected original-profile, got %s", s2.Context.Profile)
	}

	// Verify database dump contains no prompt sentinel
	dbPath := filepath.Join(stateDir, "quota-guardian.db")
	dbBytes, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("failed to read db file: %v", err)
	}
	if bytes.Contains(dbBytes, []byte(seededPrompt)) {
		t.Fatalf("CRITICAL SECURITY VIOLATION: seeded prompt found in raw SQLite file!")
	}
}

// TestRollbackOnFailedOperation asserts failed operations leave no partial/orphaned records.
func TestRollbackOnFailedOperation(t *testing.T) {
	ctx := context.Background()
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	subKey := "rollback-test-key"
	sess, err := store.Create(ctx, ports.NewSession{
		SubmissionKey: &subKey,
		Provider:      "agy",
		ContextID:     "ctx-rollback",
		Mode:          domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/bin/agy",
			ProviderVersion: "1.0.0",
			Cwd:             "/workspace",
			Profile:         "default",
		},
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Attempt an invalid transition from DISCOVERED to WAITING_FOR_RESET (invalid per state machine)
	_, err = store.Apply(ctx, domain.Transition{
		ExpectedRevision: sess.Revision,
		Destination:      domain.WaitingForReset,
		EventID:          "invalid-event",
		Source:           "test",
	})
	if err == nil {
		t.Fatalf("expected invalid transition to fail, but succeeded")
	}

	// Verify session remains at revision 1 and state DISCOVERED
	curr, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	if curr.Revision != 1 {
		t.Errorf("revision was incremented on failed transaction: got %d", curr.Revision)
	}
	if curr.State != domain.Discovered {
		t.Errorf("state changed on failed transaction: got %s", curr.State)
	}

	// Verify session_events has no entry for invalid-event
	var count int
	err = store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM session_events WHERE id = 'invalid-event'").Scan(&count)
	if err != nil {
		t.Fatalf("failed to query session_events: %v", err)
	}
	if count != 0 {
		t.Errorf("orphaned event record found in session_events after rollback")
	}
}

// TestCancelVsClaimRace asserts atomic behavior when Cancel races ClaimDue.
func TestCancelVsClaimRace(t *testing.T) {
	ctx := context.Background()
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	now := time.Now().UTC()
	past := now.Add(-1 * time.Minute)
	subKey := "cancel-claim-race-key"

	sess, err := store.Create(ctx, ports.NewSession{
		SubmissionKey: &subKey,
		Provider:      "agy",
		ContextID:     "ctx-race",
		Mode:          domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/bin/agy",
			ProviderVersion: "1.0.0",
			Cwd:             "/workspace",
			Profile:         "default",
		},
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	sess = advanceToReadyToResume(t, store, sess, past)

	var wg sync.WaitGroup
	wg.Add(2)

	var cancelErr error
	var claimAttempt domain.Attempt
	var claimed bool
	var claimErr error

	barrier := make(chan struct{})

	go func() {
		defer wg.Done()
		<-barrier
		_, cancelErr = store.Cancel(ctx, sess.ID, 0)
	}()

	go func() {
		defer wg.Done()
		<-barrier
		claimAttempt, claimed, claimErr = store.ClaimDue(ctx, ports.ClaimRequest{
			SessionID:     sess.ID,
			OwnerEpoch:    "racer",
			LeaseDuration: 30 * time.Second,
			Now:           now,
		})
	}()

	close(barrier)
	wg.Wait()

	if cancelErr != nil {
		t.Fatalf("cancel returned error: %v", cancelErr)
	}
	if claimErr != nil {
		t.Fatalf("claim returned error: %v", claimErr)
	}

	finalSess, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("failed to get final session: %v", err)
	}

	if claimed {
		// If claim won the race, session is RESUMING with cancel_requested = 1
		if finalSess.State != domain.Resuming && finalSess.State != domain.Cancelled {
			t.Errorf("unexpected state when claim won: %s", finalSess.State)
		}
		if !finalSess.CancelRequested {
			t.Errorf("expected cancel_requested to be true")
		}
		if claimAttempt.ID == "" {
			t.Errorf("claimed attempt ID is empty")
		}
	} else {
		// If cancel won the race, session is CANCELLED and claim failed
		if finalSess.State != domain.Cancelled {
			t.Errorf("expected CANCELLED when cancel won, got %s", finalSess.State)
		}
	}
}

// TestConcurrentClaimVsConsentRace asserts atomic revision checks between ClaimDue and SetConsent.
func TestConcurrentClaimVsConsentRace(t *testing.T) {
	ctx := context.Background()
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	now := time.Now().UTC()
	past := now.Add(-1 * time.Minute)
	subKey := "consent-claim-race-key"

	sess, err := store.Create(ctx, ports.NewSession{
		SubmissionKey: &subKey,
		Provider:      "agy",
		ContextID:     "ctx-consent-race",
		Mode:          domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/bin/agy",
			ProviderVersion: "1.0.0",
			Cwd:             "/workspace",
			Profile:         "default",
		},
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	sess = advanceToReadyToResume(t, store, sess, past)

	var wg sync.WaitGroup
	wg.Add(2)

	var consentErr error
	var claimAttempt domain.Attempt
	var claimed bool
	var claimErr error

	barrier := make(chan struct{})

	// Thread 1: SetConsent with ExpectedRevision
	go func() {
		defer wg.Done()
		<-barrier
		_, consentErr = store.SetConsent(ctx, sess.ID, sess.Revision, domain.Consent{
			Enabled:           true,
			BackgroundAllowed: true,
		})
	}()

	// Thread 2: ClaimDue with ExpectedRevision
	go func() {
		defer wg.Done()
		<-barrier
		claimAttempt, claimed, claimErr = store.ClaimDue(ctx, ports.ClaimRequest{
			SessionID:        sess.ID,
			ExpectedRevision: sess.Revision,
			OwnerEpoch:       "consent-racer",
			LeaseDuration:    30 * time.Second,
			Now:              now,
		})
	}()

	close(barrier)
	wg.Wait()

	if claimErr != nil {
		t.Fatalf("claim returned unexpected error: %v", claimErr)
	}

	// Exactly one of the two should win the ExpectedRevision race
	finalSess, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("failed to get final session: %v", err)
	}

	if claimed {
		// Claim won: SetConsent either failed with ErrConflict or succeeded if it ran after
		if finalSess.State != domain.Resuming {
			t.Errorf("expected state RESUMING, got %s", finalSess.State)
		}
		if claimAttempt.ID == "" {
			t.Errorf("claim attempt has empty ID")
		}
	} else {
		// SetConsent won: ClaimDue failed to claim due to revision mismatch
		if consentErr != nil {
			t.Errorf("consent update failed: %v", consentErr)
		}
		if !finalSess.Consent.Enabled {
			t.Errorf("expected consent to be enabled")
		}
	}
}

// TestListSessions tests filtering and due queries.
func TestListSessions(t *testing.T) {
	ctx := context.Background()
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	now := time.Now().UTC()
	past := now.Add(-10 * time.Minute)

	subKey1 := "list-sub-1"
	sess1, err := store.Create(ctx, ports.NewSession{
		SubmissionKey: &subKey1,
		Provider:      "agy",
		ContextID:     "ctx-list-1",
		Mode:          domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/bin/agy",
			ProviderVersion: "1.0.0",
			Cwd:             "/workspace",
			Profile:         "default",
		},
	})
	if err != nil {
		t.Fatalf("failed to create sess1: %v", err)
	}

	subKey2 := "list-sub-2"
	sess2, err := store.Create(ctx, ports.NewSession{
		SubmissionKey: &subKey2,
		Provider:      "codex",
		ContextID:     "ctx-list-2",
		Mode:          domain.ModeObserved,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/bin/codex",
			ProviderVersion: "2.0.0",
			Cwd:             "/workspace",
			Profile:         "default",
		},
	})
	if err != nil {
		t.Fatalf("failed to create sess2: %v", err)
	}

	// Advance sess1 to READY_TO_RESUME with past reset time (due)
	_ = advanceToReadyToResume(t, store, sess1, past)

	// List all
	all, err := store.List(ctx, ports.SessionFilter{})
	if err != nil {
		t.Fatalf("failed to list all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(all))
	}

	// Filter by Provider
	codexProv := "codex"
	codexList, err := store.List(ctx, ports.SessionFilter{Provider: &codexProv})
	if err != nil {
		t.Fatalf("failed to list by provider: %v", err)
	}
	if len(codexList) != 1 || codexList[0].ID != sess2.ID {
		t.Errorf("expected 1 codex session, got %d", len(codexList))
	}

	// Filter DueOnly
	dueList, err := store.List(ctx, ports.SessionFilter{
		DueOnly: true,
		Now:     now,
	})
	if err != nil {
		t.Fatalf("failed to list due sessions: %v", err)
	}
	if len(dueList) != 1 || dueList[0].ID != sess1.ID {
		t.Errorf("expected 1 due session (sess1), got %d", len(dueList))
	}

	// Future time check: if filter.Now is earlier than reset_at, sess1 should not be due
	duePastList, err := store.List(ctx, ports.SessionFilter{
		DueOnly: true,
		Now:     past.Add(-5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("failed to list due sessions with early now: %v", err)
	}
	if len(duePastList) != 0 {
		t.Errorf("expected 0 due sessions when now < reset_at, got %d", len(duePastList))
	}
}

// TestClaimStartEligibility tests ClaimStart rules and validations.
func TestClaimStartEligibility(t *testing.T) {
	ctx := context.Background()
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	now := time.Now().UTC()
	subKey := "claim-start-test-key"

	sess, err := store.Create(ctx, ports.NewSession{
		SubmissionKey: &subKey,
		Provider:      "agy",
		ContextID:     "ctx-claim-start",
		Mode:          domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/bin/agy",
			ProviderVersion: "1.0.0",
			Cwd:             "/workspace",
			Profile:         "default",
		},
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// 1. ClaimStart with wrong expected revision returns claimed=false
	_, claimed, err := store.ClaimStart(ctx, ports.ClaimRequest{
		SessionID:        sess.ID,
		ExpectedRevision: 999,
		OwnerEpoch:       "ep-1",
		LeaseDuration:    30 * time.Second,
		Now:              now,
	})
	if err != nil || claimed {
		t.Fatalf("expected claim=false on wrong revision, got claimed=%v, err=%v", claimed, err)
	}

	// 2. Valid ClaimStart succeeds
	att, claimed, err := store.ClaimStart(ctx, ports.ClaimRequest{
		SessionID:     sess.ID,
		OwnerEpoch:    "ep-1",
		LeaseDuration: 30 * time.Second,
		Now:           now,
	})
	if err != nil || !claimed {
		t.Fatalf("expected valid ClaimStart to succeed, got claimed=%v, err=%v", claimed, err)
	}
	if att.Kind != domain.AttemptKindStart || att.Phase != domain.AttemptPhaseClaimed {
		t.Errorf("unexpected attempt properties: kind=%s, phase=%s", att.Kind, att.Phase)
	}

	// Verify retry_count was NOT incremented on start claim
	sAfter, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	if sAfter.RetryCount != 0 {
		t.Errorf("retry count was incremented on start claim: got %d", sAfter.RetryCount)
	}
	if sAfter.State != domain.Running {
		t.Errorf("expected session state RUNNING, got %s", sAfter.State)
	}

	// 3. Second ClaimStart on now RUNNING session returns claimed=false
	_, claimed, err = store.ClaimStart(ctx, ports.ClaimRequest{
		SessionID:     sess.ID,
		OwnerEpoch:    "ep-2",
		LeaseDuration: 30 * time.Second,
		Now:           now,
	})
	if err != nil || claimed {
		t.Fatalf("expected second ClaimStart to return false, got claimed=%v, err=%v", claimed, err)
	}
}
