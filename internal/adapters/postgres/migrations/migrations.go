// Package migrations owns the PostgreSQL schema migration mechanism.
package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	toolVersion = "thinkpixelar-migrate/v1"
	lockKey     = int64(0x54504152) // "TPAR"
)

//go:embed sql/*.sql
var migrationFiles embed.FS

// Migration is one immutable, ordered schema change.
type Migration struct {
	Version  int64
	Name     string
	SQL      string
	Checksum string
}

// Load returns the migrations embedded in the migrate binary.
func Load() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "sql")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	migrations := make([]Migration, 0, len(entries))
	seen := make(map[int64]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, name, err := parseFilename(entry.Name())
		if err != nil {
			return nil, err
		}
		if _, exists := seen[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %d", version)
		}
		seen[version] = struct{}{}
		contents, err := migrationFiles.ReadFile(path.Join("sql", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		digest := sha256.Sum256(contents)
		migrations = append(migrations, Migration{
			Version: version, Name: name, SQL: string(contents), Checksum: hex.EncodeToString(digest[:]),
		})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	if len(migrations) == 0 {
		return nil, errors.New("no embedded migrations")
	}
	return migrations, nil
}

func parseFilename(filename string) (int64, string, error) {
	stem := strings.TrimSuffix(filename, ".sql")
	parts := strings.SplitN(stem, "_", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, "", fmt.Errorf("invalid migration filename %q", filename)
	}
	version, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || version <= 0 {
		return 0, "", fmt.Errorf("invalid migration version in %q", filename)
	}
	return version, parts[1], nil
}

// Up verifies applied checksums and applies every pending migration.
func Up(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("migration database is required")
	}
	migrations, err := Load()
	if err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, lockKey) }()

	if _, err := conn.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version bigint PRIMARY KEY CHECK (version > 0),
    name text NOT NULL CHECK (name <> ''),
    checksum character(64) NOT NULL CHECK (checksum ~ '^[0-9a-f]{64}$'),
    applied_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    tool_version text NOT NULL CHECK (tool_version <> '')
)`); err != nil {
		return fmt.Errorf("ensure migration ledger: %w", err)
	}

	applied, err := appliedMigrations(ctx, conn)
	if err != nil {
		return err
	}
	known := make(map[int64]Migration, len(migrations))
	for _, migration := range migrations {
		known[migration.Version] = migration
		if checksum, ok := applied[migration.Version]; ok && checksum != migration.Checksum {
			return fmt.Errorf("migration %d checksum mismatch", migration.Version)
		}
	}
	for version := range applied {
		if _, ok := known[version]; !ok {
			return fmt.Errorf("database contains unknown migration version %d", version)
		}
	}

	for _, migration := range migrations {
		if _, ok := applied[migration.Version]; ok {
			continue
		}
		if err := apply(ctx, conn, migration); err != nil {
			return err
		}
	}
	return nil
}

func appliedMigrations(ctx context.Context, conn *sql.Conn) (map[int64]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read migration ledger: %w", err)
	}
	defer rows.Close()
	applied := make(map[int64]string)
	for rows.Next() {
		var version int64
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("scan migration ledger: %w", err)
		}
		applied[version] = strings.TrimSpace(checksum)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate migration ledger: %w", err)
	}
	return applied, nil
}

func apply(ctx context.Context, conn *sql.Conn, migration Migration) error {
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", migration.Version, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
		return fmt.Errorf("apply migration %d: %w", migration.Version, err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO schema_migrations (version, name, checksum, applied_at, tool_version)
VALUES ($1, $2, $3, $4, $5)`, migration.Version, migration.Name, migration.Checksum, time.Now().UTC(), toolVersion); err != nil {
		return fmt.Errorf("record migration %d: %w", migration.Version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", migration.Version, err)
	}
	return nil
}
