package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/outbox"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/reconciliation"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestStoreReplaysExpiredWorkAfterRestart(t *testing.T) {
	databaseURL := os.Getenv("THINKPIXELAR_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("THINKPIXELAR_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	ids := make([]primitives.ID, 9)
	for i := range ids {
		ids[i], _ = primitives.NewID(now.Add(time.Duration(i) * time.Millisecond))
	}

	db := openReplayDatabase(t, databaseURL)
	store, err := postgres.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	createReplayTenant(t, ctx, db, ids[0])
	message, err := outbox.New(ids[0], ids[1], outbox.Envelope{
		Topic: "runtime.events", SchemaVersion: "thinkpixel.runtime-event/v1", EventID: ids[2],
		AggregateType: "session", AggregateID: ids[3], AggregateVersion: 1,
		Payload: []byte(`{"type":"session.created"}`), PayloadDigest: testDigest('a'),
	}, now, now)
	if err != nil {
		t.Fatal(err)
	}
	work, err := reconciliation.New(ids[0], ids[4], "session.reconcile", "session", ids[3], now, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		if err := repos.Outbox().Add(ctx, message); err != nil {
			return err
		}
		return repos.Reconciliation().Add(ctx, work)
	}); err != nil {
		t.Fatal(err)
	}

	claimAt := time.Now().UTC().Truncate(time.Microsecond)
	leaseUntil := claimAt.Add(2 * time.Second)
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		messages, err := repos.Outbox().ClaimAvailable(ctx, ids[5], claimAt, leaseUntil, 10)
		if err != nil || len(messages) != 1 {
			return claimResultError("outbox", len(messages), err)
		}
		workItems, err := repos.Reconciliation().ClaimAvailable(ctx, ids[5], claimAt, leaseUntil, 10)
		if err != nil || len(workItems) != 1 {
			return claimResultError("reconciliation", len(workItems), err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	// A new process/store cannot replay active leases, but can take them over
	// after expiry using the same durable identities and higher fences.
	db = openReplayDatabase(t, databaseURL)
	defer db.Close()
	store, _ = postgres.NewStore(db)
	activeAt := time.Now().UTC().Truncate(time.Microsecond)
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		messages, err := repos.Outbox().ClaimAvailable(ctx, ids[6], activeAt, activeAt.Add(time.Minute), 10)
		if err != nil || len(messages) != 0 {
			return claimResultError("active outbox", len(messages), err)
		}
		workItems, err := repos.Reconciliation().ClaimAvailable(ctx, ids[6], activeAt, activeAt.Add(time.Minute), 10)
		if err != nil || len(workItems) != 0 {
			return claimResultError("active reconciliation", len(workItems), err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if wait := time.Until(leaseUntil.Add(50 * time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	replayAt := time.Now().UTC().Truncate(time.Microsecond)
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		messages, err := repos.Outbox().ClaimAvailable(ctx, ids[7], replayAt, replayAt.Add(time.Minute), 10)
		if err != nil || len(messages) != 1 {
			return claimResultError("replayed outbox", len(messages), err)
		}
		if messages[0].ID() != ids[1] || messages[0].Envelope().EventID != ids[2] || messages[0].Attempts() != 2 || messages[0].ClaimFence() != 2 {
			t.Fatalf("replayed outbox lost identity or fence: %#v", messages[0])
		}
		if err := messages[0].MarkDelivered(ids[7], 2, replayAt.Add(time.Second)); err != nil {
			return err
		}
		if err := repos.Outbox().Update(ctx, messages[0], 2); err != nil {
			return err
		}

		workItems, err := repos.Reconciliation().ClaimAvailable(ctx, ids[7], replayAt, replayAt.Add(time.Minute), 10)
		if err != nil || len(workItems) != 1 {
			return claimResultError("replayed reconciliation", len(workItems), err)
		}
		if workItems[0].ID() != ids[4] || workItems[0].TargetID() != ids[3] || workItems[0].Attempts() != 2 || workItems[0].ClaimFence() != 2 {
			t.Fatalf("replayed reconciliation lost identity or fence: %#v", workItems[0])
		}
		if err := workItems[0].Complete(ids[7], 2, replayAt.Add(time.Second)); err != nil {
			return err
		}
		return repos.Reconciliation().Update(ctx, workItems[0], 2)
	}); err != nil {
		t.Fatal(err)
	}

	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		messages, err := repos.Outbox().ClaimAvailable(ctx, ids[8], replayAt.Add(2*time.Minute), replayAt.Add(3*time.Minute), 10)
		if err != nil || len(messages) != 0 {
			return claimResultError("terminal outbox", len(messages), err)
		}
		workItems, err := repos.Reconciliation().ClaimAvailable(ctx, ids[8], replayAt.Add(2*time.Minute), replayAt.Add(3*time.Minute), 10)
		if err != nil || len(workItems) != 0 {
			return claimResultError("terminal reconciliation", len(workItems), err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func openReplayDatabase(t *testing.T, databaseURL string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Ping(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}

func createReplayTenant(t *testing.T, ctx context.Context, db *sql.DB, tenantID primitives.ID) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, tenantID); err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO tenants (tenant_id) VALUES ($1)`, tenantID)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func claimResultError(kind string, count int, err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("%s claim count = %d", kind, count)
}
