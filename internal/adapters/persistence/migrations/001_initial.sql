-- 001_initial.sql: Schema migrations and core persistence tables

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL,
    checksum TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS execution_contexts (
    digest TEXT PRIMARY KEY,
    executable_path TEXT NOT NULL,
    provider_version TEXT NOT NULL,
    cwd TEXT NOT NULL,
    profile TEXT NOT NULL,
    env_names_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    submission_key TEXT UNIQUE,
    provider TEXT NOT NULL,
    provider_session_id TEXT,
    context_id TEXT NOT NULL,
    mode TEXT NOT NULL CHECK(mode IN ('managed', 'observed')),
    state TEXT NOT NULL CHECK(state IN (
        'DISCOVERED', 'RUNNING', 'QUOTA_EXHAUSTED', 'WAITING_FOR_RESET',
        'READY_TO_RESUME', 'RESUMING', 'NEEDS_ATTENTION', 'COMPLETED',
        'FAILED', 'CANCELLED'
    )),
    revision INTEGER NOT NULL DEFAULT 1,
    fence INTEGER NOT NULL DEFAULT 0,
    context_digest TEXT NOT NULL REFERENCES execution_contexts(digest),
    consent_revision INTEGER NOT NULL DEFAULT 0,
    consent_enabled INTEGER NOT NULL DEFAULT 0,
    consent_background_allowed INTEGER NOT NULL DEFAULT 0,
    consent_approved_context_digest TEXT,
    consent_approved_at INTEGER,
    consent_revoked_at INTEGER,
    retry_count INTEGER NOT NULL DEFAULT 0 CHECK(retry_count >= 0),
    retry_policy_margin_ms INTEGER NOT NULL DEFAULT 30000,
    retry_policy_base_delay_ms INTEGER NOT NULL DEFAULT 60000,
    retry_policy_cap_delay_ms INTEGER NOT NULL DEFAULT 3600000,
    retry_policy_jitter_fraction REAL NOT NULL DEFAULT 0.1,
    retry_policy_max_retries INTEGER NOT NULL DEFAULT 5,
    retry_policy_deadline_ms INTEGER NOT NULL DEFAULT 604800000,
    next_retry_at INTEGER,
    deadline_at INTEGER,
    cancel_requested INTEGER NOT NULL DEFAULT 0,
    turn_outcome TEXT NOT NULL DEFAULT 'unknown' CHECK(turn_outcome IN ('unknown', 'completed', 'quota', 'permission_blocked', 'failed')),
    last_error_category TEXT,
    last_error_code TEXT,
    last_error_reset_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_observed_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_sessions_due ON sessions (state, next_retry_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_provider_identity ON sessions (provider, context_id, provider_session_id) WHERE provider_session_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS attempts (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('start', 'resume')),
    phase TEXT NOT NULL CHECK(phase IN ('claimed', 'launching', 'active', 'finished', 'uncertain')),
    owner_epoch TEXT NOT NULL,
    fence INTEGER NOT NULL,
    lease_deadline INTEGER NOT NULL,
    pid INTEGER,
    boot_id TEXT,
    start_ticks INTEGER,
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    outcome_code TEXT,
    CONSTRAINT uq_session_attempt UNIQUE (session_id, attempt_number)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_attempts_open_per_session ON attempts (session_id) WHERE phase IN ('claimed', 'launching', 'active', 'uncertain');

CREATE TABLE IF NOT EXISTS session_events (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    state_from TEXT NOT NULL,
    state_to TEXT NOT NULL,
    event_source TEXT NOT NULL,
    attempt_id TEXT REFERENCES attempts(id),
    failure_category TEXT,
    observed_at INTEGER NOT NULL,
    CONSTRAINT uq_session_sequence UNIQUE (session_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_session_events_session ON session_events (session_id, observed_at);
