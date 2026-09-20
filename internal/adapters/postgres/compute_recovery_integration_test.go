package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
)

func TestMissingComputeCreatesAtomicRecoveryWithoutTerminalizingSession(t *testing.T) {
	db, r := sandboxDatabaseFixture(t)
	ctx := context.Background()
	store, _ := postgres.NewSandboxBindings(db)
	if _, err := store.Reserve(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := store.BindReference(ctx, r.Scope.TenantID, r.Scope.SandboxID, "namespace/name/uid"); err != nil {
		t.Fatal(err)
	}
	intent, err := store.LoadCompute(ctx, r.Scope.TenantID, r.Scope.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	observation := sandbox.ComputeObservation{State: sandbox.Unknown, Code: "BOUND_COMPUTE_MISSING", RecoveryRequired: true}
	if err = store.RecordCompute(ctx, intent, observation); err != nil {
		t.Fatal(err)
	}
	if err = store.RecordCompute(ctx, intent, observation); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatal("replayed stale observation", err)
	}
	var state, recovery, currentExecution, executionState string
	err = db.QueryRow(`SELECT s.state,s.recovery_state,s.current_execution_id,e.state FROM sessions s JOIN executions e ON e.tenant_id=s.tenant_id AND e.execution_id=s.current_execution_id WHERE s.tenant_id=$1 AND s.session_id=$2`, r.Scope.TenantID, r.Scope.SessionID).Scan(&state, &recovery, &currentExecution, &executionState)
	if err != nil || state != "DEGRADED" || recovery != "ACTIVE" || currentExecution != string(r.Scope.ExecutionID) || executionState != "MATERIALIZING" {
		t.Fatal("Session continuity lost", state, recovery, executionState, err)
	}
	for _, query := range []string{
		`SELECT count(*) FROM reconciliation_work WHERE tenant_id=$1 AND work_kind='sandbox.recover' AND state='PENDING'`,
		`SELECT count(*) FROM runtime_events WHERE tenant_id=$1 AND event_type='session.degraded'`,
		`SELECT count(*) FROM outbox_messages WHERE tenant_id=$1 AND schema_version='thinkpixel.runtime-event/v1'`,
		`SELECT count(*) FROM cleanup_intents WHERE tenant_id=$1 AND state='PENDING'`,
	} {
		var count int
		if err = db.QueryRow(query, r.Scope.TenantID).Scan(&count); err != nil || count != 1 {
			t.Fatal("missing atomic recovery effect", count, err)
		}
	}
	if _, err = db.Exec(`UPDATE attempts SET sandbox_heartbeat_at=CURRENT_TIMESTAMP,state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND attempt_id=$2`, r.Scope.TenantID, r.Scope.AttemptID); err == nil {
		t.Fatal("degraded Session accepted Attempt mutation")
	}
	restarted, _ := postgres.NewSandboxBindings(db)
	release, err := restarted.LoadCompute(ctx, r.Scope.TenantID, r.Scope.SandboxID)
	if err != nil || release.Current || !release.ReleaseAuthorized || release.Desired != sandbox.ComputeReleased {
		t.Fatal("cleanup not replayable", err)
	}
	if _, err = restarted.BeginOperation(ctx, r.Scope.TenantID, r.Scope.SandboxID, "release", release.Operation); err != nil {
		t.Fatal("cleanup fence rejected", err)
	}
	if err = restarted.RecordCompute(ctx, release, sandbox.ComputeObservation{State: sandbox.Released, Code: "COMPUTE_ABSENT", Converged: true}); err != nil {
		t.Fatal(err)
	}
}

func TestOrphanedComputeCannotDegradeCancellingSession(t *testing.T) {
	db, r := sandboxDatabaseFixture(t)
	ctx := context.Background()
	store, _ := postgres.NewSandboxBindings(db)
	if _, err := store.Reserve(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := store.BindReference(ctx, r.Scope.TenantID, r.Scope.SandboxID, "namespace/name/orphan-uid"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE executions SET state='CANCELLING',state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND execution_id=$2`, r.Scope.TenantID, r.Scope.ExecutionID); err != nil {
		t.Fatal(err)
	}
	intent, err := store.LoadCompute(ctx, r.Scope.TenantID, r.Scope.SandboxID)
	if err != nil || intent.Current {
		t.Fatal(err)
	}
	if err = store.RecordCompute(ctx, intent, sandbox.ComputeObservation{State: sandbox.Unknown, Code: "FENCED_COMPUTE", RecoveryRequired: true}); err != nil {
		t.Fatal(err)
	}
	var state string
	var orphan bool
	if err = db.QueryRow(`SELECT state FROM sessions WHERE tenant_id=$1 AND session_id=$2`, r.Scope.TenantID, r.Scope.SessionID).Scan(&state); err != nil || state != "ACTIVE" {
		t.Fatal("cancellation workflow disrupted", state, err)
	}
	if err = db.QueryRow(`SELECT is_orphan FROM cleanup_intents WHERE tenant_id=$1`, r.Scope.TenantID).Scan(&orphan); err != nil || !orphan {
		t.Fatal("orphan cleanup not recorded", err)
	}
	release, err := store.LoadCompute(ctx, r.Scope.TenantID, r.Scope.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RecordCompute(ctx, release, sandbox.ComputeObservation{State: sandbox.Unknown, Code: "PROVIDER_INTEGRITY_FAILURE", RecoveryRequired: true}); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT state FROM cleanup_intents WHERE tenant_id=$1`, r.Scope.TenantID).Scan(&state); err != nil || state != "QUARANTINED" {
		t.Fatal("uncertain cleanup not quarantined", state, err)
	}
	if _, err = store.BeginOperation(ctx, r.Scope.TenantID, r.Scope.SandboxID, "release", release.Operation); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatal("quarantined cleanup authorized", err)
	}
	owner := concurrencyIDs(t, time.Now(), 1)[0]
	jobs, err := store.ClaimCompute(ctx, r.Scope.TenantID, owner, time.Minute, 5)
	if err != nil || len(jobs) != 1 || jobs[0].Kind() != "sandbox.reconcile" {
		t.Fatal("compute worker stole recovery work", err)
	}
}

func TestCommittedCleanupIntentSurvivesPreProviderCrash(t *testing.T) {
	db, r := sandboxDatabaseFixture(t)
	ctx := context.Background()
	store, _ := postgres.NewSandboxBindings(db)
	if _, err := store.Reserve(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := store.BindReference(ctx, r.Scope.TenantID, r.Scope.SandboxID, "namespace/name/gap-uid"); err != nil {
		t.Fatal(err)
	}
	id := concurrencyIDs(t, time.Now(), 1)[0]
	digest := sandbox.LifecycleDigest(r.Scope.TenantID, r.Scope.SandboxID, "release", string(id))
	_, err := db.Exec(`INSERT INTO cleanup_intents(tenant_id,cleanup_intent_id,owner_type,owner_id,target_type,provider_kind,external_reference,cleanup_operation_id,request_digest,ownership_proof_digest,is_orphan,state,next_attempt_at,created_at,updated_at) VALUES($1,$2,'sandbox-binding',$3,'sandbox','kubernetes-agent-sandbox','namespace/name/gap-uid',$2,$4,$5,false,'PENDING',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, r.Scope.TenantID, id, r.Scope.SandboxID, digest, r.Operation.Digest)
	if err != nil {
		t.Fatal(err)
	}
	restarted, _ := postgres.NewSandboxBindings(db)
	intent, err := restarted.LoadCompute(ctx, r.Scope.TenantID, r.Scope.SandboxID)
	if err != nil || intent.Revision != 0 || intent.Operation.ID != string(id) || !intent.ReleaseAuthorized || intent.Desired != sandbox.ComputeReleased {
		t.Fatal("lost committed cleanup", err)
	}
	revision, err := restarted.BeginOperation(ctx, r.Scope.TenantID, r.Scope.SandboxID, "release", intent.Operation)
	if err != nil || revision != 1 {
		t.Fatal("cleanup could not resume", err)
	}
}
