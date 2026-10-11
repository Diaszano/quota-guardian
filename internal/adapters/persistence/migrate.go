package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migrationFile struct {
	version  int
	name     string
	sql      string
	checksum string
}

func loadMigrationFiles() ([]migrationFile, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory: %w", err)
	}

	var migrations []migrationFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		parts := strings.SplitN(entry.Name(), "_", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid migration filename: %s", entry.Name())
		}

		version, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("invalid migration version in filename %s: %w", entry.Name(), err)
		}

		content, err := migrationFS.ReadFile(path.Join("migrations", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read migration file %s: %w", entry.Name(), err)
		}

		hash := sha256.Sum256(content)
		checksum := hex.EncodeToString(hash[:])

		migrations = append(migrations, migrationFile{
			version:  version,
			name:     entry.Name(),
			sql:      string(content),
			checksum: checksum,
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	return migrations, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	migrations, err := loadMigrationFiles()
	if err != nil {
		return err
	}

	// 1. Ensure schema_migrations table exists
	createTableSQL := `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL,
		checksum TEXT NOT NULL
	);`
	if _, err := db.ExecContext(ctx, createTableSQL); err != nil {
		return fmt.Errorf("failed to initialize schema_migrations: %w", err)
	}

	// 2. Query applied migrations
	rows, err := db.QueryContext(ctx, "SELECT version, checksum FROM schema_migrations ORDER BY version ASC")
	if err != nil {
		return fmt.Errorf("failed to query applied schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]string)
	var maxAppliedVersion int
	for rows.Next() {
		var ver int
		var csum string
		if err := rows.Scan(&ver, &csum); err != nil {
			return fmt.Errorf("failed to scan migration row: %w", err)
		}
		applied[ver] = csum
		if ver > maxAppliedVersion {
			maxAppliedVersion = ver
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error reading applied migrations: %w", err)
	}

	// 3. Fail closed if database has newer migrations unknown to this binary
	var maxBinaryVersion int
	if len(migrations) > 0 {
		maxBinaryVersion = migrations[len(migrations)-1].version
	}
	if maxAppliedVersion > maxBinaryVersion {
		return fmt.Errorf("database schema version %d is newer than supported binary version %d", maxAppliedVersion, maxBinaryVersion)
	}

	// 4. Validate existing applied migrations against embedded checksums (fail closed on mismatch)
	for _, m := range migrations {
		if storedChecksum, ok := applied[m.version]; ok {
			if storedChecksum != m.checksum {
				return fmt.Errorf("migration %d checksum mismatch: stored %s, current %s", m.version, storedChecksum, m.checksum)
			}
		}
	}

	// 5. Apply missing migrations in explicit transactions
	nowMs := time.Now().UTC().UnixMilli()
	for _, m := range migrations {
		if _, ok := applied[m.version]; ok {
			continue
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin migration transaction for %d: %w", m.version, err)
		}

		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed executing migration %d (%s): %w", m.version, m.name, err)
		}

		insertSQL := "INSERT INTO schema_migrations (version, applied_at, checksum) VALUES (?, ?, ?)"
		if _, err := tx.ExecContext(ctx, insertSQL, m.version, nowMs, m.checksum); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed recording applied migration %d: %w", m.version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed committing migration %d: %w", m.version, err)
		}
	}

	return nil
}
