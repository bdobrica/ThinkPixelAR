package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/attempt"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/cleanup"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/idempotency"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/outbox"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/reconciliation"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestCleanupRepositoryRetainsExactRetryableTombstone(t *testing.T) {
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
	ids := make([]primitives.ID, 4)
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
	target := cleanup.Target{OwnerType: "sandbox-binding", OwnerID: ids[2], TargetType: "sandbox", ProviderKind: "agentsandbox", ExternalReference: "clusters/a/sandboxes/exact", OperationID: ids[3], RequestDigest: testDigest('a'), OwnershipProofDigest: testDigest('b'), Orphan: true}
	intent, err := cleanup.New(ids[0], ids[1], target, now, now)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := postgres.NewStore(db)
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		return repos.Cleanup().Add(ctx, intent)
	}); err != nil {
		t.Fatal(err)
	}
	if err = intent.Retry(0, now.Add(time.Minute), "TIMEOUT", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		return repos.Cleanup().Update(ctx, intent, 0)
	}); err != nil {
		t.Fatal(err)
	}
	if err = intent.Confirm(1, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		return repos.Cleanup().Update(ctx, intent, 1)
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		got, err := repos.Cleanup().Get(ctx, ids[1])
		if err != nil {
			return err
		}
		if got.State() != cleanup.Confirmed || got.Target() != target || got.Attempts() != 2 {
			t.Fatalf("tombstone = %#v", got)
		}
		due, err := repos.Cleanup().ListDue(ctx, now.Add(time.Hour), 10)
		if err == nil && len(due) != 0 {
			t.Fatalf("terminal intent listed: %#v", due)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func testDigest(c byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return "sha256:" + string(b)
}

func TestStoreIsolatesEveryRepositoryByTenant(t *testing.T) {
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
	ids := make([]primitives.ID, 12)
	for i := range ids {
		ids[i], _ = primitives.NewID(now.Add(time.Duration(i) * time.Millisecond))
	}
	tenants := []primitives.ID{ids[0], ids[1]}
	for _, tenantID := range tenants {
		tx, beginErr := db.BeginTx(ctx, nil)
		if beginErr != nil {
			t.Fatal(beginErr)
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
	}

	type tenantRecords struct {
		session        *session.Session
		execution      *execution.Execution
		attempt        *attempt.Attempt
		event          *runtimeevent.Event
		idempotency    *idempotency.Record
		outbox         *outbox.Message
		reconciliation *reconciliation.Work
		cleanup        *cleanup.Intent
	}
	build := func(tenantID primitives.ID, marker byte) tenantRecords {
		binding := session.RuntimeBinding{AuthorityMode: "LOCAL", AuthorityNamespace: "tenant-isolation", AgentID: "agent", AgentVersionID: "v1", RuntimeSpecSchemaVersion: "v1", RuntimeSpec: []byte(`{}`), RuntimeSpecDigest: testDigest(marker), RuntimeProfileSchemaVersion: "v1", RuntimeProfileSnapshot: []byte(`{}`), RuntimeProfileDigest: testDigest(marker)}
		sess, buildErr := session.New(tenantID, ids[2], binding, now)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		if buildErr = sess.Transition(session.Ready, 0, now); buildErr == nil {
			buildErr = sess.Transition(session.Active, 1, now)
		}
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		exec, buildErr := execution.New(tenantID, ids[3], execution.Binding{SessionID: ids[2], SessionGeneration: 1, AuthorityMode: "LOCAL", AuthorityNamespace: "tenant-isolation", AuthorityReference: "shared-authority-reference", GrantDigest: testDigest(marker), AgentID: "agent", AgentVersionID: "v1", AgentEvidence: []byte(`{}`), AgentEvidenceDigest: testDigest(marker)}, now.Add(time.Hour), now)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		if buildErr = exec.Transition(execution.Materializing, 0, nil, now); buildErr != nil {
			t.Fatal(buildErr)
		}
		att, buildErr := attempt.New(tenantID, ids[4], attempt.Binding{ExecutionID: ids[3], ExecutionGeneration: 1, Number: 1}, now)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		event, buildErr := runtimeevent.New(ids[5], tenantID, ids[2], ids[3], ids[4], 1, 1, "attempt.started", now, now, runtimeevent.SourceAgentRuntime, runtimeevent.Internal, []byte(`{"state":"PENDING"}`), runtimeevent.Correlation{}, "attempt", nil)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		scope := idempotency.Scope{PrincipalDigest: testDigest('c'), Action: "POST:/v1/executions", KeyDigest: testDigest('d')}
		idem, buildErr := idempotency.New(tenantID, ids[6], scope, "http-v1", testDigest(marker), ids[7], ids[3], ids[8], ids[9], now.Add(time.Minute), now.Add(time.Hour), now)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		message, buildErr := outbox.New(tenantID, ids[10], outbox.Envelope{Topic: "runtime.events", SchemaVersion: runtimeevent.SchemaVersion, EventID: ids[5], AggregateType: "attempt", AggregateID: ids[4], AggregateVersion: 1, Payload: []byte(`{"type":"attempt.started"}`), PayloadDigest: testDigest(marker)}, now, now)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		work, buildErr := reconciliation.New(tenantID, ids[11], "attempt.reconcile", "attempt", ids[4], now, now)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		intent, buildErr := cleanup.New(tenantID, ids[7], cleanup.Target{OwnerType: "attempt", OwnerID: ids[4], TargetType: "sandbox", ProviderKind: "agentsandbox", ExternalReference: "shared-sandbox-reference", OperationID: ids[8], RequestDigest: testDigest(marker), OwnershipProofDigest: testDigest(marker), Orphan: true}, now, now)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		return tenantRecords{sess, exec, att, event, idem, message, work, intent}
	}
	records := []tenantRecords{build(tenants[0], 'a'), build(tenants[1], 'b')}
	store, err := postgres.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	for i, tenantID := range tenants {
		r := records[i]
		if err = store.WithinTransaction(ctx, tenantID, func(ctx context.Context, repos persistence.Repositories) error {
			if err := repos.Sessions().Add(ctx, r.session); err != nil {
				return err
			}
			return repos.Executions().Add(ctx, r.execution)
		}); err != nil {
			t.Fatalf("seed tenant %s aggregate: %v", tenantID, err)
		}
		tx, beginErr := db.BeginTx(ctx, nil)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, tenantID); err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE sessions SET current_execution_id=$2 WHERE tenant_id=$1 AND session_id=$3`, tenantID, ids[3], ids[2])
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			t.Fatalf("bind tenant %s current execution: %v", tenantID, err)
		}
		if err = store.WithinTransaction(ctx, tenantID, func(ctx context.Context, repos persistence.Repositories) error {
			if err := repos.Attempts().Add(ctx, r.attempt); err != nil {
				return err
			}
			if err := repos.RuntimeEvents().Append(ctx, r.event); err != nil {
				return err
			}
			if _, created, err := repos.Idempotency().Reserve(ctx, r.idempotency); err != nil || !created {
				if err != nil {
					return err
				}
				return errors.New("idempotency record was not created")
			}
			if err := repos.Outbox().Add(ctx, r.outbox); err != nil {
				return err
			}
			if err := repos.Reconciliation().Add(ctx, r.reconciliation); err != nil {
				return err
			}
			return repos.Cleanup().Add(ctx, r.cleanup)
		}); err != nil {
			t.Fatalf("seed tenant %s repositories: %v", tenantID, err)
		}
	}

	for i, tenantID := range tenants {
		if err = store.WithinTransaction(ctx, tenantID, func(ctx context.Context, repos persistence.Repositories) error {
			checks := []struct {
				name string
				get  func() (primitives.ID, error)
			}{
				{"Sessions", func() (primitives.ID, error) {
					v, err := repos.Sessions().Get(ctx, ids[2])
					if err != nil {
						return "", err
					}
					return v.TenantID(), nil
				}},
				{"Executions", func() (primitives.ID, error) {
					v, err := repos.Executions().Get(ctx, ids[3])
					if err != nil {
						return "", err
					}
					return v.TenantID(), nil
				}},
				{"Attempts", func() (primitives.ID, error) {
					v, err := repos.Attempts().Get(ctx, ids[4])
					if err != nil {
						return "", err
					}
					return v.TenantID(), nil
				}},
				{"Idempotency", func() (primitives.ID, error) {
					v, err := repos.Idempotency().Get(ctx, records[i].idempotency.Scope())
					if err != nil {
						return "", err
					}
					return v.TenantID(), nil
				}},
				{"Outbox", func() (primitives.ID, error) {
					v, err := repos.Outbox().Get(ctx, ids[10])
					if err != nil {
						return "", err
					}
					return v.TenantID(), nil
				}},
				{"Reconciliation", func() (primitives.ID, error) {
					v, err := repos.Reconciliation().Get(ctx, ids[11])
					if err != nil {
						return "", err
					}
					return v.TenantID(), nil
				}},
				{"Cleanup", func() (primitives.ID, error) {
					v, err := repos.Cleanup().Get(ctx, ids[7])
					if err != nil {
						return "", err
					}
					return v.TenantID(), nil
				}},
			}
			for _, check := range checks {
				got, getErr := check.get()
				if getErr != nil {
					return fmt.Errorf("%s: %w", check.name, getErr)
				}
				if got != tenantID {
					return fmt.Errorf("%s returned tenant %s", check.name, got)
				}
			}
			claimedOutbox, claimErr := repos.Outbox().ClaimAvailable(ctx, ids[8], now, now.Add(time.Minute), 10)
			if claimErr != nil || len(claimedOutbox) != 1 || claimedOutbox[0].TenantID() != tenantID {
				return fmt.Errorf("Outbox claim tenant isolation: %v, %#v", claimErr, claimedOutbox)
			}
			claimedWork, claimErr := repos.Reconciliation().ClaimAvailable(ctx, ids[8], now, now.Add(time.Minute), 10)
			if claimErr != nil || len(claimedWork) != 1 || claimedWork[0].TenantID() != tenantID {
				return fmt.Errorf("Reconciliation claim tenant isolation: %v, %#v", claimErr, claimedWork)
			}
			due, listErr := repos.Cleanup().ListDue(ctx, now, 10)
			if listErr != nil || len(due) != 1 || due[0].TenantID() != tenantID {
				return fmt.Errorf("Cleanup list tenant isolation: %v, %#v", listErr, due)
			}
			return nil
		}); err != nil {
			t.Fatalf("read tenant %s: %v", tenantID, err)
		}
	}

	if err = store.WithinTransaction(ctx, tenants[0], func(ctx context.Context, repos persistence.Repositories) error {
		foreign := records[1]
		operations := []struct {
			name string
			run  func() error
		}{
			{"Sessions", func() error { return repos.Sessions().Add(ctx, foreign.session) }},
			{"Executions", func() error { return repos.Executions().Add(ctx, foreign.execution) }},
			{"Attempts", func() error { return repos.Attempts().Add(ctx, foreign.attempt) }},
			{"RuntimeEvents", func() error { return repos.RuntimeEvents().Append(ctx, foreign.event) }},
			{"Idempotency", func() error { _, _, err := repos.Idempotency().Reserve(ctx, foreign.idempotency); return err }},
			{"Outbox", func() error { return repos.Outbox().Add(ctx, foreign.outbox) }},
			{"Reconciliation", func() error { return repos.Reconciliation().Add(ctx, foreign.reconciliation) }},
			{"Cleanup", func() error { return repos.Cleanup().Add(ctx, foreign.cleanup) }},
		}
		for _, operation := range operations {
			if operation.run() == nil {
				return fmt.Errorf("%s accepted another tenant's aggregate", operation.name)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, tenantID := range tenants {
		var count int
		tx, beginErr := db.BeginTx(ctx, nil)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, tenantID); err == nil {
			err = tx.QueryRowContext(ctx, `SELECT count(*) FROM runtime_events WHERE tenant_id=$1 AND event_id=$2`, tenantID, ids[5]).Scan(&count)
		}
		_ = tx.Rollback()
		if err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("RuntimeEvents visible to tenant %s = %d", tenantID, count)
		}
	}
}

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

func TestStoreRollsBackExternalReferenceReservationBeforeCommit(t *testing.T) {
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

	sessionBinding := session.RuntimeBinding{AuthorityMode: "LOCAL", AuthorityNamespace: "test", AgentID: "agent", AgentVersionID: "v1", RuntimeSpecSchemaVersion: "v1", RuntimeSpec: []byte(`{}`), RuntimeSpecDigest: testDigest('a'), RuntimeProfileSchemaVersion: "v1", RuntimeProfileSnapshot: []byte(`{}`), RuntimeProfileDigest: testDigest('b')}
	sess, err := session.New(ids[0], ids[1], sessionBinding, now)
	if err != nil {
		t.Fatal(err)
	}
	executionBinding := execution.Binding{SessionID: ids[1], SessionGeneration: 1, AuthorityMode: "THINKPIXEL_AG", AuthorityNamespace: "test", AuthorityReference: "run:reserved-before-commit", ExternalRunID: "external-run", GrantDigest: testDigest('c'), AgentID: "agent", AgentVersionID: "v1", AgentEvidence: []byte(`{}`), AgentEvidenceDigest: testDigest('d')}
	reserved, err := execution.New(ids[0], ids[2], executionBinding, now.Add(time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	event, err := runtimeevent.New(ids[3], ids[0], ids[1], ids[2], "", 1, 1, "execution.accepted", now, now, runtimeevent.SourceRunAuthority, runtimeevent.Internal, []byte(`{"state":"QUEUED"}`), runtimeevent.Correlation{}, "execution", nil)
	if err != nil {
		t.Fatal(err)
	}
	message, err := outbox.New(ids[0], ids[4], outbox.Envelope{Topic: "runtime.events", SchemaVersion: runtimeevent.SchemaVersion, EventID: ids[3], AggregateType: "execution", AggregateID: ids[2], AggregateVersion: 1, Payload: []byte(`{"type":"execution.accepted"}`), PayloadDigest: testDigest('e')}, now, now)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := postgres.NewStore(db)
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		return repos.Sessions().Add(ctx, sess)
	}); err != nil {
		t.Fatal(err)
	}

	wantRollback := errors.New("forced failure after external-reference reservation")
	err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		if err := repos.Executions().Add(ctx, reserved); err != nil {
			return err
		}
		if err := repos.RuntimeEvents().Append(ctx, event); err != nil {
			return err
		}
		if err := repos.Outbox().Add(ctx, message); err != nil {
			return err
		}
		return wantRollback
	})
	if !errors.Is(err, wantRollback) {
		t.Fatalf("rollback error = %v", err)
	}

	err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		if _, err := repos.Executions().Get(ctx, ids[2]); !errors.Is(err, persistence.ErrNotFound) {
			return errors.New("rolled-back Execution remained visible")
		}
		if _, err := repos.Outbox().Get(ctx, ids[4]); !errors.Is(err, persistence.ErrNotFound) {
			return errors.New("rolled-back OutboxMessage remained visible")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var eventCount int
	check, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = check.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, ids[0]); err == nil {
		err = check.QueryRowContext(ctx, `SELECT count(*) FROM runtime_events WHERE tenant_id=$1 AND event_id=$2`, ids[0], ids[3]).Scan(&eventCount)
	}
	if rollbackErr := check.Rollback(); err == nil {
		err = rollbackErr
	}
	if err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 {
		t.Fatalf("rolled-back RuntimeEvent count = %d", eventCount)
	}

	replacement, err := execution.New(ids[0], ids[5], executionBinding, now.Add(time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		return repos.Executions().Add(ctx, replacement)
	}); err != nil {
		t.Fatalf("reuse rolled-back authority reference: %v", err)
	}
	if err = store.WithinTransaction(ctx, ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		got, err := repos.Executions().Get(ctx, ids[5])
		if err == nil && got.Binding().AuthorityReference != executionBinding.AuthorityReference {
			t.Fatalf("authority reference = %q", got.Binding().AuthorityReference)
		}
		return err
	}); err != nil {
		t.Fatal(err)
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
