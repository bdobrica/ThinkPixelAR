package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/app/reconciliation"
	"github.com/bdobrica/ThinkPixelAR/internal/app/recovery"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type replacementPolicy func(context.Context, sandbox.Binding, sandbox.AcquireRequest) error

func (p replacementPolicy) CheckReplacement(ctx context.Context, b sandbox.Binding, r sandbox.AcquireRequest) error {
	return p(ctx, b, r)
}

var allowReplacement = replacementPolicy(func(context.Context, sandbox.Binding, sandbox.AcquireRequest) error { return nil })

func newReplacementResources(t *testing.T) sandbox.ReplacementResources {
	ids := concurrencyIDs(t, time.Now(), 3)
	return sandbox.ReplacementResources{AttemptID: ids[0], SandboxID: ids[1], AcquireOperationID: ids[2], AttachmentReference: "fresh-attachment", BootstrapReference: "fresh-bootstrap"}
}

func deletedSandboxFixture(t *testing.T, release bool) (*sql.DB, sandbox.AcquireRequest) {
	t.Helper()
	db, r := sandboxDatabaseFixture(t)
	store, _ := postgres.NewSandboxBindings(db)
	ctx := context.Background()
	if _, err := store.Reserve(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := store.BindReference(ctx, r.Scope.TenantID, r.Scope.SandboxID, "namespace/deleted/old-uid"); err != nil {
		t.Fatal(err)
	}
	intent, err := store.LoadCompute(ctx, r.Scope.TenantID, r.Scope.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RecordCompute(ctx, intent, sandbox.ComputeObservation{State: sandbox.Unknown, Code: "BOUND_COMPUTE_MISSING", RecoveryRequired: true}); err != nil {
		t.Fatal(err)
	}
	if release {
		intent, err = store.LoadCompute(ctx, r.Scope.TenantID, r.Scope.SandboxID)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.RecordCompute(ctx, intent, sandbox.ComputeObservation{State: sandbox.Released, Code: "COMPUTE_ABSENT", Converged: true}); err != nil {
			t.Fatal(err)
		}
	}
	return db, r
}

func TestSandboxReplacementConcurrentReplayAndOldFence(t *testing.T) {
	db, r := deletedSandboxFixture(t, true)
	replacementResources := newReplacementResources(t)
	ctx := context.Background()
	results := make([]sandbox.Binding, 8)
	errs := concurrently(len(results), func(i int) error {
		store, _ := postgres.NewSandboxReplacements(db, allowReplacement)
		var err error
		results[i], err = store.ReplaceDeleted(ctx, r.Scope.TenantID, r.Scope.SandboxID, replacementResources)
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("recovery %d: %v", i, err)
		}
		if !reflect.DeepEqual(results[0], results[i]) {
			t.Fatal("duplicate candidate")
		}
	}
	next := results[0].Request
	if next.Scope.Generation != r.Scope.Generation || next.Scope.AttemptOrdinal != 2 || next.Scope.AttemptID == r.Scope.AttemptID || next.Scope.SandboxID == r.Scope.SandboxID || next.Operation.ID == r.Operation.ID {
		t.Fatal("replacement identity")
	}
	if next.Workspace.WorkspaceID != r.Workspace.WorkspaceID || next.Workspace.Generation != r.Workspace.Generation || next.ProfileDigest != r.ProfileDigest || next.ImplementationDigest != r.ImplementationDigest || !next.Deadline.Equal(r.Deadline) {
		t.Fatal("continuity changed")
	}
	var state string
	var generation uint64
	var count int
	if err := db.QueryRow(`SELECT state,execution_generation FROM sessions WHERE tenant_id=$1`, r.Scope.TenantID).Scan(&state, &generation); err != nil || state != "ACTIVE" || generation != 1 {
		t.Fatal(state, generation, err)
	}
	if err := db.QueryRow(`SELECT state FROM attempts WHERE tenant_id=$1 AND attempt_id=$2`, r.Scope.TenantID, r.Scope.AttemptID).Scan(&state); err != nil || state != "REPLACED" {
		t.Fatal(state, err)
	}
	for _, q := range []string{`SELECT count(*) FROM attempts WHERE tenant_id=$1 AND is_current`, `SELECT count(*) FROM sandbox_replacements WHERE tenant_id=$1`, `SELECT count(*) FROM reconciliation_work WHERE tenant_id=$1 AND work_kind='sandbox.recover' AND state='COMPLETED'`, `SELECT count(*) FROM runtime_events WHERE tenant_id=$1 AND event_type='attempt.replaced'`, `SELECT count(*) FROM outbox_messages WHERE tenant_id=$1 AND aggregate_type='attempt'`} {
		if err := db.QueryRow(q, r.Scope.TenantID).Scan(&count); err != nil || count != 1 {
			t.Fatal("non-atomic recovery", count, err)
		}
	}
	if _, err := db.Exec(`UPDATE attempts SET sandbox_heartbeat_at=clock_timestamp(),updated_at=clock_timestamp(),state_version=state_version+1 WHERE tenant_id=$1 AND attempt_id=$2`, r.Scope.TenantID, r.Scope.AttemptID); err == nil {
		t.Fatal("old Attempt accepted heartbeat")
	}
	bindings, _ := postgres.NewSandboxBindings(db)
	if _, err := bindings.Reserve(ctx, r); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatal("old acquisition revived", err)
	}
	if _, err := bindings.Reserve(ctx, next); err != nil {
		t.Fatal("replacement provider cannot reserve", err)
	}
	restarted, _ := postgres.NewSandboxReplacements(db, allowReplacement)
	competing := newReplacementResources(t)
	if _, err := restarted.ReplaceDeleted(ctx, r.Scope.TenantID, r.Scope.SandboxID, competing); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatal("competing tuple replaced committed candidate", err)
	}
	changed := replacementResources
	changed.BootstrapReference = "different"
	if _, err := restarted.ReplaceDeleted(ctx, r.Scope.TenantID, r.Scope.SandboxID, changed); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatal("conflicting replay", err)
	}
	foreign := concurrencyIDs(t, time.Now(), 1)[0]
	if _, err := restarted.ReplaceDeleted(ctx, foreign, r.Scope.SandboxID, replacementResources); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatal("tenant disclosure", err)
	}
	if _, err := db.Exec(`UPDATE sandbox_replacements SET new_sandbox_id=old_sandbox_id WHERE tenant_id=$1`, r.Scope.TenantID); err == nil {
		t.Fatal("decision rewritten")
	}
	denied, _ := postgres.NewSandboxReplacements(db, replacementPolicy(func(context.Context, sandbox.Binding, sandbox.AcquireRequest) error { return sandbox.ErrPermission }))
	if _, err := denied.ReplaceDeleted(ctx, r.Scope.TenantID, r.Scope.SandboxID, replacementResources); !errors.Is(err, sandbox.ErrPermission) {
		t.Fatal("replay bypassed authority", err)
	}
	if _, err := db.Exec(`UPDATE executions SET state='CANCELLING',state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1`, r.Scope.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ReplaceDeleted(ctx, r.Scope.TenantID, r.Scope.SandboxID, replacementResources); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatal("cancelled replay", err)
	}
}

func TestSandboxReplacementRejectsUnsafeRecovery(t *testing.T) {
	for _, name := range []string{"release-pending", "authority-denied", "running", "cancelled", "command-dispatched", "old-connection", "live-bootstrap", "reused-resources", "recovery-claimed"} {
		t.Run(name, func(t *testing.T) {
			db, r := deletedSandboxFixture(t, name != "release-pending")
			replacementResources := newReplacementResources(t)
			policy := allowReplacement
			resources := replacementResources
			var err error
			switch name {
			case "authority-denied":
				policy = replacementPolicy(func(context.Context, sandbox.Binding, sandbox.AcquireRequest) error { return sandbox.ErrPermission })
			case "running", "cancelled":
				state := "RUNNING"
				if name == "cancelled" {
					state = "CANCELLING"
				}
				_, err = db.Exec(`UPDATE executions SET state=$2,state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1`, r.Scope.TenantID, state)
			case "command-dispatched":
				_, err = db.Exec(`INSERT INTO agentd_admission(tenant_id,sandbox_binding_id,snapshot) VALUES($1,$2,'{}')`, r.Scope.TenantID, r.Scope.SandboxID)
				if err == nil {
					_, err = db.Exec(`INSERT INTO agentd_commands(tenant_id,sandbox_binding_id,operation_id,request_digest,connection_id,connection_epoch,message_id,sequence) VALUES($1,$2,$3,$4,$3,1,$3,1)`, r.Scope.TenantID, r.Scope.SandboxID, r.Operation.ID, r.Operation.Digest)
				}
			case "old-connection":
				_, err = db.Exec(`INSERT INTO agentd_credential_state(tenant_id,sandbox_binding_id,attempt_id,connection_id,connection_epoch,connection_digest,connection_deadline) VALUES($1,$2,$3,$4,1,$5,clock_timestamp()+interval '1 minute')`, r.Scope.TenantID, r.Scope.SandboxID, r.Scope.AttemptID, r.Operation.ID, r.Operation.Digest)
			case "live-bootstrap":
				_, err = db.Exec(`INSERT INTO agentd_credential_state(tenant_id,sandbox_binding_id,attempt_id) VALUES($1,$2,$3)`, r.Scope.TenantID, r.Scope.SandboxID, r.Scope.AttemptID)
				if err == nil {
					_, err = db.Exec(`INSERT INTO agentd_credentials(tenant_id,sandbox_binding_id,credential_id,certificate_digest,issuer_digest,proof_digest,bootstrap,not_before,expires_at) VALUES($1,$2,$3,$4,$4,$4,true,clock_timestamp(),clock_timestamp()+interval '1 minute')`, r.Scope.TenantID, r.Scope.SandboxID, r.Operation.ID, r.Operation.Digest)
				}
				if err == nil {
					_, err = db.Exec(`INSERT INTO agentd_bootstrap_delivery(tenant_id,credential_id,reference,expires_at) VALUES($1,$2,'{}',clock_timestamp()+interval '1 minute')`, r.Scope.TenantID, r.Operation.ID)
				}
			case "recovery-claimed":
				_, err = db.Exec(`UPDATE reconciliation_work SET state='CLAIMED',attempts=attempts+1,claim_owner_id=$2,claim_fence=claim_fence+1,claim_expires_at=clock_timestamp()+interval '1 minute',updated_at=clock_timestamp() WHERE tenant_id=$1 AND work_kind='sandbox.recover'`, r.Scope.TenantID, r.Scope.AttemptID)
			case "reused-resources":
				resources.AttachmentReference = r.Workspace.Reference
			}
			if err != nil {
				t.Fatal(err)
			}
			store, _ := postgres.NewSandboxReplacements(db, policy)
			_, err = store.ReplaceDeleted(context.Background(), r.Scope.TenantID, r.Scope.SandboxID, resources)
			want := sandbox.ErrConflict
			if name == "authority-denied" {
				want = sandbox.ErrPermission
			}
			if !errors.Is(err, want) {
				t.Fatal("unsafe replacement", err)
			}
			var state string
			var count int
			if err = db.QueryRow(`SELECT state FROM sessions WHERE tenant_id=$1`, r.Scope.TenantID).Scan(&state); err != nil || state != "DEGRADED" {
				t.Fatal("partial recovery", state, err)
			}
			if err = db.QueryRow(`SELECT count(*) FROM attempts WHERE tenant_id=$1`, r.Scope.TenantID).Scan(&count); err != nil || count != 1 {
				t.Fatal("partial attempt", count, err)
			}
		})
	}
}

// Fault-injecting infrastructure boundary: the real durable AR stores and
// acquisition reconciler survive a provider create whose response is lost.
type replacementProvider struct {
	bindings     *postgres.SandboxBindings
	exists       bool
	failBefore   bool
	acquisitions int
	request      sandbox.AcquireRequest
}

func (p *replacementProvider) Acquire(ctx context.Context, r sandbox.AcquireRequest) (sandbox.Handle, error) {
	p.acquisitions++
	if p.failBefore {
		p.failBefore = false
		return sandbox.Handle{}, sandbox.ErrUnavailable
	}
	if _, err := p.bindings.Reserve(ctx, r); err != nil {
		return sandbox.Handle{}, err
	}
	p.request = r
	p.exists = true
	if err := p.bindings.BindReference(ctx, r.Scope.TenantID, r.Scope.SandboxID, "namespace/replacement/new-uid"); err != nil {
		return sandbox.Handle{}, err
	}
	return sandbox.Handle{}, sandbox.ErrUnavailable
}
func (p *replacementProvider) Get(ctx context.Context, tenant, id primitives.ID) (sandbox.Status, error) {
	if !p.exists {
		return sandbox.Status{}, sandbox.ErrNotFound
	}
	return sandbox.Status{Handle: sandbox.Handle{SandboxID: id, ProviderReference: "namespace/replacement/new-uid"}, State: sandbox.Ready, Effective: sandbox.EffectiveFacts{Verified: true}}, nil
}
func (*replacementProvider) Release(context.Context, primitives.ID, primitives.ID, sandbox.Operation) error {
	return sandbox.ErrUnsupported
}

type replacementAuthority struct{}

func (replacementAuthority) CheckCompute(context.Context, sandbox.Scope) error { return nil }

func TestSandboxReplacementAcquisitionResponseLoss(t *testing.T) {
	db, r := deletedSandboxFixture(t, true)
	replacementResources := newReplacementResources(t)
	bindings, _ := postgres.NewSandboxBindings(db)
	provider := &replacementProvider{bindings: bindings}
	compute, _ := reconciliation.NewCompute(bindings, provider, replacementAuthority{})
	store, _ := postgres.NewSandboxReplacements(db, allowReplacement)
	coordinator, _ := recovery.NewSandboxReplacement(store, compute)
	ctx := context.Background()
	first, _, firstErr := coordinator.Reconcile(ctx, r.Scope.TenantID, r.Scope.SandboxID, replacementResources)
	if !errors.Is(firstErr, sandbox.ErrConflict) {
		t.Fatal("lost-response observation did not detect changed binding version", firstErr)
	}
	if !provider.exists || provider.acquisitions != 1 {
		t.Fatal("replacement was not acquired")
	}
	// Recreate all AR objects; provider state and PostgreSQL survive the restart.
	bindings, _ = postgres.NewSandboxBindings(db)
	provider.bindings = bindings
	compute, _ = reconciliation.NewCompute(bindings, provider, replacementAuthority{})
	store, _ = postgres.NewSandboxReplacements(db, allowReplacement)
	coordinator, _ = recovery.NewSandboxReplacement(store, compute)
	next, observed, err := coordinator.Reconcile(ctx, r.Scope.TenantID, r.Scope.SandboxID, replacementResources)
	if err != nil || !observed.Converged || observed.State != sandbox.Ready || next.Request.Scope != first.Request.Scope || provider.acquisitions != 1 {
		t.Fatal("response loss allocated duplicate or failed recovery", observed, err)
	}
}

func TestSandboxReplacementRetriesProviderUnavailable(t *testing.T) {
	db, r := deletedSandboxFixture(t, true)
	replacementResources := newReplacementResources(t)
	bindings, _ := postgres.NewSandboxBindings(db)
	provider := &replacementProvider{bindings: bindings, failBefore: true}
	compute, _ := reconciliation.NewCompute(bindings, provider, replacementAuthority{})
	store, _ := postgres.NewSandboxReplacements(db, allowReplacement)
	coordinator, _ := recovery.NewSandboxReplacement(store, compute)
	ctx := context.Background()
	first, observed, err := coordinator.Reconcile(ctx, r.Scope.TenantID, r.Scope.SandboxID, replacementResources)
	if err != nil || observed.State != sandbox.Unknown || observed.Code != "PROVIDER_UNAVAILABLE" || provider.exists {
		t.Fatal("unexpected provider failure", observed, err)
	}
	second, _, err := coordinator.Reconcile(ctx, r.Scope.TenantID, r.Scope.SandboxID, replacementResources)
	if !errors.Is(err, sandbox.ErrConflict) || !provider.exists || first.Request.Scope != second.Request.Scope {
		t.Fatal("provider retry lost identity", err)
	}
	_, observed, err = coordinator.Reconcile(ctx, r.Scope.TenantID, r.Scope.SandboxID, replacementResources)
	if err != nil || !observed.Converged || provider.acquisitions != 2 {
		t.Fatal("provider retry did not converge", observed, err)
	}
}
