package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/attempt"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestAttemptFenceSerializesLifecycleChange(t *testing.T) {
	for _, parent := range []string{"Session", "Execution"} {
		t.Run(parent, func(t *testing.T) {
			db, r := sandboxDatabaseFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			var blocker int
			if err = tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&blocker); err != nil {
				t.Fatal(err)
			}
			if parent == "Session" {
				_, err = tx.ExecContext(ctx, `UPDATE sessions SET state='DEGRADED',recovery_state='ACTIVE',state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND session_id=$2`, r.Scope.TenantID, r.Scope.SessionID)
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE executions SET state='FAILED',state_version=state_version+1,terminal_result_reference='fixture',terminal_result_digest=$3,terminal_at=clock_timestamp(),updated_at=clock_timestamp() WHERE tenant_id=$1 AND execution_id=$2`, r.Scope.TenantID, r.Scope.ExecutionID, testDigest('a'))
			}
			if err != nil {
				t.Fatal(err)
			}
			// Bypass the repository's parent locks: the trigger itself must protect
			// this writer from the concurrently changing, uncommitted parent.
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			var writer int
			if err = conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&writer); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				_, e := conn.ExecContext(ctx, `UPDATE attempts SET state_version=state_version+1,sandbox_heartbeat_at=clock_timestamp(),updated_at=clock_timestamp() WHERE tenant_id=$1 AND attempt_id=$2`, r.Scope.TenantID, r.Scope.AttemptID)
				result <- e
			}()
			// Observe an actual PostgreSQL lock wait, rather than relying on sleep
			// to guess whether the racing statement has reached its fence check.
			for {
				var blocked bool
				if err = db.QueryRowContext(ctx, `SELECT $1::int=ANY(pg_blocking_pids($2))`, blocker, writer).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case e := <-result:
					t.Fatalf("Attempt escaped parent fence before commit: %v", e)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(5 * time.Millisecond):
				}
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			var pgErr *pgconn.PgError
			if err = <-result; !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.Message != "stale attempt mutation fence" {
				t.Fatalf("stale write: %v", err)
			}
			var version int
			var untouched bool
			if err = db.QueryRowContext(ctx, `SELECT state_version,sandbox_heartbeat_at IS NULL FROM attempts WHERE tenant_id=$1 AND attempt_id=$2`, r.Scope.TenantID, r.Scope.AttemptID).Scan(&version, &untouched); err != nil || version != 0 || !untouched {
				t.Fatalf("partial write: version=%d untouched=%v err=%v", version, untouched, err)
			}
		})
	}
}

func TestConcurrentCurrentAttemptUniqueness(t *testing.T) {
	db, r := sandboxDatabaseFixture(t)
	db.SetMaxOpenConns(8)
	if _, err := db.Exec(`UPDATE attempts SET is_current=false,state_version=state_version+1 WHERE tenant_id=$1 AND attempt_id=$2`, r.Scope.TenantID, r.Scope.AttemptID); err != nil {
		t.Fatal(err)
	}
	store, err := postgres.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ids := concurrencyIDs(t, now, 32)
	results := concurrently(len(ids), func(i int) error {
		a, err := attempt.New(r.Scope.TenantID, ids[i], attempt.Binding{ExecutionID: r.Scope.ExecutionID, ExecutionGeneration: r.Scope.Generation, Number: uint64(i + 2)}, now)
		if err != nil {
			return err
		}
		return store.WithinTransaction(context.Background(), r.Scope.TenantID, func(ctx context.Context, repos persistence.Repositories) error { return repos.Attempts().Add(ctx, a) })
	})
	assertOneWinner(t, results)
	for _, err := range results {
		if err != nil && !errors.Is(err, persistence.ErrConflict) {
			t.Fatal(err)
		}
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM attempts WHERE tenant_id=$1 AND execution_id=$2 AND is_current`, r.Scope.TenantID, r.Scope.ExecutionID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("current writers=%d: %v", count, err)
	}
}
