package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestStoreCommitsAndRollsBackTenantScopedRepositories(t *testing.T) {
	databaseURL := os.Getenv("THINKPIXELAR_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("THINKPIXELAR_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Truncate(time.Microsecond)
	tenantID, _ := primitives.NewID(now)
	committedID, _ := primitives.NewID(now.Add(time.Millisecond))
	rolledBackID, _ := primitives.NewID(now.Add(2 * time.Millisecond))
	eventID, _ := primitives.NewID(now.Add(3 * time.Millisecond))
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, tenantID); err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO tenants (tenant_id) VALUES ($1)`, tenantID)
	}
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	store, err := postgres.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	binding := session.RuntimeBinding{AuthorityMode: "LOCAL", AuthorityNamespace: "test", AgentID: "agent", AgentVersionID: "v1", RuntimeSpecSchemaVersion: "v1", RuntimeSpec: []byte(`{}`), RuntimeSpecDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RuntimeProfileSchemaVersion: "v1", RuntimeProfileSnapshot: []byte(`{}`), RuntimeProfileDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	committed, err := session.New(tenantID, committedID, binding, now)
	if err != nil {
		t.Fatal(err)
	}
	event, err := runtimeevent.New(eventID, tenantID, committedID, "", "", 1, 1, "session.created", now, now,
		runtimeevent.SourceAgentRuntime, runtimeevent.Internal, []byte(`{"state":"PROVISIONING"}`), runtimeevent.Correlation{}, "session", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.WithinTransaction(ctx, tenantID, func(ctx context.Context, repositories persistence.Repositories) error {
		if err := repositories.Sessions().Add(ctx, committed); err != nil {
			return err
		}
		return repositories.RuntimeEvents().Append(ctx, event)
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.WithinTransaction(ctx, tenantID, func(ctx context.Context, repositories persistence.Repositories) error {
		got, err := repositories.Sessions().Get(ctx, committedID)
		if err != nil {
			return err
		}
		if got.ID() != committedID {
			t.Fatalf("ID = %s", got.ID())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rolledBack, _ := session.New(tenantID, rolledBackID, binding, now)
	wantRollback := errors.New("force rollback")
	err = store.WithinTransaction(ctx, tenantID, func(ctx context.Context, repositories persistence.Repositories) error {
		if err := repositories.Sessions().Add(ctx, rolledBack); err != nil {
			return err
		}
		return wantRollback
	})
	if !errors.Is(err, wantRollback) {
		t.Fatalf("rollback error = %v", err)
	}
	err = store.WithinTransaction(ctx, tenantID, func(ctx context.Context, repositories persistence.Repositories) error {
		_, err := repositories.Sessions().Get(ctx, rolledBackID)
		return err
	})
	if !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("Get rolled-back Session error = %v", err)
	}
}
