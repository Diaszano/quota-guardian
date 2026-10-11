package persistence_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Diaszano/quota-guardian/internal/adapters/persistence"
)

func TestOpenPrivateState(t *testing.T) {
	ctx := context.Background()
	tempDir, err := os.MkdirTemp("", "qg-state-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	stateDir := filepath.Join(tempDir, "state")

	store, err := persistence.Open(ctx, stateDir)
	if err != nil {
		t.Fatalf("expected Open to succeed, got: %v", err)
	}
	defer store.Close()

	// 1. Verify state directory permissions (0700)
	dirInfo, err := os.Lstat(stateDir)
	if err != nil {
		t.Fatalf("failed to stat state dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0700 {
		t.Errorf("expected state dir permissions 0700, got %04o", perm)
	}

	// 2. Verify database file exists and permissions are 0600
	dbPath := filepath.Join(stateDir, "quota-guardian.db")
	dbInfo, err := os.Lstat(dbPath)
	if err != nil {
		t.Fatalf("failed to stat db file: %v", err)
	}
	if perm := dbInfo.Mode().Perm(); perm != 0600 {
		t.Errorf("expected db file permissions 0600, got %04o", perm)
	}

	// 3. Query SQLite PRAGMAs directly over SQL connection
	db := store.DB()
	if db == nil {
		t.Fatalf("store.DB() returned nil")
	}

	var foreignKeys int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys;").Scan(&foreignKeys); err != nil {
		t.Fatalf("failed to query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("expected PRAGMA foreign_keys = 1, got %d", foreignKeys)
	}

	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		t.Fatalf("failed to query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("expected PRAGMA journal_mode = wal, got %s", journalMode)
	}

	var synchronous int
	if err := db.QueryRowContext(ctx, "PRAGMA synchronous;").Scan(&synchronous); err != nil {
		t.Fatalf("failed to query synchronous: %v", err)
	}
	// FULL is 2 in SQLite
	if synchronous != 2 {
		t.Errorf("expected PRAGMA synchronous = 2 (FULL), got %d", synchronous)
	}

	var busyTimeout int
	if err := db.QueryRowContext(ctx, "PRAGMA busy_timeout;").Scan(&busyTimeout); err != nil {
		t.Fatalf("failed to query busy_timeout: %v", err)
	}
	if busyTimeout != 2000 {
		t.Errorf("expected PRAGMA busy_timeout = 2000, got %d", busyTimeout)
	}

	// 4. Test symlink rejection: Symlink at stateDir must be rejected
	symlinkDir := filepath.Join(tempDir, "state-symlink")
	if err := os.Symlink(stateDir, symlinkDir); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}
	_, err = persistence.Open(ctx, symlinkDir)
	if err == nil {
		t.Fatalf("expected Open on symlink directory to fail, but it succeeded")
	}
}

func TestMigrationsFailClosed(t *testing.T) {
	ctx := context.Background()

	t.Run("checksum mismatch fails closed", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "qg-corrupt-*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tempDir)

		stateDir := filepath.Join(tempDir, "state")
		store, err := persistence.Open(ctx, stateDir)
		if err != nil {
			t.Fatalf("failed to open store initially: %v", err)
		}

		// Mutate checksum in schema_migrations
		db := store.DB()
		_, err = db.ExecContext(ctx, "UPDATE schema_migrations SET checksum = 'tampered' WHERE version = 1")
		if err != nil {
			t.Fatalf("failed to tamper schema_migrations: %v", err)
		}
		store.Close()

		// Reopening must fail closed
		_, err = persistence.Open(ctx, stateDir)
		if err == nil {
			t.Fatalf("expected Open to fail on checksum mismatch, but succeeded")
		}
	})

	t.Run("newer schema version fails closed", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "qg-newer-*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tempDir)

		stateDir := filepath.Join(tempDir, "state")
		store, err := persistence.Open(ctx, stateDir)
		if err != nil {
			t.Fatalf("failed to open store initially: %v", err)
		}

		db := store.DB()
		_, err = db.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at, checksum) VALUES (999, 123456789, 'future')")
		if err != nil {
			t.Fatalf("failed to insert future migration: %v", err)
		}
		store.Close()

		// Reopening must fail closed
		_, err = persistence.Open(ctx, stateDir)
		if err == nil {
			t.Fatalf("expected Open to fail on newer schema version, but succeeded")
		}
	})

	t.Run("corrupt database fails closed", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "qg-corrupt-file-*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tempDir)

		stateDir := filepath.Join(tempDir, "state")
		if err := os.MkdirAll(stateDir, 0700); err != nil {
			t.Fatalf("failed to create state dir: %v", err)
		}
		dbPath := filepath.Join(stateDir, "quota-guardian.db")
		if err := os.WriteFile(dbPath, []byte("NOT A VALID SQLITE DATABASE HEADER"), 0600); err != nil {
			t.Fatalf("failed to write garbage to db file: %v", err)
		}

		_, err = persistence.Open(ctx, stateDir)
		if err == nil {
			t.Fatalf("expected Open to fail on corrupted sqlite file, but succeeded")
		}
	})
}

func TestSchemaTablesAndConstraints(t *testing.T) {
	ctx := context.Background()
	tempDir, err := os.MkdirTemp("", "qg-schema-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	stateDir := filepath.Join(tempDir, "state")
	store, err := persistence.Open(ctx, stateDir)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	db := store.DB()

	// Verify required tables exist
	requiredTables := []string{
		"schema_migrations",
		"execution_contexts",
		"sessions",
		"attempts",
		"session_events",
	}

	for _, tbl := range requiredTables {
		var name string
		err := db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing in schema: %v", tbl, err)
		}
	}

	// Verify turn_outcome CHECK constraint rejects invalid values
	_, err = db.ExecContext(ctx, `
		INSERT INTO sessions (
			id, provider, context_id, mode, state, context_digest, turn_outcome, created_at, updated_at
		) VALUES (
			'test-1', 'agy', 'ctx-1', 'managed', 'RUNNING', 'digest-dummy', 'invalid_turn_outcome', 1000, 1000
		)
	`)
	if err == nil {
		t.Errorf("expected insert with invalid turn_outcome to fail CHECK constraint, but succeeded")
	}

	// Verify foreign keys are enforced: inserting session with non-existent context_digest fails
	_, err = db.ExecContext(ctx, `
		INSERT INTO sessions (
			id, provider, context_id, mode, state, context_digest, turn_outcome, created_at, updated_at
		) VALUES (
			'test-fk', 'agy', 'ctx-1', 'managed', 'RUNNING', 'non-existent-digest', 'unknown', 1000, 1000
		)
	`)
	if err == nil {
		t.Errorf("expected insert with non-existent foreign key to fail, but succeeded")
	}

	// Verify valid insert works when execution_context is present
	_, err = db.ExecContext(ctx, `
		INSERT INTO execution_contexts (digest, executable_path, provider_version, cwd, profile, env_names_json)
		VALUES ('valid-digest', '/usr/bin/agy', '1.3.3', '/workspace', 'default', '[]')
	`)
	if err != nil {
		t.Fatalf("failed to insert valid execution_context: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO sessions (
			id, submission_key, provider, context_id, mode, state, revision, fence,
			context_digest, turn_outcome, created_at, updated_at
		) VALUES (
			'sess-1', 'sub-key-1', 'agy', 'ctx-1', 'managed', 'RUNNING', 1, 0,
			'valid-digest', 'unknown', 1000, 1000
		)
	`)
	if err != nil {
		t.Fatalf("failed to insert valid session: %v", err)
	}

	// Verify unique submission_key constraint
	_, err = db.ExecContext(ctx, `
		INSERT INTO sessions (
			id, submission_key, provider, context_id, mode, state, revision, fence,
			context_digest, turn_outcome, created_at, updated_at
		) VALUES (
			'sess-2', 'sub-key-1', 'agy', 'ctx-1', 'managed', 'RUNNING', 1, 0,
			'valid-digest', 'unknown', 1000, 1000
		)
	`)
	if err == nil {
		t.Errorf("expected duplicate submission_key to fail UNIQUE constraint, but succeeded")
	}
}
