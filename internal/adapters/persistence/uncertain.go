package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

func (s *Store) MarkUncertain(ctx context.Context, attempt domain.Attempt, evidence domain.Evidence) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var sessionID, phase string
	var fence int64
	if err := tx.QueryRowContext(ctx, "SELECT session_id, fence, phase FROM attempts WHERE id = ?", attempt.ID).Scan(&sessionID, &fence, &phase); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.ErrNotFound
		}
		return err
	}
	if fence != attempt.Fence || (phase != string(domain.AttemptPhaseClaimed) && phase != string(domain.AttemptPhaseLaunching) && phase != string(domain.AttemptPhaseActive)) {
		return ports.ErrConflict
	}

	session, err := scanSession(tx.QueryRowContext(ctx, sessionSelectQuery+" WHERE s.id = ?", sessionID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.ErrNotFound
		}
		return err
	}
	if session.Fence != attempt.Fence {
		return ports.ErrConflict
	}
	if err := domain.ValidateTransition(session.State, domain.NeedsAttention); err != nil {
		return err
	}
	now := evidence.ObservedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if _, err := tx.ExecContext(ctx, "UPDATE attempts SET phase = 'uncertain' WHERE id = ? AND fence = ? AND phase = ?", attempt.ID, attempt.Fence, phase); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sessions SET state = 'NEEDS_ATTENTION', revision = revision + 1,
			turn_outcome = 'unknown',
			last_error_category = COALESCE(?, last_error_category),
			last_error_code = COALESCE(?, last_error_code), updated_at = ?
		WHERE id = ? AND revision = ? AND fence = ?`,
		nullString(string(evidence.Failure.Category)), nullString(evidence.Failure.Code), now.UnixMilli(),
		session.ID, session.Revision, attempt.Fence); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_events (id, session_id, sequence, state_from, state_to, event_source, attempt_id, failure_category, observed_at)
		VALUES (?, ?, (SELECT COALESCE(MAX(sequence), 0) + 1 FROM session_events WHERE session_id = ?), ?, 'NEEDS_ATTENTION', 'coordinator', ?, ?, ?)`,
		newID(), session.ID, session.ID, string(session.State), attempt.ID,
		nullString(string(evidence.Failure.Category)), now.UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

func nullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}
