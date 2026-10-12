package persistence

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant RFC 4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

const sessionSelectQuery = `
SELECT
    s.id, s.submission_key, s.provider, s.provider_session_id, s.context_id,
    s.mode, s.state, s.revision, s.fence,
    s.consent_revision, s.consent_enabled, s.consent_background_allowed,
    s.consent_approved_context_digest, s.consent_approved_at, s.consent_revoked_at,
    s.retry_count, s.retry_policy_margin_ms, s.retry_policy_base_delay_ms,
    s.retry_policy_cap_delay_ms, s.retry_policy_jitter_fraction,
    s.retry_policy_max_retries, s.retry_policy_deadline_ms,
    s.next_retry_at, s.deadline_at, s.cancel_requested, s.turn_outcome,
    s.last_error_category, s.last_error_code, s.last_error_reset_at,
    s.created_at, s.updated_at, s.last_observed_at,
    ec.executable_path, ec.provider_version, ec.cwd, ec.profile, ec.digest
FROM sessions s
JOIN execution_contexts ec ON s.context_digest = ec.digest
`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSession(s rowScanner) (domain.Session, error) {
	var (
		sess                          domain.Session
		submissionKey                 sql.NullString
		providerSessionID             sql.NullString
		approvedDigest                sql.NullString
		approvedAtMs                  sql.NullInt64
		revokedAtMs                   sql.NullInt64
		nextRetryAtMs                 sql.NullInt64
		deadlineAtMs                  sql.NullInt64
		cancelRequestedInt            int
		consentEnabledInt             int
		consentBgInt                  int
		lastErrCat                    sql.NullString
		lastErrCode                   sql.NullString
		lastErrResetMs                sql.NullInt64
		createdAtMs                   int64
		updatedAtMs                   int64
		lastObservedAtMs              sql.NullInt64
		retryMarginMs                 int64
		retryBaseMs                   int64
		retryCapMs                    int64
		retryJitter                   float64
		retryMax                      int
		retryDeadlineMs               int64
		stateStr                      string
		modeStr                       string
		turnOutcomeStr                string
		execPath, ver, cwd, prof, dig string
	)

	err := s.Scan(
		&sess.ID, &submissionKey, &sess.Provider, &providerSessionID, &sess.ContextID,
		&modeStr, &stateStr, &sess.Revision, &sess.Fence,
		&sess.Consent.Revision, &consentEnabledInt, &consentBgInt,
		&approvedDigest, &approvedAtMs, &revokedAtMs,
		&sess.RetryCount, &retryMarginMs, &retryBaseMs,
		&retryCapMs, &retryJitter,
		&retryMax, &retryDeadlineMs,
		&nextRetryAtMs, &deadlineAtMs, &cancelRequestedInt, &turnOutcomeStr,
		&lastErrCat, &lastErrCode, &lastErrResetMs,
		&createdAtMs, &updatedAtMs, &lastObservedAtMs,
		&execPath, &ver, &cwd, &prof, &dig,
	)
	if err != nil {
		return domain.Session{}, err
	}

	if submissionKey.Valid {
		sess.SubmissionKey = &submissionKey.String
	}
	if providerSessionID.Valid {
		sess.ProviderSessionID = &providerSessionID.String
	}
	sess.Mode = domain.SessionMode(modeStr)
	sess.State = domain.State(stateStr)
	sess.TurnOutcome = domain.TurnOutcome(turnOutcomeStr)
	sess.CancelRequested = cancelRequestedInt == 1

	sess.Consent.Enabled = consentEnabledInt == 1
	sess.Consent.BackgroundAllowed = consentBgInt == 1
	if approvedDigest.Valid {
		sess.Consent.ApprovedContextDigest = approvedDigest.String
	}
	if approvedAtMs.Valid {
		t := time.UnixMilli(approvedAtMs.Int64).UTC()
		sess.Consent.ApprovedAt = &t
	}
	if revokedAtMs.Valid {
		t := time.UnixMilli(revokedAtMs.Int64).UTC()
		sess.Consent.RevokedAt = &t
	}

	sess.RetryPolicy = domain.RetryPolicy{
		SafetyMargin:   time.Duration(retryMarginMs) * time.Millisecond,
		BaseDelay:      time.Duration(retryBaseMs) * time.Millisecond,
		CapDelay:       time.Duration(retryCapMs) * time.Millisecond,
		JitterFraction: retryJitter,
		MaxRetries:     retryMax,
		Deadline:       time.Duration(retryDeadlineMs) * time.Millisecond,
	}

	if nextRetryAtMs.Valid {
		t := time.UnixMilli(nextRetryAtMs.Int64).UTC()
		sess.NextRetryAt = &t
	}
	if deadlineAtMs.Valid {
		t := time.UnixMilli(deadlineAtMs.Int64).UTC()
		sess.DeadlineAt = &t
	}
	if lastObservedAtMs.Valid {
		t := time.UnixMilli(lastObservedAtMs.Int64).UTC()
		sess.LastObservedAt = &t
	}

	if lastErrCat.Valid || lastErrCode.Valid || lastErrResetMs.Valid {
		failure := &domain.Failure{
			Category: domain.FailureCategory(lastErrCat.String),
			Code:     lastErrCode.String,
		}
		if lastErrResetMs.Valid {
			t := time.UnixMilli(lastErrResetMs.Int64).UTC()
			failure.ResetAt = &t
		}
		sess.LastError = failure
	}

	sess.CreatedAt = time.UnixMilli(createdAtMs).UTC()
	sess.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()

	sess.Context = domain.ExecutionContext{
		ExecutablePath:  execPath,
		ProviderVersion: ver,
		Cwd:             cwd,
		Profile:         prof,
		Digest:          dig,
	}

	return sess, nil
}

// Get retrieves a session by its ID.
func (s *Store) Get(ctx context.Context, id string) (domain.Session, error) {
	row := s.db.QueryRowContext(ctx, sessionSelectQuery+" WHERE s.id = ?", id)
	sess, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Session{}, ports.ErrNotFound
		}
		return domain.Session{}, fmt.Errorf("failed to get session %s: %w", id, err)
	}
	return sess, nil
}

// getBySubmissionKey retrieves a session by its submission_key.
func (s *Store) getBySubmissionKey(ctx context.Context, key string) (domain.Session, error) {
	row := s.db.QueryRowContext(ctx, sessionSelectQuery+" WHERE s.submission_key = ?", key)
	sess, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Session{}, ports.ErrNotFound
		}
		return domain.Session{}, fmt.Errorf("failed to get session by submission key %s: %w", key, err)
	}
	return sess, nil
}

// Create inserts a new session or returns the existing session if submission_key matches.
// Prompt or prompt hash is never stored.
func (s *Store) Create(ctx context.Context, newSess ports.NewSession) (domain.Session, error) {
	// 1. Idempotent submission check: if submission_key already exists, return existing
	if newSess.SubmissionKey != nil && *newSess.SubmissionKey != "" {
		existing, err := s.getBySubmissionKey(ctx, *newSess.SubmissionKey)
		if err == nil {
			return existing, nil
		} else if !errors.Is(err, ports.ErrNotFound) {
			return domain.Session{}, err
		}
	}
	if newSess.ProviderSessionID != nil && *newSess.ProviderSessionID != "" {
		existing, err := s.getByProviderIdentity(ctx, newSess.Provider, newSess.ContextID, *newSess.ProviderSessionID)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, ports.ErrNotFound) {
			return domain.Session{}, err
		}
	}

	// 2. Prepare digest and execution context
	digest := newSess.Context.Digest
	if digest == "" {
		digest = domain.DigestContext(newSess.Context)
		newSess.Context.Digest = digest
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Session{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert execution context if not exists
	_, err = tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO execution_contexts (
			digest, executable_path, provider_version, cwd, profile, env_names_json
		) VALUES (?, ?, ?, ?, ?, '[]')
	`, digest, newSess.Context.ExecutablePath, newSess.Context.ProviderVersion, newSess.Context.Cwd, newSess.Context.Profile)
	if err != nil {
		return domain.Session{}, fmt.Errorf("failed to save execution context: %w", err)
	}

	// Session defaults
	now := time.Now().UTC()
	nowMs := now.UnixMilli()
	sessID := newID()

	mode := string(newSess.Mode)
	if mode == "" {
		mode = string(domain.ModeManaged)
	}

	rp := newSess.RetryPolicy
	if rp.SafetyMargin == 0 {
		rp.SafetyMargin = 30 * time.Second
	}
	if rp.BaseDelay == 0 {
		rp.BaseDelay = 60 * time.Second
	}
	if rp.CapDelay == 0 {
		rp.CapDelay = time.Hour
	}
	if rp.JitterFraction == 0 {
		rp.JitterFraction = 0.1
	}
	if rp.MaxRetries == 0 {
		rp.MaxRetries = 5
	}
	if rp.Deadline == 0 {
		rp.Deadline = 7 * 24 * time.Hour
	}

	deadlineAtMs := now.Add(rp.Deadline).UnixMilli()

	consentEnabled := 0
	if newSess.Consent.Enabled {
		consentEnabled = 1
	}
	consentBg := 0
	if newSess.Consent.BackgroundAllowed {
		consentBg = 1
	}
	var approvedAtMs *int64
	if newSess.Consent.ApprovedAt != nil {
		ms := newSess.Consent.ApprovedAt.UnixMilli()
		approvedAtMs = &ms
	}
	var revokedAtMs *int64
	if newSess.Consent.RevokedAt != nil {
		ms := newSess.Consent.RevokedAt.UnixMilli()
		revokedAtMs = &ms
	}
	var lastObservedAtMs *int64
	if newSess.LastObservedAt != nil {
		ms := newSess.LastObservedAt.UTC().UnixMilli()
		lastObservedAtMs = &ms
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO sessions (
			id, submission_key, provider, provider_session_id, context_id, mode, state,
			revision, fence, context_digest, consent_revision, consent_enabled,
			consent_background_allowed, consent_approved_context_digest, consent_approved_at,
			consent_revoked_at, retry_count, retry_policy_margin_ms, retry_policy_base_delay_ms,
			retry_policy_cap_delay_ms, retry_policy_jitter_fraction, retry_policy_max_retries,
			retry_policy_deadline_ms, next_retry_at, deadline_at, cancel_requested,
			turn_outcome, last_error_category, last_error_code, last_error_reset_at,
			created_at, updated_at, last_observed_at
		) VALUES (
			?, ?, ?, ?, ?, ?, 'DISCOVERED',
			1, 0, ?, ?, ?,
			?, ?, ?,
			?, 0, ?, ?,
			?, ?, ?,
			?, NULL, ?, 0,
			'unknown', NULL, NULL, NULL,
			?, ?, ?
		)
	`,
		sessID, newSess.SubmissionKey, newSess.Provider, newSess.ProviderSessionID, newSess.ContextID, mode,
		digest, newSess.Consent.Revision, consentEnabled,
		consentBg, newSess.Consent.ApprovedContextDigest, approvedAtMs,
		revokedAtMs, rp.SafetyMargin.Milliseconds(), rp.BaseDelay.Milliseconds(),
		rp.CapDelay.Milliseconds(), rp.JitterFraction, rp.MaxRetries,
		rp.Deadline.Milliseconds(), deadlineAtMs,
		nowMs, nowMs, lastObservedAtMs,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: sessions.submission_key") && newSess.SubmissionKey != nil {
			_ = tx.Rollback()
			return s.getBySubmissionKey(ctx, *newSess.SubmissionKey)
		}
		if strings.Contains(err.Error(), "sessions.provider") && newSess.ProviderSessionID != nil {
			_ = tx.Rollback()
			return s.getByProviderIdentity(ctx, newSess.Provider, newSess.ContextID, *newSess.ProviderSessionID)
		}
		return domain.Session{}, fmt.Errorf("failed to insert session: %w", err)
	}

	// Insert initial discovery event
	_, err = tx.ExecContext(ctx, `
		INSERT INTO session_events (
			id, session_id, sequence, state_from, state_to, event_source, attempt_id, failure_category, observed_at
		) VALUES (?, ?, 1, 'DISCOVERED', 'DISCOVERED', 'submission', NULL, NULL, ?)
	`, newID(), sessID, nowMs)
	if err != nil {
		return domain.Session{}, fmt.Errorf("failed to insert initial event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return domain.Session{}, fmt.Errorf("failed to commit session creation: %w", err)
	}

	return s.Get(ctx, sessID)
}

func (s *Store) getByProviderIdentity(ctx context.Context, provider, contextID, providerSessionID string) (domain.Session, error) {
	row := s.db.QueryRowContext(ctx, sessionSelectQuery+" WHERE s.provider=? AND s.context_id=? AND s.provider_session_id=?", provider, contextID, providerSessionID)
	session, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Session{}, ports.ErrNotFound
	}
	if err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

func (s *Store) RecordObserved(ctx context.Context, sessionID string, observedAt time.Time) error {
	ms := observedAt.UTC().UnixMilli()
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_observed_at=CASE WHEN last_observed_at IS NULL OR last_observed_at<? THEN ? ELSE last_observed_at END WHERE id=? AND mode='observed'`, ms, ms, sessionID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ports.ErrNotFound
	}
	return nil
}

// List returns sessions matching the filter criteria.
func (s *Store) List(ctx context.Context, filter ports.SessionFilter) ([]domain.Session, error) {
	var (
		conditions []string
		args       []any
	)

	if filter.State != nil {
		conditions = append(conditions, "s.state = ?")
		args = append(args, string(*filter.State))
	}
	if filter.Provider != nil {
		conditions = append(conditions, "s.provider = ?")
		args = append(args, *filter.Provider)
	}
	if filter.Mode != nil {
		conditions = append(conditions, "s.mode = ?")
		args = append(args, string(*filter.Mode))
	}
	if filter.DueOnly {
		nowMs := filter.Now.UnixMilli()
		if filter.Now.IsZero() {
			nowMs = time.Now().UTC().UnixMilli()
		}
		conditions = append(conditions, "s.state IN ('READY_TO_RESUME', 'WAITING_FOR_RESET') AND s.next_retry_at IS NOT NULL AND s.next_retry_at <= ?")
		args = append(args, nowMs)
	}

	query := sessionSelectQuery
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY s.next_retry_at ASC, s.created_at ASC, s.id ASC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query sessions: %w", err)
	}
	defer rows.Close()

	var sessions []domain.Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan session row: %w", err)
		}
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return sessions, nil
}

// Apply updates session state, checks revision/fence, increments revision, and records session event.
func (s *Store) Apply(ctx context.Context, t domain.Transition) (domain.Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Session{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Locate target session ID
	sessionID := t.Evidence.ExpectedSessionID
	if sessionID == "" && t.AttemptID != "" {
		err = tx.QueryRowContext(ctx, "SELECT session_id FROM attempts WHERE id = ?", t.AttemptID).Scan(&sessionID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.Session{}, ports.ErrNotFound
			}
			return domain.Session{}, err
		}
	}
	if sessionID == "" {
		return domain.Session{}, errors.New("cannot apply transition: missing session ID")
	}

	// Fetch current session for update
	row := tx.QueryRowContext(ctx, sessionSelectQuery+" WHERE s.id = ?", sessionID)
	curr, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Session{}, ports.ErrNotFound
		}
		return domain.Session{}, err
	}

	// Validate revision and fence
	if t.ExpectedRevision > 0 && curr.Revision != t.ExpectedRevision {
		return domain.Session{}, ports.ErrConflict
	}
	if t.ExpectedState != "" && curr.State != t.ExpectedState {
		return domain.Session{}, ports.ErrConflict
	}
	if t.Fence > 0 && curr.Fence != t.Fence {
		return domain.Session{}, ports.ErrConflict
	}

	// Validate state transition rules
	if err := domain.ValidateTransition(curr.State, t.Destination); err != nil {
		return domain.Session{}, err
	}

	now := time.Now().UTC()
	nowMs := now.UnixMilli()
	if !t.Evidence.ObservedAt.IsZero() {
		nowMs = t.Evidence.ObservedAt.UnixMilli()
	}

	// Determine outcome & next retry
	turnOutcome := string(curr.TurnOutcome)
	var nextRetryAtMs *int64
	if curr.NextRetryAt != nil {
		ms := curr.NextRetryAt.UnixMilli()
		nextRetryAtMs = &ms
	}
	if t.Evidence.Failure.ResetAt != nil {
		ms := t.Evidence.Failure.ResetAt.UnixMilli()
		nextRetryAtMs = &ms
	}

	if t.Evidence.TurnCompleted {
		turnOutcome = string(domain.TurnOutcomeCompleted)
	} else if t.Evidence.PermissionBlocked {
		turnOutcome = string(domain.TurnOutcomePermissionBlocked)
	} else if t.Evidence.Failure.Category == domain.Quota {
		turnOutcome = string(domain.TurnOutcomeQuota)
	} else if t.Evidence.Failure.Category != "" {
		turnOutcome = string(domain.TurnOutcomeFailed)
	}

	var lastErrCat, lastErrCode sql.NullString
	var lastErrResetMs sql.NullInt64
	if t.Evidence.Failure.Category != "" {
		lastErrCat = sql.NullString{String: string(t.Evidence.Failure.Category), Valid: true}
	}
	if t.Evidence.Failure.Code != "" {
		lastErrCode = sql.NullString{String: t.Evidence.Failure.Code, Valid: true}
	}
	if t.Evidence.Failure.ResetAt != nil {
		lastErrResetMs = sql.NullInt64{Int64: t.Evidence.Failure.ResetAt.UnixMilli(), Valid: true}
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
		string(t.Destination), turnOutcome, nextRetryAtMs,
		lastErrCat, lastErrCode, lastErrResetMs, nowMs,
		curr.ID, curr.Revision,
	)
	if err != nil {
		return domain.Session{}, fmt.Errorf("failed to update session state: %w", err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return domain.Session{}, ports.ErrConflict
	}

	// Insert event
	eventID := t.EventID
	if eventID == "" {
		eventID = newID()
	}
	source := t.Source
	if source == "" {
		source = "coordinator"
	}
	var attemptID sql.NullString
	if t.AttemptID != "" {
		attemptID = sql.NullString{String: t.AttemptID, Valid: true}
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
		eventID, curr.ID, curr.ID,
		string(curr.State), string(t.Destination), source,
		attemptID, lastErrCat, nowMs,
	)
	if err != nil {
		return domain.Session{}, fmt.Errorf("failed to record transition event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return domain.Session{}, fmt.Errorf("failed to commit transition: %w", err)
	}

	return s.Get(ctx, curr.ID)
}

// Cancel sets cancel_requested = 1 and transitions session to CANCELLED if permitted and idle.
func (s *Store) Cancel(ctx context.Context, id string, expectedRevision int64) (domain.Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Session{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, sessionSelectQuery+" WHERE s.id = ?", id)
	sess, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Session{}, ports.ErrNotFound
		}
		return domain.Session{}, err
	}

	if expectedRevision > 0 && sess.Revision != expectedRevision {
		return sess, ports.ErrConflict
	}
	if domain.IsTerminal(sess.State) {
		return sess, domain.ErrTerminalStateImmutable
	}

	nowMs := time.Now().UTC().UnixMilli()

	// Check if there are active/open attempts
	var openAttempts int
	err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM attempts
		WHERE session_id = ? AND phase IN ('claimed', 'launching', 'active', 'uncertain')
	`, id).Scan(&openAttempts)
	if err != nil {
		return domain.Session{}, err
	}

	if openAttempts == 0 {
		// No active attempt: directly transition to CANCELLED
		res, err := tx.ExecContext(ctx, `
			UPDATE sessions SET
				state = 'CANCELLED',
				cancel_requested = 1,
				revision = revision + 1,
				updated_at = ?
			WHERE id = ? AND revision = ?
		`, nowMs, id, sess.Revision)
		if err != nil {
			return domain.Session{}, err
		}
		rowsAffected, _ := res.RowsAffected()
		if rowsAffected == 0 {
			return domain.Session{}, ports.ErrConflict
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO session_events (
				id, session_id, sequence, state_from, state_to, event_source, attempt_id, failure_category, observed_at
			) VALUES (
				?, ?,
				(SELECT COALESCE(MAX(sequence), 0) + 1 FROM session_events WHERE session_id = ?),
				?, 'CANCELLED', 'cancellation', NULL, NULL, ?
			)
		`, newID(), id, id, string(sess.State), nowMs)
		if err != nil {
			return domain.Session{}, err
		}
	} else {
		// Active attempt present: set cancel_requested flag and bump revision
		res, err := tx.ExecContext(ctx, `
			UPDATE sessions SET
				cancel_requested = 1,
				revision = revision + 1,
				updated_at = ?
			WHERE id = ? AND revision = ?
		`, nowMs, id, sess.Revision)
		if err != nil {
			return domain.Session{}, err
		}
		rowsAffected, _ := res.RowsAffected()
		if rowsAffected == 0 {
			return domain.Session{}, ports.ErrConflict
		}
	}

	if err := tx.Commit(); err != nil {
		return domain.Session{}, fmt.Errorf("failed to commit cancellation: %w", err)
	}

	return s.Get(ctx, id)
}

// SetConsent updates session consent, checking revision.
func (s *Store) SetConsent(ctx context.Context, id string, expectedRevision int64, consent domain.Consent) (domain.Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Session{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, sessionSelectQuery+" WHERE s.id = ?", id)
	sess, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Session{}, ports.ErrNotFound
		}
		return domain.Session{}, err
	}

	if expectedRevision > 0 && sess.Revision != expectedRevision {
		return domain.Session{}, ports.ErrConflict
	}
	if domain.IsTerminal(sess.State) {
		return domain.Session{}, domain.ErrTerminalStateImmutable
	}

	nowMs := time.Now().UTC().UnixMilli()

	consentEnabled := 0
	if consent.Enabled {
		consentEnabled = 1
	}
	consentBg := 0
	if consent.BackgroundAllowed {
		consentBg = 1
	}
	var approvedAtMs *int64
	if consent.ApprovedAt != nil {
		ms := consent.ApprovedAt.UnixMilli()
		approvedAtMs = &ms
	}
	var revokedAtMs *int64
	if consent.RevokedAt != nil {
		ms := consent.RevokedAt.UnixMilli()
		revokedAtMs = &ms
	}

	consentRev := sess.Consent.Revision + 1
	if consent.Revision > consentRev {
		consentRev = consent.Revision
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE sessions SET
			consent_revision = ?,
			consent_enabled = ?,
			consent_background_allowed = ?,
			consent_approved_context_digest = ?,
			consent_approved_at = ?,
			consent_revoked_at = ?,
			revision = revision + 1,
			updated_at = ?
		WHERE id = ? AND revision = ?
	`,
		consentRev, consentEnabled, consentBg,
		consent.ApprovedContextDigest, approvedAtMs, revokedAtMs,
		nowMs, id, sess.Revision,
	)
	if err != nil {
		return domain.Session{}, fmt.Errorf("failed to update consent: %w", err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return domain.Session{}, ports.ErrConflict
	}

	if err := tx.Commit(); err != nil {
		return domain.Session{}, fmt.Errorf("failed to commit consent update: %w", err)
	}

	return s.Get(ctx, id)
}
