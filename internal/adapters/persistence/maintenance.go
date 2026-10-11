package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Backup creates an online, transactional backup of the database to destination.
// It verifies destination directory security and uses SQLite's transactional VACUUM INTO
// to safely checkpoint WAL contents without raw file copy.
func (s *Store) Backup(ctx context.Context, destination string) error {
	if destination == "" {
		return errors.New("backup destination cannot be empty")
	}

	cleanDest := filepath.Clean(destination)
	dir := filepath.Dir(cleanDest)

	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("backup destination directory error: %w", err)
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("backup destination directory %s is a symlink: rejected", dir)
	}
	if !dirInfo.IsDir() {
		return fmt.Errorf("backup destination directory %s is not a directory", dir)
	}
	if dirInfo.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("backup destination directory %s has insecure permissions %04o: must not be group/world accessible", dir, dirInfo.Mode().Perm())
	}
	if stat, ok := dirInfo.Sys().(*syscall.Stat_t); ok {
		currentUID := uint32(os.Getuid())
		if stat.Uid != currentUID {
			return fmt.Errorf("backup destination directory %s owned by uid %d, expected %d", dir, stat.Uid, currentUID)
		}
	}

	if _, err := os.Lstat(cleanDest); err == nil {
		return fmt.Errorf("backup destination file %s already exists: rejected", cleanDest)
	}

	// modernc.org/sqlite supports VACUUM INTO ? or string literal
	_, err = s.db.ExecContext(ctx, "VACUUM INTO ?", cleanDest)
	if err != nil {
		escaped := strings.ReplaceAll(cleanDest, "'", "''")
		_, err = s.db.ExecContext(ctx, fmt.Sprintf("VACUUM INTO '%s';", escaped))
		if err != nil {
			return fmt.Errorf("failed executing VACUUM INTO: %w", err)
		}
	}

	if err := os.Chmod(cleanDest, 0600); err != nil {
		return fmt.Errorf("failed setting backup permissions: %w", err)
	}

	return nil
}

// Prune purges historical session events older than retention cutoff for terminal sessions.
// It strictly preserves active, waiting, and uncertain sessions and attempts, submission keys,
// and execution contexts.
func (s *Store) Prune(ctx context.Context, before time.Time) error {
	if before.IsZero() {
		return errors.New("prune before time cannot be zero")
	}

	beforeMs := before.UTC().UnixMilli()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin prune transaction: %w", err)
	}
	defer tx.Rollback()

	// Prune terminal session events older than retention cutoff
	// Preserves non-terminal events, sessions, submission keys, execution contexts, attempts
	_, err = tx.ExecContext(ctx, `
		DELETE FROM session_events
		WHERE observed_at < ?
		  AND session_id IN (
		      SELECT id FROM sessions
		      WHERE state IN ('COMPLETED', 'FAILED', 'CANCELLED')
		  )
	`, beforeMs)
	if err != nil {
		return fmt.Errorf("failed to prune terminal session events: %w", err)
	}

	return tx.Commit()
}
