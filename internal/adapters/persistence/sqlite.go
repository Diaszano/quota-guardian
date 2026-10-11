package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"syscall"

	_ "modernc.org/sqlite"
)

type Store struct {
	db       *sql.DB
	stateDir string
}

// DB returns the underlying *sql.DB for low-level or testing access.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Open validates the private directory permissions, configures SQLite with WAL, FULL synchronous,
// foreign keys, and 2000ms busy timeout, and applies migrations.
func Open(ctx context.Context, stateDir string) (*Store, error) {
	// 1. Validate or create state directory with mode 0700
	info, err := os.Lstat(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(stateDir, 0700); err != nil {
				return nil, fmt.Errorf("failed to create state dir: %w", err)
			}
			info, err = os.Lstat(stateDir)
			if err != nil {
				return nil, fmt.Errorf("failed to stat created state dir: %w", err)
			}
		} else {
			return nil, fmt.Errorf("failed to stat state dir: %w", err)
		}
	}

	// Reject symlinks
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("state dir %s is a symlink: rejected", stateDir)
	}

	// Verify directory permissions mode 0700
	if info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("state dir %s has permissions %04o; expected 0700", stateDir, info.Mode().Perm())
	}

	// Verify ownership matches current process UID
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		currentUID := uint32(os.Getuid())
		if stat.Uid != currentUID {
			return nil, fmt.Errorf("state dir %s owned by uid %d, expected %d", stateDir, stat.Uid, currentUID)
		}
	}

	dbPath := filepath.Join(stateDir, "quota-guardian.db")

	// If database file exists, ensure it is not a symlink and has permissions 0600
	if fi, err := os.Lstat(dbPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("database file %s is a symlink: rejected", dbPath)
		}
		if fi.Mode().Perm() != 0600 {
			return nil, fmt.Errorf("database file %s has permissions %04o; expected 0600", dbPath, fi.Mode().Perm())
		}
		if stat, ok := fi.Sys().(*syscall.Stat_t); ok {
			currentUID := uint32(os.Getuid())
			if stat.Uid != currentUID {
				return nil, fmt.Errorf("database file %s owned by uid %d, expected %d", dbPath, stat.Uid, currentUID)
			}
		}
	}

	// Also check WAL / SHM files if they exist
	for _, ext := range []string{"-wal", "-shm"} {
		sidecar := dbPath + ext
		if fi, err := os.Lstat(sidecar); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("sidecar file %s is a symlink: rejected", sidecar)
			}
			if fi.Mode().Perm() != 0600 {
				_ = os.Chmod(sidecar, 0600)
			}
		}
	}

	// Connection DSN with PRAGMA parameters
	// SQLite single writer connection: _pragma=busy_timeout(2000)&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)
	values := url.Values{}
	values.Add("_pragma", "busy_timeout(2000)")
	values.Add("_pragma", "foreign_keys(ON)")
	values.Add("_pragma", "journal_mode(WAL)")
	values.Add("_pragma", "synchronous(FULL)")

	dsn := fmt.Sprintf("file:%s?%s", dbPath, values.Encode())

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Single writer connection
	db.SetMaxOpenConns(1)

	// Ensure database file permissions are 0600 if created
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Ensure file permissions 0600 on db file and WAL
	if fi, err := os.Lstat(dbPath); err == nil {
		if fi.Mode().Perm() != 0600 {
			_ = os.Chmod(dbPath, 0600)
		}
	}
	for _, ext := range []string{"-wal", "-shm"} {
		sidecar := dbPath + ext
		if fi, err := os.Lstat(sidecar); err == nil {
			if fi.Mode().Perm() != 0600 {
				_ = os.Chmod(sidecar, 0600)
			}
		}
	}

	// Execute explicit PRAGMAs to be 100% certain
	pragmas := []string{
		"PRAGMA foreign_keys = ON;",
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = FULL;",
		"PRAGMA busy_timeout = 2000;",
	}
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed executing pragma %q: %w", p, err)
		}
	}

	// Run embedded migrations
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to apply migrations: %w", err)
	}

	return &Store{
		db:       db,
		stateDir: stateDir,
	}, nil
}
