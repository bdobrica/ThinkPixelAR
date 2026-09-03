package migrations

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestUpFromEmptyPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("THINKPIXELAR_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("THINKPIXELAR_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	schema := temporarySchema(t)
	if _, err = db.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create empty schema: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, cleanupErr := db.ExecContext(cleanupCtx, `DROP SCHEMA `+schema+` CASCADE`); cleanupErr != nil {
			t.Errorf("drop temporary schema: %v", cleanupErr)
		}
	}()
	if _, err = db.ExecContext(ctx, `SET search_path TO `+schema); err != nil {
		t.Fatalf("select empty schema: %v", err)
	}

	assertTableNames(t, ctx, db, nil)
	if err = Up(ctx, db); err != nil {
		t.Fatalf("migrate from empty: %v", err)
	}
	wantTables := []string{
		"attempts", "checkpoints", "cleanup_intents", "executions", "harness_bindings",
		"idempotency_records", "outbox_messages", "reconciliation_work", "runtime_event_streams",
		"runtime_events", "runtime_profile_resolution_snapshots", "sandbox_bindings", "schema_migrations",
		"sessions", "tenants", "workspace_generations", "workspaces",
	}
	assertTableNames(t, ctx, db, wantTables)
	firstLedger := readLedger(t, ctx, db)
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(firstLedger) != len(loaded) {
		t.Fatalf("migration ledger entries = %d, want %d", len(firstLedger), len(loaded))
	}
	for i, migration := range loaded {
		got := firstLedger[i]
		if got.Version != migration.Version || got.Name != migration.Name || got.Checksum != migration.Checksum || got.ToolVersion != toolVersion {
			t.Fatalf("migration ledger entry %d = %#v, want version=%d name=%q checksum=%q tool=%q", i, got, migration.Version, migration.Name, migration.Checksum, toolVersion)
		}
	}

	if err = Up(ctx, db); err != nil {
		t.Fatalf("reapply current migrations: %v", err)
	}
	if secondLedger := readLedger(t, ctx, db); !reflect.DeepEqual(secondLedger, firstLedger) {
		t.Fatalf("reapply changed migration ledger\nfirst:  %#v\nsecond: %#v", firstLedger, secondLedger)
	}
}

type ledgerEntry struct {
	Version     int64
	Name        string
	Checksum    string
	AppliedAt   time.Time
	ToolVersion string
}

func readLedger(t *testing.T, ctx context.Context, db *sql.DB) []ledgerEntry {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT version, name, checksum, applied_at, tool_version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var entries []ledgerEntry
	for rows.Next() {
		var entry ledgerEntry
		if err = rows.Scan(&entry.Version, &entry.Name, &entry.Checksum, &entry.AppliedAt, &entry.ToolVersion); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return entries
}

func assertTableNames(t *testing.T, ctx context.Context, db *sql.DB, want []string) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = current_schema() ORDER BY tablename`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tables = %v, want %v", got, want)
	}
}

func temporarySchema(t *testing.T) string {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`"migration_test_%s"`, hex.EncodeToString(suffix[:]))
}
