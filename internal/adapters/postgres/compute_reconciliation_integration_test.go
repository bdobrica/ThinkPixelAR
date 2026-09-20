package postgres_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
)

func TestComputeQueueRestartAndObservationFencing(t *testing.T) {
	db, r := sandboxDatabaseFixture(t)
	ctx := context.Background()
	store, _ := postgres.NewSandboxBindings(db)
	if _, err := store.Reserve(ctx, r); err != nil {
		t.Fatal(err)
	}
	intent, err := store.LoadCompute(ctx, r.Scope.TenantID, r.Scope.SandboxID)
	if err != nil || !intent.Current || intent.Operation != r.Operation {
		t.Fatal("intent", err)
	}
	observation := sandbox.ComputeObservation{State: sandbox.Provisioning, Code: "COMPUTE_PENDING"}
	if err = store.RecordCompute(ctx, intent, observation); err != nil {
		t.Fatal(err)
	}
	if err = store.RecordCompute(ctx, intent, observation); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatal("stale observation accepted", err)
	}
	ids := concurrencyIDs(t, time.Now(), 3)
	var claimed atomic.Int32
	results := concurrently(6, func(int) error {
		queue, _ := postgres.NewSandboxBindings(db)
		jobs, err := queue.ClaimCompute(ctx, r.Scope.TenantID, ids[0], time.Minute, 1)
		claimed.Add(int32(len(jobs)))
		return err
	})
	for _, err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if claimed.Load() != 1 {
		t.Fatal("duplicate claim", claimed.Load())
	}
	// Expire the retained claim without deleting or resetting the work identity.
	_, err = db.Exec(`UPDATE reconciliation_work SET claim_expires_at=updated_at+interval '1 microsecond' WHERE tenant_id=$1 AND target_id=$2`, r.Scope.TenantID, r.Scope.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	restarted, _ := postgres.NewSandboxBindings(db)
	jobs, err := restarted.ClaimCompute(ctx, r.Scope.TenantID, ids[1], time.Minute, 1)
	if err != nil || len(jobs) != 1 || jobs[0].ClaimFence() != 2 {
		t.Fatal("restart replay", jobs, err)
	}
	if err = restarted.FinishCompute(ctx, jobs[0], false, "COMPUTE_PENDING", time.Second); err != nil {
		t.Fatal(err)
	}
	if err = restarted.FinishCompute(ctx, jobs[0], true, "COMPUTE_ABSENT", time.Second); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatal("old claim finished twice", err)
	}
	other, err := restarted.ClaimCompute(ctx, ids[2], ids[1], time.Minute, 1)
	if err != nil || len(other) != 0 {
		t.Fatal("tenant leak", err)
	}
	intent, err = store.LoadCompute(ctx, r.Scope.TenantID, r.Scope.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE executions SET state='CANCELLING',state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND execution_id=$2`, r.Scope.TenantID, r.Scope.ExecutionID); err != nil {
		t.Fatal(err)
	}
	if err = store.RecordCompute(ctx, intent, observation); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatal("cancelled generation accepted observation", err)
	}
}

func TestComputeReleaseIntentReplaysUnderCleanupFence(t *testing.T) {
	db, r := sandboxDatabaseFixture(t)
	ctx := context.Background()
	store, _ := postgres.NewSandboxBindings(db)
	if _, err := store.Reserve(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := store.BindReference(ctx, r.Scope.TenantID, r.Scope.SandboxID, "namespace/name/uid"); err != nil {
		t.Fatal(err)
	}
	id := concurrencyIDs(t, time.Now(), 1)[0]
	op := sandbox.Operation{ID: string(id), Digest: sandbox.LifecycleDigest(r.Scope.TenantID, r.Scope.SandboxID, "release", string(id))}
	if _, err := store.BeginOperation(ctx, r.Scope.TenantID, r.Scope.SandboxID, "release", op); err != nil {
		t.Fatal(err)
	}
	intent, err := store.LoadCompute(ctx, r.Scope.TenantID, r.Scope.SandboxID)
	if err != nil || intent.ReleaseAuthorized {
		t.Fatal("missing cleanup was authorized", err)
	}
	_, err = db.Exec(`INSERT INTO cleanup_intents(tenant_id,cleanup_intent_id,owner_type,owner_id,target_type,provider_kind,external_reference,cleanup_operation_id,request_digest,ownership_proof_digest,is_orphan,state,next_attempt_at,created_at,updated_at) VALUES($1,$2,'sandbox-binding',$3,'sandbox','kubernetes-agent-sandbox','namespace/name/uid',$2,$4,$5,false,'PENDING',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, r.Scope.TenantID, id, r.Scope.SandboxID, op.Digest, r.Operation.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE executions SET state='CANCELLING',state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND execution_id=$2`, r.Scope.TenantID, r.Scope.ExecutionID); err != nil {
		t.Fatal(err)
	}
	restarted, _ := postgres.NewSandboxBindings(db)
	intent, err = restarted.LoadCompute(ctx, r.Scope.TenantID, r.Scope.SandboxID)
	if err != nil || !intent.ReleaseAuthorized || intent.Current || intent.Operation != op || intent.Desired != sandbox.ComputeReleased {
		t.Fatal("cleanup intent", err)
	}
	if _, err = restarted.BeginOperation(ctx, r.Scope.TenantID, r.Scope.SandboxID, "release", op); err != nil {
		t.Fatal(err)
	}
	if err = restarted.RecordCompute(ctx, intent, sandbox.ComputeObservation{State: sandbox.Released, Code: "COMPUTE_ABSENT", Converged: true}); err != nil {
		t.Fatal(err)
	}
	jobs, err := restarted.ClaimCompute(ctx, r.Scope.TenantID, id, time.Minute, 2)
	if err != nil || len(jobs) != 1 {
		t.Fatal("one stable work per binding", len(jobs), err)
	}
	if err = restarted.FinishCompute(ctx, jobs[0], true, "COMPUTE_ABSENT", time.Second); err != nil {
		t.Fatal(err)
	}
	jobs, err = restarted.ClaimCompute(ctx, r.Scope.TenantID, id, time.Minute, 2)
	if err != nil || len(jobs) != 0 {
		t.Fatal("completed cleanup replayed", err)
	}
}
