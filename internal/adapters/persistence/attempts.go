package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

// ClaimStart claims an initial start attempt for a DISCOVERED session.
// Sets state to RUNNING, creates attempt row with phase 'claimed', and fences attempt.
// Retries count is NOT incremented at start.
func (s *Store) ClaimStart(ctx context.Context, req ports.ClaimRequest) (domain.Attempt, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Attempt{}, false, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, sessionSelectQuery+" WHERE s.id = ?", req.SessionID)
	sess, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Attempt{}, false, ports.ErrNotFound
		}
		return domain.Attempt{}, false, err
	}

	// Eligibility checks
	if sess.State != domain.Discovered || sess.CancelRequested {
		return domain.Attempt{}, false, nil
	}
	if req.ExpectedRevision > 0 && sess.Revision != req.ExpectedRevision {
		return domain.Attempt{}, false, nil
	}

	// Check if there is already an open attempt
	var openCount int
	err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM attempts
		WHERE session_id = ? AND phase IN ('claimed', 'launching', 'active', 'uncertain')
	`, req.SessionID).Scan(&openCount)
	if err != nil {
		return domain.Attempt{}, false, err
	}
	if openCount > 0 {
		return domain.Attempt{}, false, nil
	}

	nowMs := req.Now.UnixMilli()
	if req.Now.IsZero() {
		nowMs = time.Now().UTC().UnixMilli()
	}

	newFence := sess.Fence + 1
	newRevision := sess.Revision + 1

	// Update session: DISCOVERED -> RUNNING
	res, err := tx.ExecContext(ctx, `
		UPDATE sessions SET
			state = 'RUNNING',
			fence = ?,
			revision = ?,
			updated_at = ?
		WHERE id = ? AND revision = ?
	`, newFence, newRevision, nowMs, sess.ID, sess.Revision)
	if err != nil {
		return domain.Attempt{}, false, err
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return domain.Attempt{}, false, nil
	}

	var attemptNumber int
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(attempt_number), 0) + 1 FROM attempts WHERE session_id = ?
	`, sess.ID).Scan(&attemptNumber)
	if err != nil {
		return domain.Attempt{}, false, err
	}

	attemptID := newID()
	leaseDeadlineMs := req.Now.Add(req.LeaseDuration).UnixMilli()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO attempts (
			id, session_id, attempt_number, kind, phase, owner_epoch,
			fence, lease_deadline, pid, boot_id, start_ticks, started_at, finished_at, outcome_code
		) VALUES (
			?, ?, ?, 'start', 'claimed', ?,
			?, ?, NULL, NULL, NULL, ?, NULL, NULL
		)
	`,
		attemptID, sess.ID, attemptNumber, req.OwnerEpoch,
		newFence, leaseDeadlineMs, nowMs,
	)
	if err != nil {
		return domain.Attempt{}, false, nil
	}

	// Record session event
	_, err = tx.ExecContext(ctx, `
		INSERT INTO session_events (
			id, session_id, sequence, state_from, state_to, event_source, attempt_id, failure_category, observed_at
		) VALUES (
			?, ?,
			(SELECT COALESCE(MAX(sequence), 0) + 1 FROM session_events WHERE session_id = ?),
			'DISCOVERED', 'RUNNING', 'coordinator', ?, NULL, ?
		)
	`, newID(), sess.ID, sess.ID, attemptID, nowMs)
	if err != nil {
		return domain.Attempt{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return domain.Attempt{}, false, nil
	}

	att := domain.Attempt{
		ID:            attemptID,
		SessionID:     sess.ID,
		AttemptNumber: attemptNumber,
		Kind:          domain.AttemptKindStart,
		Phase:         domain.AttemptPhaseClaimed,
		OwnerEpoch:    req.OwnerEpoch,
		Fence:         newFence,
		LeaseDeadline: time.UnixMilli(leaseDeadlineMs).UTC(),
		StartedAt:     time.UnixMilli(nowMs).UTC(),
	}

	return att, true, nil
}

// ClaimDue atomically claims a session that is due (READY_TO_RESUME or WAITING_FOR_RESET where next_retry_at <= now).
// Sets state to RESUMING, creates attempt row, and increments retry_count at resume claim.
func (s *Store) ClaimDue(ctx context.Context, req ports.ClaimRequest) (domain.Attempt, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Attempt{}, false, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, sessionSelectQuery+" WHERE s.id = ?", req.SessionID)
	sess, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Attempt{}, false, ports.ErrNotFound
		}
		return domain.Attempt{}, false, err
	}

	nowMs := req.Now.UnixMilli()
	if req.Now.IsZero() {
		nowMs = time.Now().UTC().UnixMilli()
	}

	// Eligibility checks
	if sess.CancelRequested {
		return domain.Attempt{}, false, nil
	}
	if sess.State != domain.ReadyToResume && sess.State != domain.WaitingForReset {
		return domain.Attempt{}, false, nil
	}
	if sess.NextRetryAt != nil && sess.NextRetryAt.UnixMilli() > nowMs {
		return domain.Attempt{}, false, nil
	}
	if req.ExpectedRevision > 0 && sess.Revision != req.ExpectedRevision {
		return domain.Attempt{}, false, nil
	}

	// Check if an attempt is already open
	var openCount int
	err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM attempts
		WHERE session_id = ? AND phase IN ('claimed', 'launching', 'active', 'uncertain')
	`, req.SessionID).Scan(&openCount)
	if err != nil {
		return domain.Attempt{}, false, err
	}
	if openCount > 0 {
		return domain.Attempt{}, false, nil
	}

	newFence := sess.Fence + 1
	newRevision := sess.Revision + 1
	newRetryCount := sess.RetryCount + 1

	// Update session: transitions to RESUMING and increments retry_count
	res, err := tx.ExecContext(ctx, `
		UPDATE sessions SET
			state = 'RESUMING',
			fence = ?,
			revision = ?,
			retry_count = ?,
			updated_at = ?
		WHERE id = ? AND revision = ?
	`, newFence, newRevision, newRetryCount, nowMs, sess.ID, sess.Revision)
	if err != nil {
		return domain.Attempt{}, false, err
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return domain.Attempt{}, false, nil
	}

	var attemptNumber int
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(attempt_number), 0) + 1 FROM attempts WHERE session_id = ?
	`, sess.ID).Scan(&attemptNumber)
	if err != nil {
		return domain.Attempt{}, false, err
	}

	attemptID := newID()
	leaseDeadlineMs := req.Now.Add(req.LeaseDuration).UnixMilli()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO attempts (
			id, session_id, attempt_number, kind, phase, owner_epoch,
			fence, lease_deadline, pid, boot_id, start_ticks, started_at, finished_at, outcome_code
		) VALUES (
			?, ?, ?, 'resume', 'claimed', ?,
			?, ?, NULL, NULL, NULL, ?, NULL, NULL
		)
	`,
		attemptID, sess.ID, attemptNumber, req.OwnerEpoch,
		newFence, leaseDeadlineMs, nowMs,
	)
	if err != nil {
		return domain.Attempt{}, false, nil
	}

	// Record session event
	_, err = tx.ExecContext(ctx, `
		INSERT INTO session_events (
			id, session_id, sequence, state_from, state_to, event_source, attempt_id, failure_category, observed_at
		) VALUES (
			?, ?,
			(SELECT COALESCE(MAX(sequence), 0) + 1 FROM session_events WHERE session_id = ?),
			?, 'RESUMING', 'coordinator', ?, NULL, ?
		)
	`, newID(), sess.ID, sess.ID, string(sess.State), attemptID, nowMs)
	if err != nil {
		return domain.Attempt{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return domain.Attempt{}, false, nil
	}

	att := domain.Attempt{
		ID:            attemptID,
		SessionID:     sess.ID,
		AttemptNumber: attemptNumber,
		Kind:          domain.AttemptKindResume,
		Phase:         domain.AttemptPhaseClaimed,
		OwnerEpoch:    req.OwnerEpoch,
		Fence:         newFence,
		LeaseDeadline: time.UnixMilli(leaseDeadlineMs).UTC(),
		StartedAt:     time.UnixMilli(nowMs).UTC(),
	}

	return att, true, nil
}

// MarkLaunching transitions attempt phase to 'launching', validating fence and owner epoch.
func (s *Store) MarkLaunching(ctx context.Context, attempt domain.Attempt) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var (
		currentFence int64
		ownerEpoch   string
		phase        string
	)
	err = tx.QueryRowContext(ctx, "SELECT fence, owner_epoch, phase FROM attempts WHERE id = ?", attempt.ID).
		Scan(&currentFence, &ownerEpoch, &phase)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.ErrNotFound
		}
		return err
	}

	if currentFence != attempt.Fence || ownerEpoch != attempt.OwnerEpoch || phase != string(domain.AttemptPhaseClaimed) {
		return ports.ErrConflict
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE attempts SET phase = 'launching'
		WHERE id = ? AND fence = ? AND owner_epoch = ? AND phase = 'claimed'
	`, attempt.ID, attempt.Fence, attempt.OwnerEpoch)
	if err != nil {
		return err
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return ports.ErrConflict
	}

	return tx.Commit()
}

// RecordIdentity records PID, boot ID, start ticks, and transitions phase to 'active'.
func (s *Store) RecordIdentity(ctx context.Context, attempt domain.Attempt, identity domain.ProcessIdentity) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var (
		currentFence int64
		ownerEpoch   string
		phase        string
	)
	err = tx.QueryRowContext(ctx, "SELECT fence, owner_epoch, phase FROM attempts WHERE id = ?", attempt.ID).
		Scan(&currentFence, &ownerEpoch, &phase)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.ErrNotFound
		}
		return err
	}

	if currentFence != attempt.Fence || ownerEpoch != attempt.OwnerEpoch {
		return ports.ErrConflict
	}
	if phase != string(domain.AttemptPhaseLaunching) && phase != string(domain.AttemptPhaseClaimed) {
		return ports.ErrConflict
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE attempts SET
			phase = 'active',
			pid = ?,
			boot_id = ?,
			start_ticks = ?
		WHERE id = ? AND fence = ? AND owner_epoch = ? AND phase IN ('launching', 'claimed')
	`, identity.PID, identity.BootID, identity.StartTicks, attempt.ID, attempt.Fence, attempt.OwnerEpoch)
	if err != nil {
		return err
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return ports.ErrConflict
	}

	return tx.Commit()
}

// Renew renews the lease deadline until given time. Validates attempt fence and owner epoch.
func (s *Store) Renew(ctx context.Context, attempt domain.Attempt, until time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var (
		currentFence int64
		ownerEpoch   string
		phase        string
	)
	err = tx.QueryRowContext(ctx, "SELECT fence, owner_epoch, phase FROM attempts WHERE id = ?", attempt.ID).
		Scan(&currentFence, &ownerEpoch, &phase)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.ErrNotFound
		}
		return err
	}

	if currentFence != attempt.Fence || ownerEpoch != attempt.OwnerEpoch {
		return ports.ErrConflict
	}
	if phase != string(domain.AttemptPhaseClaimed) && phase != string(domain.AttemptPhaseLaunching) && phase != string(domain.AttemptPhaseActive) {
		return ports.ErrConflict
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE attempts SET lease_deadline = ?
		WHERE id = ? AND fence = ? AND owner_epoch = ? AND phase IN ('claimed', 'launching', 'active')
	`, until.UnixMilli(), attempt.ID, attempt.Fence, attempt.OwnerEpoch)
	if err != nil {
		return err
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return ports.ErrConflict
	}

	return tx.Commit()
}

// FinishAttempt transitions attempt phase to 'finished', validates fence, updates session state based
// on the transition, and records a session event. Old/stale fence returns ErrConflict.
func (s *Store) FinishAttempt(ctx context.Context, result ports.AttemptResult) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var (
		sessionID    string
		currentFence int64
		phase        string
	)
	err = tx.QueryRowContext(ctx, "SELECT session_id, fence, phase FROM attempts WHERE id = ?", result.AttemptID).
		Scan(&sessionID, &currentFence, &phase)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.ErrNotFound
		}
		return err
	}

	if currentFence != result.Fence {
		return ports.ErrConflict
	}
	if phase != string(domain.AttemptPhaseClaimed) && phase != string(domain.AttemptPhaseLaunching) && phase != string(domain.AttemptPhaseActive) {
		return ports.ErrConflict
	}

	// Fetch target session
	row := tx.QueryRowContext(ctx, sessionSelectQuery+" WHERE s.id = ?", sessionID)
	sess, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.ErrNotFound
		}
		return err
	}

	if sess.Fence != result.Fence {
		return ports.ErrConflict
	}

	// If a session transition is specified
	destState := result.Transition.Destination
	if destState != "" {
		if result.Transition.ExpectedRevision > 0 && sess.Revision != result.Transition.ExpectedRevision {
			return ports.ErrConflict
		}
		if result.Transition.ExpectedState != "" && sess.State != result.Transition.ExpectedState {
			return ports.ErrConflict
		}
		if result.Transition.Fence > 0 && sess.Fence != result.Transition.Fence {
			return ports.ErrConflict
		}
		if err := domain.ValidateTransition(sess.State, destState); err != nil {
			return err
		}
	}

	finishedAtMs := result.FinishedAt.UnixMilli()
	if result.FinishedAt.IsZero() {
		finishedAtMs = time.Now().UTC().UnixMilli()
	}

	// Mark attempt finished
	res, err := tx.ExecContext(ctx, `
		UPDATE attempts SET
			phase = 'finished',
			finished_at = ?,
			outcome_code = ?
		WHERE id = ? AND fence = ?
	`, finishedAtMs, result.OutcomeCode, result.AttemptID, result.Fence)
	if err != nil {
		return err
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return ports.ErrConflict
	}

	// If session transition requested
	if destState != "" {
		turnOutcome := string(sess.TurnOutcome)
		var nextRetryAtMs *int64
		if sess.NextRetryAt != nil {
			ms := sess.NextRetryAt.UnixMilli()
			nextRetryAtMs = &ms
		}
		if result.Transition.Evidence.Failure.ResetAt != nil {
			ms := result.Transition.Evidence.Failure.ResetAt.UnixMilli()
			nextRetryAtMs = &ms
		}

		if result.Transition.Evidence.TurnCompleted {
			turnOutcome = string(domain.TurnOutcomeCompleted)
		} else if result.Transition.Evidence.PermissionBlocked {
			turnOutcome = string(domain.TurnOutcomePermissionBlocked)
		} else if result.Transition.Evidence.Failure.Category == domain.Quota {
			turnOutcome = string(domain.TurnOutcomeQuota)
		} else if result.Transition.Evidence.Failure.Category != "" {
			turnOutcome = string(domain.TurnOutcomeFailed)
		}

		var lastErrCat, lastErrCode sql.NullString
		var lastErrResetMs sql.NullInt64
		if result.Transition.Evidence.Failure.Category != "" {
			lastErrCat = sql.NullString{String: string(result.Transition.Evidence.Failure.Category), Valid: true}
		}
		if result.Transition.Evidence.Failure.Code != "" {
			lastErrCode = sql.NullString{String: result.Transition.Evidence.Failure.Code, Valid: true}
		}
		if result.Transition.Evidence.Failure.ResetAt != nil {
			lastErrResetMs = sql.NullInt64{Int64: result.Transition.Evidence.Failure.ResetAt.UnixMilli(), Valid: true}
		}

		res, err := tx.ExecContext(ctx, `
			UPDATE sessions SET
				state = ?,
				revision = revision + 1,
				turn_outcome = ?,
				next_retry_at = ?,
				last_error_category = COALESCE(?, last_error_category),
				last_error_code = COALESCE(?, last_error_code),
				last_error_reset_at = COALESCE(?, last_error_reset_at),
				updated_at = ?
			WHERE id = ? AND revision = ?
		`,
			string(destState), turnOutcome, nextRetryAtMs,
			lastErrCat, lastErrCode, lastErrResetMs, finishedAtMs,
			sess.ID, sess.Revision,
		)
		if err != nil {
			return err
		}
		rowsAffected, _ := res.RowsAffected()
		if rowsAffected == 0 {
			return ports.ErrConflict
		}

		eventID := result.Transition.EventID
		if eventID == "" {
			eventID = newID()
		}
		source := result.Transition.Source
		if source == "" {
			source = "coordinator"
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO session_events (
				id, session_id, sequence, state_from, state_to, event_source, attempt_id, failure_category, observed_at
			) VALUES (
				?, ?,
				(SELECT COALESCE(MAX(sequence), 0) + 1 FROM session_events WHERE session_id = ?),
				?, ?, ?, ?, ?, ?
			)
		`,
			eventID, sess.ID, sess.ID,
			string(sess.State), string(destState), source,
			result.AttemptID, lastErrCat, finishedAtMs,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// Reconcile is a lifecycle hook to reconcile crashed or interrupted attempts.
// Detailed crash recovery is implemented in Task 5.
func (s *Store) Reconcile(ctx context.Context, epoch string, now time.Time) error {
	return nil
}
