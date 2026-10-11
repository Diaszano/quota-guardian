package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

type ProcessChecker func(pid int, bootID string, startTicks uint64) bool

// SetProcessChecker allows overriding process liveness check for testing or platform customization.
func (s *Store) SetProcessChecker(checker ProcessChecker) {
	s.processChecker = checker
}

func (s *Store) isProcessAlive(pid int, bootID string, startTicks uint64) bool {
	if s.processChecker != nil {
		return s.processChecker(pid, bootID, startTicks)
	}
	return defaultProcessChecker(pid, bootID, startTicks)
}

func defaultProcessChecker(pid int, bootID string, startTicks uint64) bool {
	if pid <= 0 {
		return false
	}
	if bootID != "" {
		if currentBootID, err := readCurrentBootID(); err == nil && currentBootID != "" {
			if currentBootID != bootID {
				return false
			}
		}
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.ESRCH) {
		return false
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}

func readCurrentBootID() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// Reconcile inspects attempts from old epochs or with expired leases, marking them uncertain
// and transitioning sessions to NEEDS_ATTENTION without allowing retry replay.
func (s *Store) Reconcile(ctx context.Context, currentEpoch string, now time.Time) error {
	if currentEpoch == "" {
		return errors.New("currentEpoch cannot be empty")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	nowMs := now.UnixMilli()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin reconcile transaction: %w", err)
	}
	defer tx.Rollback()

	// Select all attempts in claimed, launching, active where owner_epoch != currentEpoch OR lease_deadline < nowMs
	rows, err := tx.QueryContext(ctx, `
		SELECT id, session_id
		FROM attempts
		WHERE phase IN ('claimed', 'launching', 'active')
		  AND (owner_epoch != ? OR lease_deadline < ?)
	`, currentEpoch, nowMs)
	if err != nil {
		return fmt.Errorf("failed to query unfinished attempts: %w", err)
	}
	defer rows.Close()

	type interruptedAttempt struct {
		id        string
		sessionID string
	}
	var interrupted []interruptedAttempt
	for rows.Next() {
		var item interruptedAttempt
		if err := rows.Scan(&item.id, &item.sessionID); err != nil {
			return fmt.Errorf("failed to scan attempt: %w", err)
		}
		interrupted = append(interrupted, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error reading attempts: %w", err)
	}
	rows.Close()

	for _, att := range interrupted {
		// 1. Mark attempt as uncertain
		res, err := tx.ExecContext(ctx, `
			UPDATE attempts SET phase = 'uncertain'
			WHERE id = ? AND phase IN ('claimed', 'launching', 'active')
		`, att.id)
		if err != nil {
			return fmt.Errorf("failed to mark attempt %s uncertain: %w", att.id, err)
		}
		rowsAffected, _ := res.RowsAffected()
		if rowsAffected == 0 {
			continue
		}

		// 2. Fetch session
		var (
			sessState string
			revision  int64
		)
		err = tx.QueryRowContext(ctx, `
			SELECT state, revision FROM sessions WHERE id = ?
		`, att.sessionID).Scan(&sessState, &revision)
		if err != nil {
			return fmt.Errorf("failed to fetch session %s: %w", att.sessionID, err)
		}

		// Do not mutate terminal sessions
		if domain.IsTerminal(domain.State(sessState)) {
			continue
		}

		if sessState != string(domain.NeedsAttention) {
			_, err = tx.ExecContext(ctx, `
				UPDATE sessions SET
					state = 'NEEDS_ATTENTION',
					revision = revision + 1,
					turn_outcome = 'unknown',
					last_error_category = 'unknown',
					last_error_code = 'crash_recovery_uncertain',
					updated_at = ?
				WHERE id = ? AND revision = ?
			`, nowMs, att.sessionID, revision)
			if err != nil {
				return fmt.Errorf("failed to transition session %s to NEEDS_ATTENTION: %w", att.sessionID, err)
			}

			// Record session event
			_, err = tx.ExecContext(ctx, `
				INSERT INTO session_events (
					id, session_id, sequence, state_from, state_to, event_source, attempt_id, failure_category, observed_at
				) VALUES (
					?, ?,
					(SELECT COALESCE(MAX(sequence), 0) + 1 FROM session_events WHERE session_id = ?),
					?, 'NEEDS_ATTENTION', 'reconciler', ?, 'unknown', ?
				)
			`, newID(), att.sessionID, att.sessionID, sessState, att.id, nowMs)
			if err != nil {
				return fmt.Errorf("failed to record reconcile session event for %s: %w", att.sessionID, err)
			}
		} else {
			// If already in NEEDS_ATTENTION, ensure error info is set
			_, err = tx.ExecContext(ctx, `
				UPDATE sessions SET
					last_error_category = COALESCE(last_error_category, 'unknown'),
					last_error_code = COALESCE(last_error_code, 'crash_recovery_uncertain'),
					updated_at = ?
				WHERE id = ?
			`, nowMs, att.sessionID)
			if err != nil {
				return fmt.Errorf("failed to update session error info for %s: %w", att.sessionID, err)
			}
		}
	}

	return tx.Commit()
}

// ResolveUncertain explicitly reconciles an uncertain attempt with verifiable operator evidence.
func (s *Store) ResolveUncertain(ctx context.Context, req ports.ResolveUncertainRequest) error {
	if req.SessionID == "" || req.AttemptID == "" {
		return errors.New("session ID and attempt ID must not be empty")
	}
	if req.Operator == "" {
		return errors.New("operator must not be empty")
	}
	if req.Reason == "" {
		return errors.New("reason must not be empty")
	}
	switch req.Resolution {
	case ports.ResolutionAbandon, ports.ResolutionMarkFailed, ports.ResolutionVerifiedSettled:
	default:
		return fmt.Errorf("invalid resolution: %s", req.Resolution)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Fetch attempt and process details
	var (
		sessionID  string
		phase      string
		pid        sql.NullInt64
		bootID     sql.NullString
		startTicks sql.NullInt64
	)
	err = tx.QueryRowContext(ctx, `
		SELECT session_id, phase, pid, boot_id, start_ticks
		FROM attempts WHERE id = ?
	`, req.AttemptID).Scan(&sessionID, &phase, &pid, &bootID, &startTicks)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.ErrNotFound
		}
		return err
	}

	if sessionID != req.SessionID {
		return ports.ErrConflict
	}
	if phase != string(domain.AttemptPhaseUncertain) {
		return ports.ErrConflict
	}

	// 2. Process liveness verification:
	// "ensuring manual acknowledgement alone cannot override evidence of a still-running process"
	// "Never allow another execution while the previous provider invocation may still be active."
	if pid.Valid {
		procPID := int(pid.Int64)
		var bID string
		if bootID.Valid {
			bID = bootID.String
		}
		var ticks uint64
		if startTicks.Valid {
			ticks = uint64(startTicks.Int64)
		}
		if s.isProcessAlive(procPID, bID, ticks) {
			return fmt.Errorf("cannot resolve uncertain attempt %s: process %d is still running: %w", req.AttemptID, procPID, ports.ErrConflict)
		}
	}

	// 3. Fetch session
	var (
		sessState string
		revision  int64
	)
	err = tx.QueryRowContext(ctx, "SELECT state, revision FROM sessions WHERE id = ?", req.SessionID).
		Scan(&sessState, &revision)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.ErrNotFound
		}
		return err
	}

	if sessState != string(domain.NeedsAttention) {
		return ports.ErrConflict
	}

	// 4. Determine destination state
	destState := req.Destination
	if destState == "" {
		switch req.Resolution {
		case ports.ResolutionAbandon, ports.ResolutionMarkFailed:
			destState = domain.Failed
		case ports.ResolutionVerifiedSettled:
			if req.Evidence.Failure.ResetAt != nil && req.Evidence.Failure.ResetAt.After(time.Now().UTC()) {
				destState = domain.WaitingForReset
			} else {
				destState = domain.ReadyToResume
			}
		}
	}

	if err := domain.ValidateTransition(domain.NeedsAttention, destState); err != nil {
		return err
	}

	nowMs := time.Now().UTC().UnixMilli()

	// 5. Update attempt: phase -> finished, outcome_code -> resolution, finished_at -> nowMs
	res, err := tx.ExecContext(ctx, `
		UPDATE attempts SET
			phase = 'finished',
			outcome_code = ?,
			finished_at = ?
		WHERE id = ? AND phase = 'uncertain'
	`, string(req.Resolution), nowMs, req.AttemptID)
	if err != nil {
		return err
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return ports.ErrConflict
	}

	// 6. Update session
	var nextRetryAtMs *int64
	if req.Resolution == ports.ResolutionVerifiedSettled && req.Evidence.Failure.ResetAt != nil {
		ms := req.Evidence.Failure.ResetAt.UnixMilli()
		nextRetryAtMs = &ms
	}

	res, err = tx.ExecContext(ctx, `
		UPDATE sessions SET
			state = ?,
			revision = revision + 1,
			next_retry_at = COALESCE(?, next_retry_at),
			last_error_code = ?,
			updated_at = ?
		WHERE id = ? AND revision = ?
	`, string(destState), nextRetryAtMs, req.Reason, nowMs, req.SessionID, revision)
	if err != nil {
		return err
	}
	rowsAffected, _ = res.RowsAffected()
	if rowsAffected == 0 {
		return ports.ErrConflict
	}

	// 7. Record audited session event
	eventSource := fmt.Sprintf("operator:%s", req.Operator)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO session_events (
			id, session_id, sequence, state_from, state_to, event_source, attempt_id, failure_category, observed_at
		) VALUES (
			?, ?,
			(SELECT COALESCE(MAX(sequence), 0) + 1 FROM session_events WHERE session_id = ?),
			'NEEDS_ATTENTION', ?, ?, ?, NULL, ?
		)
	`, newID(), req.SessionID, req.SessionID, string(destState), eventSource, req.AttemptID, nowMs)
	if err != nil {
		return err
	}

	return tx.Commit()
}
