package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/idempotency"
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

func TestIdempotencyRepositoryReservesReplaysCompletesAndExpires(t *testing.T) {
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
	ids := make([]primitives.ID, 7)
	for i := range ids {
		ids[i], _ = primitives.NewID(now.Add(time.Duration(i) * time.Millisecond))
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, ids[0]); err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO tenants (tenant_id) VALUES ($1)`, ids[0])
	}
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	digestA := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	scope := idempotency.Scope{PrincipalDigest: digestA, Action: "POST:/v1/sessions", KeyDigest: digestB}
	record, err := idempotency.New(ids[0], ids[1], scope, "http-v1", digestA, ids[2], ids[3], ids[4], ids[5], now.Add(time.Minute), now.Add(time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := postgres.NewStore(db)
	err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		got, created, err := repos.Idempotency().Reserve(ctx, record)
		if err != nil {
			return err
		}
		if !created || got.OperationID() != ids[2] {
			t.Fatalf("first reservation = %v, %s", created, got.OperationID())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	replay, _ := idempotency.New(ids[0], ids[6], scope, "http-v1", digestA, ids[6], "", ids[6], ids[5], now.Add(time.Minute), now.Add(time.Hour), now)
	err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		got, created, err := repos.Idempotency().Reserve(ctx, replay)
		if err != nil {
			return err
		}
		if created || got.ID() != ids[1] || got.OperationID() != ids[2] {
			t.Fatalf("replay created=%v ID=%s operation=%s", created, got.ID(), got.OperationID())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	conflict, _ := idempotency.New(ids[0], ids[6], scope, "http-v1", digestB, ids[6], "", ids[6], ids[5], now.Add(time.Minute), now.Add(time.Hour), now)
	err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		_, _, err := repos.Idempotency().Reserve(ctx, conflict)
		return err
	})
	if !errors.Is(err, persistence.ErrRequestDigestMismatch) {
		t.Fatalf("digest conflict = %v", err)
	}
	if err = record.Succeed(idempotency.Response{HTTPStatus: 201, Payload: []byte(`{"session_id":"same"}`)}, ids[4], 1, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		return repos.Idempotency().Update(ctx, record, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		got, err := repos.Idempotency().Get(ctx, scope)
		if err != nil {
			return err
		}
		response, ok := got.Response()
		if !ok || response.HTTPStatus != 201 || string(response.Payload) != `{"session_id":"same"}` {
			t.Fatalf("response = %#v, %v", response, ok)
		}
		deleted, err := repos.Idempotency().DeleteExpired(ctx, now.Add(2*time.Hour), 10)
		if err == nil && deleted != 1 {
			t.Fatalf("deleted = %d", deleted)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
