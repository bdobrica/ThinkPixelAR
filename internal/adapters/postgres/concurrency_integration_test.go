package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/attempt"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/idempotency"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/reconciliation"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestStoreConcurrencyInvariants(t *testing.T) {
	databaseURL := os.Getenv("THINKPIXELAR_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("THINKPIXELAR_TEST_DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := postgres.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)

	t.Run("active Execution uniqueness", func(t *testing.T) {
		ids := concurrencyIDs(t, now, 6)
		seedConcurrencySession(t, db, ids[0], ids[1], now, false)
		values := make([]*execution.Execution, 2)
		for i := range values {
			values[i], err = execution.New(ids[0], ids[2+i], concurrencyExecutionBinding(ids[1], uint64(i+1), "active-uniqueness-"+string(rune('a'+i))), now.Add(time.Hour), now)
			if err != nil {
				t.Fatal(err)
			}
		}
		results := concurrently(2, func(i int) error {
			return store.WithinTransaction(context.Background(), ids[0], func(ctx context.Context, repos persistence.Repositories) error {
				return repos.Executions().Add(ctx, values[i])
			})
		})
		assertOneWinner(t, results)
		var count int
		if err = db.QueryRow(`SELECT count(*) FROM executions WHERE tenant_id=$1 AND session_id=$2`, ids[0], ids[1]).Scan(&count); err != nil || count != 1 {
			t.Fatalf("active executions = %d, error = %v", count, err)
		}
	})

	t.Run("Attempt optimistic fence", func(t *testing.T) {
		ids := concurrencyIDs(t, now.Add(time.Second), 8)
		seedConcurrencySession(t, db, ids[0], ids[1], now, true)
		execValue, buildErr := execution.New(ids[0], ids[2], concurrencyExecutionBinding(ids[1], 1, "attempt-fence"), now.Add(time.Hour), now)
		if buildErr == nil {
			buildErr = execValue.Transition(execution.Materializing, 0, nil, now)
		}
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		if err = store.WithinTransaction(context.Background(), ids[0], func(ctx context.Context, repos persistence.Repositories) error {
			return repos.Executions().Add(ctx, execValue)
		}); err != nil {
			t.Fatal(err)
		}
		bindCurrentExecution(t, db, ids[0], ids[1], ids[2])
		value, buildErr := attempt.New(ids[0], ids[3], attempt.Binding{ExecutionID: ids[2], ExecutionGeneration: 1, Number: 1}, now)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		if err = store.WithinTransaction(context.Background(), ids[0], func(ctx context.Context, repos persistence.Repositories) error {
			return repos.Attempts().Add(ctx, value)
		}); err != nil {
			t.Fatal(err)
		}
		copies := make([]*attempt.Attempt, 2)
		for i := range copies {
			if err = store.WithinTransaction(context.Background(), ids[0], func(ctx context.Context, repos persistence.Repositories) error {
				copies[i], err = repos.Attempts().Get(ctx, ids[3])
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err = copies[i].Transition(attempt.Acquiring, 0, nil, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
		}
		results := concurrently(2, func(i int) error {
			return store.WithinTransaction(context.Background(), ids[0], func(ctx context.Context, repos persistence.Repositories) error {
				return repos.Attempts().Update(ctx, copies[i], 0)
			})
		})
		assertOneWinner(t, results)
		if !errors.Is(results[0], persistence.ErrConflict) && !errors.Is(results[1], persistence.ErrConflict) {
			t.Fatalf("losing update did not report a fenced conflict: %v", results)
		}
	})

	t.Run("RuntimeEvent sequence allocation", func(t *testing.T) {
		ids := concurrencyIDs(t, now.Add(2*time.Second), 5)
		seedConcurrencySession(t, db, ids[0], ids[1], now, false)
		events := make([]*runtimeevent.Event, 2)
		for i := range events {
			events[i], err = runtimeevent.New(ids[2+i], ids[0], ids[1], "", "", uint64(i+1), 1, "session.created", now, now, runtimeevent.SourceAgentRuntime, runtimeevent.Internal, []byte(`{"state":"PROVISIONING"}`), runtimeevent.Correlation{}, "runtime", nil)
			if err != nil {
				t.Fatal(err)
			}
		}
		firstInserted := make(chan struct{})
		secondStarted := make(chan struct{})
		releaseFirst := make(chan struct{})
		results := make(chan error, 2)
		go func() {
			results <- store.WithinTransaction(context.Background(), ids[0], func(ctx context.Context, repos persistence.Repositories) error {
				if appendErr := repos.RuntimeEvents().Append(ctx, events[0]); appendErr != nil {
					return appendErr
				}
				close(firstInserted)
				<-releaseFirst
				return nil
			})
		}()
		<-firstInserted
		go func() {
			results <- store.WithinTransaction(context.Background(), ids[0], func(ctx context.Context, repos persistence.Repositories) error {
				close(secondStarted)
				return repos.RuntimeEvents().Append(ctx, events[1])
			})
		}()
		<-secondStarted
		close(releaseFirst)
		for range 2 {
			if result := <-results; result != nil {
				t.Fatal(result)
			}
		}
		var sequences []int64
		rows, queryErr := db.Query(`SELECT sequence FROM runtime_events WHERE tenant_id=$1 AND session_id=$2 ORDER BY sequence`, ids[0], ids[1])
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		defer rows.Close()
		for rows.Next() {
			var sequence int64
			if scanErr := rows.Scan(&sequence); scanErr != nil {
				t.Fatal(scanErr)
			}
			sequences = append(sequences, sequence)
		}
		if err = rows.Err(); err != nil || len(sequences) != 2 || sequences[0] != 1 || sequences[1] != 2 {
			t.Fatalf("sequences = %v, error = %v", sequences, err)
		}
	})

	t.Run("idempotency reservation race", func(t *testing.T) {
		ids := concurrencyIDs(t, now.Add(3*time.Second), 10)
		seedTenant(t, db, ids[0])
		scope := idempotency.Scope{PrincipalDigest: testDigest('a'), Action: "POST:/v1/sessions", KeyDigest: testDigest('b')}
		records := make([]*idempotency.Record, 2)
		for i := range records {
			records[i], err = idempotency.New(ids[0], ids[1+i], scope, "http-v1", testDigest('c'), ids[3+i], "", ids[5+i], ids[7+i], now.Add(time.Minute), now.Add(time.Hour), now)
			if err != nil {
				t.Fatal(err)
			}
		}
		type reservation struct {
			id      primitives.ID
			created bool
			err     error
		}
		start := make(chan struct{})
		results := make(chan reservation, 2)
		for i := range records {
			go func(record *idempotency.Record) {
				<-start
				var result reservation
				result.err = store.WithinTransaction(context.Background(), ids[0], func(ctx context.Context, repos persistence.Repositories) error {
					got, created, reserveErr := repos.Idempotency().Reserve(ctx, record)
					if reserveErr == nil {
						result.id, result.created = got.ID(), created
					}
					return reserveErr
				})
				results <- result
			}(records[i])
		}
		close(start)
		got := []reservation{<-results, <-results}
		if got[0].err != nil || got[1].err != nil || got[0].id != got[1].id || got[0].created == got[1].created {
			t.Fatalf("reservations = %#v", got)
		}
	})

	t.Run("reconciliation claim race", func(t *testing.T) {
		ids := concurrencyIDs(t, now.Add(4*time.Second), 6)
		seedTenant(t, db, ids[0])
		work, buildErr := reconciliation.New(ids[0], ids[1], "session.reconcile", "session", ids[2], now, now)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		if err = store.WithinTransaction(context.Background(), ids[0], func(ctx context.Context, repos persistence.Repositories) error {
			return repos.Reconciliation().Add(ctx, work)
		}); err != nil {
			t.Fatal(err)
		}
		claimed := make([][]*reconciliation.Work, 2)
		results := concurrently(2, func(i int) error {
			return store.WithinTransaction(context.Background(), ids[0], func(ctx context.Context, repos persistence.Repositories) error {
				var claimErr error
				claimed[i], claimErr = repos.Reconciliation().ClaimAvailable(ctx, ids[3+i], now, now.Add(time.Minute), 1)
				return claimErr
			})
		})
		if results[0] != nil || results[1] != nil || len(claimed[0])+len(claimed[1]) != 1 {
			t.Fatalf("claims = %d/%d, errors = %v", len(claimed[0]), len(claimed[1]), results)
		}
	})
}

func concurrencyIDs(t *testing.T, at time.Time, count int) []primitives.ID {
	t.Helper()
	ids := make([]primitives.ID, count)
	for i := range ids {
		var err error
		ids[i], err = primitives.NewID(at.Add(time.Duration(i) * time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func seedTenant(t *testing.T, db *sql.DB, tenantID primitives.ID) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO tenants (tenant_id) VALUES ($1)`, tenantID); err != nil {
		t.Fatal(err)
	}
}

func seedConcurrencySession(t *testing.T, db *sql.DB, tenantID, sessionID primitives.ID, now time.Time, active bool) {
	t.Helper()
	seedTenant(t, db, tenantID)
	binding := session.RuntimeBinding{AuthorityMode: "LOCAL", AuthorityNamespace: "concurrency", AgentID: "agent", AgentVersionID: "v1", RuntimeSpecSchemaVersion: "v1", RuntimeSpec: []byte(`{}`), RuntimeSpecDigest: testDigest('d'), RuntimeProfileSchemaVersion: "v1", RuntimeProfileSnapshot: []byte(`{}`), RuntimeProfileDigest: testDigest('e')}
	value, err := session.New(tenantID, sessionID, binding, now)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		if err = value.Transition(session.Ready, 0, now); err == nil {
			err = value.Transition(session.Active, 1, now)
		}
	}
	store, _ := postgres.NewStore(db)
	if err == nil {
		err = store.WithinTransaction(context.Background(), tenantID, func(ctx context.Context, repos persistence.Repositories) error {
			return repos.Sessions().Add(ctx, value)
		})
	}
	if err != nil {
		t.Fatal(err)
	}
}

func concurrencyExecutionBinding(sessionID primitives.ID, generation uint64, reference string) execution.Binding {
	return execution.Binding{SessionID: sessionID, SessionGeneration: generation, AuthorityMode: "LOCAL", AuthorityNamespace: "concurrency", AuthorityReference: reference, GrantDigest: testDigest('f'), AgentID: "agent", AgentVersionID: "v1", AgentEvidence: []byte(`{}`), AgentEvidenceDigest: testDigest('a')}
}

func bindCurrentExecution(t *testing.T, db *sql.DB, tenantID, sessionID, executionID primitives.ID) {
	t.Helper()
	if _, err := db.Exec(`UPDATE sessions SET current_execution_id=$3 WHERE tenant_id=$1 AND session_id=$2`, tenantID, sessionID, executionID); err != nil {
		t.Fatal(err)
	}
}

func concurrently(count int, work func(int) error) []error {
	start := make(chan struct{})
	results := make([]error, count)
	var wait sync.WaitGroup
	wait.Add(count)
	for i := range count {
		go func() {
			defer wait.Done()
			<-start
			results[i] = work(i)
		}()
	}
	close(start)
	wait.Wait()
	return results
}

func assertOneWinner(t *testing.T, results []error) {
	t.Helper()
	winners := 0
	for _, err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("successful transactions = %d, errors = %v", winners, results)
	}
}
