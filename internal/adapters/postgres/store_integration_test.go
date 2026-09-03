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
	"github.com/bdobrica/ThinkPixelAR/internal/domain/outbox"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/reconciliation"
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

func TestReconciliationRepositoryClaimsAndRejectsStaleWorker(t *testing.T) {
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
	ids := make([]primitives.ID, 5)
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
	work, err := reconciliation.New(ids[0], ids[1], "session.reconcile", "session", ids[2], now, now)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := postgres.NewStore(db)
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		return repos.Reconciliation().Add(ctx, work)
	}); err != nil {
		t.Fatal(err)
	}
	var stale *reconciliation.Work
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		claimed, err := repos.Reconciliation().ClaimAvailable(ctx, ids[3], now, now.Add(time.Minute), 10)
		if err != nil {
			return err
		}
		if len(claimed) != 1 || claimed[0].ClaimFence() != 1 {
			t.Fatalf("first claim = %#v", claimed)
		}
		stale = claimed[0]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		claimed, err := repos.Reconciliation().ClaimAvailable(ctx, ids[4], now.Add(time.Minute), now.Add(2*time.Minute), 10)
		if err != nil {
			return err
		}
		if len(claimed) != 1 || claimed[0].ClaimFence() != 2 || claimed[0].Attempts() != 2 {
			t.Fatalf("takeover = %#v", claimed)
		}
		if err = claimed[0].Complete(ids[4], 2, now.Add(90*time.Second)); err != nil {
			return err
		}
		return repos.Reconciliation().Update(ctx, claimed[0], 2)
	}); err != nil {
		t.Fatal(err)
	}
	if err = stale.Reschedule(ids[3], 1, now.Add(3*time.Minute), "STALE", now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		return repos.Reconciliation().Update(ctx, stale, 1)
	})
	if !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("stale update = %v", err)
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

func TestOutboxRepositoryClaimsRetriesAndDeliversWithStableIdentity(t *testing.T) {
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

	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	message, err := outbox.New(ids[0], ids[1], outbox.Envelope{Topic: "runtime.events", SchemaVersion: "thinkpixel.runtime-event/v1", EventID: ids[2], AggregateType: "session", AggregateID: ids[3], AggregateVersion: 2, Payload: []byte(`{"type":"session.created"}`), PayloadDigest: digest}, now, now)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := postgres.NewStore(db)
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		return repos.Outbox().Add(ctx, message)
	}); err != nil {
		t.Fatal(err)
	}
	duplicate, _ := outbox.New(ids[0], ids[6], message.Envelope(), now, now)
	err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		return repos.Outbox().Add(ctx, duplicate)
	})
	if !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("duplicate semantic event error = %v", err)
	}

	var claimed *outbox.Message
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		got, err := repos.Outbox().ClaimAvailable(ctx, ids[4], now, now.Add(time.Minute), 10)
		if err != nil {
			return err
		}
		if len(got) != 1 || got[0].ID() != ids[1] || got[0].Envelope().EventID != ids[2] {
			t.Fatalf("claimed = %#v", got)
		}
		claimed = got[0]
		if err = claimed.Retry(ids[4], 1, now.Add(2*time.Minute), "UPSTREAM_UNAVAILABLE", now.Add(time.Second)); err != nil {
			return err
		}
		return repos.Outbox().Update(ctx, claimed, 1)
	}); err != nil {
		t.Fatal(err)
	}

	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		got, err := repos.Outbox().ClaimAvailable(ctx, ids[5], now.Add(2*time.Minute), now.Add(3*time.Minute), 10)
		if err != nil {
			return err
		}
		if len(got) != 1 || got[0].ClaimFence() != 2 || got[0].Attempts() != 2 {
			t.Fatalf("reclaimed = %#v", got)
		}
		if err = got[0].MarkDelivered(ids[5], 2, now.Add(150*time.Second)); err != nil {
			return err
		}
		return repos.Outbox().Update(ctx, got[0], 2)
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		got, err := repos.Outbox().Get(ctx, ids[1])
		if err != nil {
			return err
		}
		if got.State() != outbox.Delivered || got.ID() != ids[1] || got.Envelope().EventID != ids[2] {
			t.Fatalf("delivered = %#v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
