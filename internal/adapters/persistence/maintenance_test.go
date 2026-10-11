package persistence_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Diaszano/quota-guardian/internal/adapters/persistence"
	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

func TestBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)

	// Populate data: session, context, attempt, events
	sessKey := "sub-key-backup-1"
	s, err := store.Create(ctx, ports.NewSession{
		SubmissionKey: &sessKey,
		Provider:      "claude",
		ContextID:     "ctx-backup-1",
		Mode:          domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/usr/local/bin/claude",
			ProviderVersion: "2.1.0",
			Cwd:             "/workspace/project",
			Profile:         "production",
		},
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	att, claimed, err := store.ClaimStart(ctx, ports.ClaimRequest{
		SessionID:     s.ID,
		OwnerEpoch:    "backup-epoch-1",
		LeaseDuration: 10 * time.Minute,
		Now:           now,
	})
	if err != nil || !claimed {
		t.Fatalf("failed to claim attempt: claimed=%v, err=%v", claimed, err)
	}

	// 1. Validation tests
	// Empty destination
	if err := store.Backup(ctx, ""); err == nil {
		t.Error("expected error for empty backup destination")
	}

	tempDir, err := os.MkdirTemp("", "qg-backup-dest-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Insecure directory permissions (e.g. 0777)
	insecureDir := filepath.Join(tempDir, "insecure")
	if err := os.Mkdir(insecureDir, 0777); err != nil {
		t.Fatalf("failed to create insecure dir: %v", err)
	}
	_ = os.Chmod(insecureDir, 0777) // ensure umask didn't mask it
	if err := store.Backup(ctx, filepath.Join(insecureDir, "backup.db")); err == nil {
		t.Error("expected error for insecure backup directory permissions")
	}

	// Symlink directory rejection
	secureDir := filepath.Join(tempDir, "secure")
	if err := os.Mkdir(secureDir, 0700); err != nil {
		t.Fatalf("failed to create secure dir: %v", err)
	}
	symlinkDir := filepath.Join(tempDir, "symlink-dir")
	if err := os.Symlink(secureDir, symlinkDir); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}
	if err := store.Backup(ctx, filepath.Join(symlinkDir, "backup.db")); err == nil {
		t.Error("expected error for symlink backup directory")
	}

	// Pre-existing destination file rejection
	backupFile := filepath.Join(secureDir, "valid-backup.db")
	if err := os.WriteFile(backupFile, []byte("existing"), 0600); err != nil {
		t.Fatalf("failed to create pre-existing file: %v", err)
	}
	if err := store.Backup(ctx, backupFile); err == nil {
		t.Error("expected error when backup file already exists")
	}
	_ = os.Remove(backupFile)

	// 2. Perform valid online backup
	if err := store.Backup(ctx, backupFile); err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	// Verify backup file mode is 0600
	fi, err := os.Stat(backupFile)
	if err != nil {
		t.Fatalf("failed to stat backup file: %v", err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("expected backup file mode 0600, got %04o", fi.Mode().Perm())
	}

	// 3. Restore and verify data integrity
	restoreDir := filepath.Join(tempDir, "restored-state")
	if err := os.Mkdir(restoreDir, 0700); err != nil {
		t.Fatalf("failed to create restore dir: %v", err)
	}

	// Copy backup file as quota-guardian.db in restoreDir
	restoredDBPath := filepath.Join(restoreDir, "quota-guardian.db")
	src, err := os.Open(backupFile)
	if err != nil {
		t.Fatalf("failed to open backup file: %v", err)
	}
	dst, err := os.OpenFile(restoredDBPath, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		src.Close()
		t.Fatalf("failed to create restored db file: %v", err)
	}
	_, err = io.Copy(dst, src)
	src.Close()
	dst.Close()
	if err != nil {
		t.Fatalf("failed to copy restored db: %v", err)
	}

	restoredStore, err := persistence.Open(ctx, restoreDir)
	if err != nil {
		t.Fatalf("failed to open restored store: %v", err)
	}
	defer restoredStore.Close()

	// Verify restored session
	restoredSess, err := restoredStore.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("failed to get session from restored store: %v", err)
	}
	if restoredSess.Provider != s.Provider || restoredSess.ContextID != s.ContextID {
		t.Errorf("restored session mismatch: got %+v, expected %+v", restoredSess, s)
	}
	if restoredSess.SubmissionKey == nil || *restoredSess.SubmissionKey != sessKey {
		t.Errorf("restored submission key mismatch: got %v", restoredSess.SubmissionKey)
	}

	// Verify restored attempt
	var restoredPhase string
	err = restoredStore.DB().QueryRowContext(ctx, "SELECT phase FROM attempts WHERE id = ?", att.ID).Scan(&restoredPhase)
	if err != nil {
		t.Fatalf("failed to query attempt from restored store: %v", err)
	}
	if restoredPhase != string(domain.AttemptPhaseClaimed) {
		t.Errorf("restored attempt phase mismatch: got %s", restoredPhase)
	}
}

func TestPruneRetention(t *testing.T) {
	ctx := context.Background()
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	cutoff := now.Add(-30 * 24 * time.Hour) // 30 days retention cutoff

	oldObserved := cutoff.Add(-10 * 24 * time.Hour).UnixMilli()   // 40 days ago
	recentObserved := cutoff.Add(10 * 24 * time.Hour).UnixMilli() // 20 days ago

	// 1. Create a terminal session (COMPLETED)
	subKey1 := "sub-prune-completed"
	sCompleted, err := store.Create(ctx, ports.NewSession{
		SubmissionKey: &subKey1,
		Provider:      "codex",
		ContextID:     "ctx-prune-1",
		Mode:          domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/usr/bin/codex",
			ProviderVersion: "1.0.0",
			Cwd:             "/workspace",
			Profile:         "default",
		},
	})
	if err != nil {
		t.Fatalf("failed to create completed session: %v", err)
	}
	_, err = store.Apply(ctx, domain.Transition{
		ExpectedRevision: sCompleted.Revision,
		Destination:      domain.Running,
		EventID:          newTestID(),
		Source:           "test",
		Evidence: domain.Evidence{
			ExpectedSessionID: sCompleted.ID,
		},
	})
	if err != nil {
		t.Fatalf("failed transition to RUNNING: %v", err)
	}
	sCompleted, err = store.Get(ctx, sCompleted.ID)
	if err != nil {
		t.Fatalf("failed to get sCompleted: %v", err)
	}
	_, err = store.Apply(ctx, domain.Transition{
		ExpectedRevision: sCompleted.Revision,
		Destination:      domain.Completed,
		EventID:          newTestID(),
		Source:           "test",
		Evidence: domain.Evidence{
			ExpectedSessionID: sCompleted.ID,
			TurnCompleted:     true,
		},
	})
	if err != nil {
		t.Fatalf("failed transition to COMPLETED: %v", err)
	}

	// Insert old and recent events for completed session
	oldEventID := newTestID()
	recentEventID := newTestID()
	_, err = store.DB().ExecContext(ctx, `
		INSERT INTO session_events (id, session_id, sequence, state_from, state_to, event_source, attempt_id, failure_category, observed_at)
		VALUES (?, ?, 101, 'DISCOVERED', 'RUNNING', 'test', NULL, NULL, ?)
	`, oldEventID, sCompleted.ID, oldObserved)
	if err != nil {
		t.Fatalf("failed to insert old event: %v", err)
	}
	_, err = store.DB().ExecContext(ctx, `
		INSERT INTO session_events (id, session_id, sequence, state_from, state_to, event_source, attempt_id, failure_category, observed_at)
		VALUES (?, ?, 102, 'RUNNING', 'COMPLETED', 'test', NULL, NULL, ?)
	`, recentEventID, sCompleted.ID, recentObserved)
	if err != nil {
		t.Fatalf("failed to insert recent event: %v", err)
	}

	// 2. Create an active / non-terminal session (RUNNING) with an old event
	subKey2 := "sub-prune-running"
	sRunning, err := store.Create(ctx, ports.NewSession{
		SubmissionKey: &subKey2,
		Provider:      "codex",
		ContextID:     "ctx-prune-2",
		Mode:          domain.ModeManaged,
		Context: domain.ExecutionContext{
			ExecutablePath:  "/usr/bin/codex",
			ProviderVersion: "1.0.0",
			Cwd:             "/workspace",
			Profile:         "default",
		},
	})
	if err != nil {
		t.Fatalf("failed to create running session: %v", err)
	}
	_, err = store.Apply(ctx, domain.Transition{
		ExpectedRevision: sRunning.Revision,
		Destination:      domain.Running,
		EventID:          newTestID(),
		Source:           "test",
		Evidence: domain.Evidence{
			ExpectedSessionID: sRunning.ID,
		},
	})
	if err != nil {
		t.Fatalf("failed transition to RUNNING: %v", err)
	}

	oldRunningEventID := newTestID()
	_, err = store.DB().ExecContext(ctx, `
		INSERT INTO session_events (id, session_id, sequence, state_from, state_to, event_source, attempt_id, failure_category, observed_at)
		VALUES (?, ?, 201, 'DISCOVERED', 'RUNNING', 'test', NULL, NULL, ?)
	`, oldRunningEventID, sRunning.ID, oldObserved)
	if err != nil {
		t.Fatalf("failed to insert running session event: %v", err)
	}

	// 3. Execute Prune
	if err := store.Prune(ctx, cutoff); err != nil {
		t.Fatalf("Prune failed: %v", err)
	}

	// Assert: old event for terminal session was pruned
	var count int
	err = store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM session_events WHERE id = ?", oldEventID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to check old event: %v", err)
	}
	if count != 0 {
		t.Errorf("expected old event %s to be pruned, but found %d", oldEventID, count)
	}

	// Assert: recent event for terminal session was PRESERVED
	err = store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM session_events WHERE id = ?", recentEventID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to check recent event: %v", err)
	}
	if count != 1 {
		t.Errorf("expected recent event %s to be preserved, found %d", recentEventID, count)
	}

	// Assert: old event for NON-TERMINAL session was PRESERVED
	err = store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM session_events WHERE id = ?", oldRunningEventID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to check non-terminal old event: %v", err)
	}
	if count != 1 {
		t.Errorf("expected non-terminal old event %s to be preserved, found %d", oldRunningEventID, count)
	}

	// Assert: sessions, submission keys, and execution contexts are NEVER pruned
	s1Check, err := store.Get(ctx, sCompleted.ID)
	if err != nil || s1Check.SubmissionKey == nil || *s1Check.SubmissionKey != subKey1 {
		t.Errorf("terminal session or submission key was altered by prune: %v, %v", s1Check, err)
	}

	s2Check, err := store.Get(ctx, sRunning.ID)
	if err != nil || s2Check.SubmissionKey == nil || *s2Check.SubmissionKey != subKey2 {
		t.Errorf("running session or submission key was altered by prune: %v, %v", s2Check, err)
	}
}
