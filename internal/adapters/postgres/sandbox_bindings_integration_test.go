package postgres_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/config/runtimeprofiles"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/attempt"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
)

func sandboxDatabaseFixture(t *testing.T) (*sql.DB, sandbox.AcquireRequest) {
	t.Helper()
	url := os.Getenv("THINKPIXELAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("THINKPIXELAR_TEST_DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC().Truncate(time.Microsecond)
	ids := concurrencyIDs(t, now, 6)
	seedTenant(t, db, ids[0])
	raw, err := os.ReadFile("../../../docs/profiles/coding-medium-secure.json")
	if err != nil {
		t.Fatal(err)
	}
	registry, err := runtimeprofiles.New()
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.Reload([][]byte{raw}, func(runtimeprofile.Profile) ([]byte, error) { return []byte(`{"fixture":"qualified"}`), nil }); err != nil {
		t.Fatal(err)
	}
	profile, raw, pd, _, impl, _ := registry.Lookup("coding-medium-secure")
	runtime := sandbox.Runtime{Image: "registry.invalid/agent@" + testDigest('a'), Architecture: "amd64", Entrypoint: []string{"/agentd"}}
	runtimeJSON, _ := json.Marshal(map[string]any{"image": map[string]any{"reference": runtime.Image}, "entrypoint": map[string]any{"command": runtime.Entrypoint}, "platform": map[string]any{"architectures": []string{"amd64"}}})
	binding := session.RuntimeBinding{AuthorityMode: "LOCAL", AuthorityNamespace: "concurrency", AgentID: "agent", AgentVersionID: "v1", RuntimeSpecSchemaVersion: "v1", RuntimeSpec: runtimeJSON, RuntimeSpecDigest: sandbox.Digest(runtimeJSON), RuntimeProfileSchemaVersion: "v1", RuntimeProfileSnapshot: raw, RuntimeProfileDigest: pd}
	sess, err := session.New(ids[0], ids[1], binding, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = sess.Transition(session.Ready, 0, now); err == nil {
		err = sess.Transition(session.Active, 1, now)
	}
	if err != nil {
		t.Fatal(err)
	}
	executionValue, err := execution.New(ids[0], ids[2], concurrencyExecutionBinding(ids[1], 1, "sandbox-journal"), now.Add(time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if err = executionValue.Transition(execution.Materializing, 0, nil, now); err != nil {
		t.Fatal(err)
	}
	attemptValue, err := attempt.New(ids[0], ids[3], attempt.Binding{ExecutionID: ids[2], ExecutionGeneration: 1, Number: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := postgres.NewStore(db)
	if err = store.WithinTransaction(context.Background(), ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		if e := repos.Sessions().Add(ctx, sess); e != nil {
			return e
		}
		return repos.Executions().Add(ctx, executionValue)
	}); err != nil {
		t.Fatal(err)
	}
	bindCurrentExecution(t, db, ids[0], ids[1], ids[2])
	if err = store.WithinTransaction(context.Background(), ids[0], func(ctx context.Context, repos persistence.Repositories) error {
		return repos.Attempts().Add(ctx, attemptValue)
	}); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO runtime_profile_resolution_snapshots(tenant_id,execution_id,schema_version,profile_name,canonical_resolution,resolution_digest,implementation_reference,implementation_version,implementation_digest,canonical_supported_versions,supported_versions_digest,decision_reason) VALUES($1,$2,1,$3,$4,$5,'test-mapping','v1',$6,$7,$8,'TEST_FIXTURE')`, ids[0], ids[2], profile.Name, raw, pd, impl, []byte(`{}`), sandbox.Digest([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	r := sandbox.AcquireRequest{Scope: sandbox.Scope{TenantID: ids[0], SessionID: ids[1], ExecutionID: ids[2], AttemptID: ids[3], SandboxID: ids[4], Generation: 1, AttemptOrdinal: 1}, Operation: sandbox.Operation{ID: string(ids[5])}, Runtime: runtime, Profile: profile, ProfileDigest: pd, ImplementationDigest: impl, Workspace: sandbox.Attachment{Reference: "attachment-1", MountPath: "/workspace"}, BootstrapReference: "bootstrap-1", Deadline: now.Add(time.Hour)}
	r.Operation.Digest, _ = sandbox.RequestDigest(r)
	return db, r
}
func TestSandboxBindingsDurableReplay(t *testing.T) {
	db, r := sandboxDatabaseFixture(t)
	ctx := context.Background()
	store, _ := postgres.NewSandboxBindings(db)
	results := concurrently(6, func(int) error { _, err := store.Reserve(ctx, r); return err })
	for _, err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	ref := "namespace/ar-id/exact-uid"
	if err := store.BindReference(ctx, r.Scope.TenantID, r.Scope.SandboxID, ref); err != nil {
		t.Fatal(err)
	}
	restarted, _ := postgres.NewSandboxBindings(db)
	b, err := restarted.Reserve(ctx, r)
	if err != nil || b.ProviderReference != ref || !reflect.DeepEqual(b.Request, r) {
		t.Fatalf("replay: %+v %v", b, err)
	}
	if err = restarted.BindReference(ctx, r.Scope.TenantID, r.Scope.SandboxID, "foreign-uid"); !errors.Is(err, sandbox.ErrIntegrity) {
		t.Fatal("UID changed", err)
	}
	other := concurrencyIDs(t, time.Now(), 1)[0]
	if _, err = restarted.Get(ctx, other, r.Scope.SandboxID); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatal("tenant leak", err)
	}
	changed := r
	changed.BootstrapReference = "other"
	changed.Operation.Digest, _ = sandbox.RequestDigest(changed)
	if _, err = restarted.Reserve(ctx, changed); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatal("operation conflict ignored", err)
	}
	changed = r
	changed.Runtime.Image = "registry.invalid/changed@" + testDigest('b')
	changed.Operation.Digest, _ = sandbox.RequestDigest(changed)
	if _, err = restarted.Reserve(ctx, changed); !errors.Is(err, sandbox.ErrIntegrity) {
		t.Fatal("runtime binding changed", err)
	}
	var count int
	_ = db.QueryRow(`SELECT count(*) FROM sandbox_bindings WHERE tenant_id=$1`, r.Scope.TenantID).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate bindings", count)
	}
	if _, err = db.Exec(`UPDATE sandbox_binding_requests SET canonical_request=$3 WHERE tenant_id=$1 AND sandbox_binding_id=$2`, r.Scope.TenantID, r.Scope.SandboxID, []byte(`{}`)); err == nil {
		t.Fatal("mutable request")
	}
}
func TestSandboxBindingsLifecycleAndFence(t *testing.T) {
	db, r := sandboxDatabaseFixture(t)
	ctx := context.Background()
	store, _ := postgres.NewSandboxBindings(db)
	if _, err := store.Reserve(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := store.BindReference(ctx, r.Scope.TenantID, r.Scope.SandboxID, "namespace/name/uid"); err != nil {
		t.Fatal(err)
	}
	ids := concurrencyIDs(t, time.Now(), 5)
	op := func(i int, kind string) sandbox.Operation {
		id := string(ids[i])
		return sandbox.Operation{ID: id, Digest: sandbox.LifecycleDigest(r.Scope.TenantID, r.Scope.SandboxID, kind, id)}
	}
	for i, kind := range []string{"suspend", "resume", "suspend"} {
		for range 2 {
			revision, err := store.BeginOperation(ctx, r.Scope.TenantID, r.Scope.SandboxID, kind, op(i, kind))
			if err != nil || revision != uint64(i+1) {
				t.Fatal("revision/replay", revision, err)
			}
		}
	}
	if _, err := store.BeginOperation(ctx, r.Scope.TenantID, r.Scope.SandboxID, "resume", op(1, "resume")); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatal("superseded replay", err)
	}
	if _, err := db.Exec(`UPDATE sandbox_operations SET request_digest=$2 WHERE tenant_id=$1`, r.Scope.TenantID, testDigest('f')); err == nil {
		t.Fatal("mutable operation journal")
	}
	// Cancellation fences new compute. Exact durable cleanup still permits release.
	if _, err := db.Exec(`UPDATE executions SET state='CANCELLING',state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND execution_id=$2`, r.Scope.TenantID, r.Scope.ExecutionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Reserve(ctx, r); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatal("acquire after cancellation", err)
	}
	if _, err := store.BeginOperation(ctx, r.Scope.TenantID, r.Scope.SandboxID, "resume", op(3, "resume")); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatal("resume after cancellation", err)
	}
	release := op(4, "release")
	if _, err := store.BeginOperation(ctx, r.Scope.TenantID, r.Scope.SandboxID, "release", release); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatal("cleanup without intent", err)
	}
	_, err := db.Exec(`INSERT INTO cleanup_intents(tenant_id,cleanup_intent_id,owner_type,owner_id,target_type,provider_kind,external_reference,cleanup_operation_id,request_digest,ownership_proof_digest,is_orphan,state,next_attempt_at,created_at,updated_at) VALUES($1,$2,'sandbox-binding',$3,'sandbox','kubernetes-agent-sandbox','namespace/name/uid',$2,$4,$5,false,'PENDING',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, r.Scope.TenantID, release.ID, r.Scope.SandboxID, release.Digest, r.Operation.Digest)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		revision, err := store.BeginOperation(ctx, r.Scope.TenantID, r.Scope.SandboxID, "release", release)
		if err != nil || revision != 4 {
			t.Fatal("cleanup replay", revision, err)
		}
	}
}

func TestSandboxBindingsDatabaseRLS(t *testing.T) {
	db, r := sandboxDatabaseFixture(t)
	ctx := context.Background()
	store, _ := postgres.NewSandboxBindings(db)
	if _, err := store.Reserve(ctx, r); err != nil {
		t.Fatal(err)
	}
	// A non-owner, non-superuser role proves table policies independently of the
	// store's explicit tenant predicates. The role owns no persistent resources.
	role := "kas_rls_" + strings.ReplaceAll(string(r.Scope.SandboxID), "-", "")
	if _, err := db.Exec(`CREATE ROLE ` + role + ` NOLOGIN NOSUPERUSER NOBYPASSRLS`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Exec(`REVOKE ALL ON sandbox_bindings,sandbox_binding_requests,sandbox_operations FROM ` + role)
		_, _ = db.Exec(`REVOKE USAGE ON SCHEMA public FROM ` + role)
		_, _ = db.Exec(`DROP ROLE ` + role)
	}()
	if _, err := db.Exec(`GRANT USAGE ON SCHEMA public TO ` + role); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`GRANT SELECT ON sandbox_bindings,sandbox_binding_requests,sandbox_operations TO ` + role); err != nil {
		t.Fatal(err)
	}
	id := string(concurrencyIDs(t, time.Now(), 1)[0])
	operation := sandbox.Operation{ID: id, Digest: sandbox.LifecycleDigest(r.Scope.TenantID, r.Scope.SandboxID, "suspend", id)}
	if _, err := store.BeginOperation(ctx, r.Scope.TenantID, r.Scope.SandboxID, "suspend", operation); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SET LOCAL ROLE ` + role); err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []string{string(r.Scope.TenantID), string(concurrencyIDs(t, time.Now(), 1)[0])} {
		if _, err = tx.Exec(`SELECT set_config('thinkpixelar.tenant_id',$1,true)`, tenant); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"sandbox_bindings", "sandbox_binding_requests", "sandbox_operations"} {
			var count int
			if err = tx.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if tenant == string(r.Scope.TenantID) {
				want = 1
			}
			if count != want {
				t.Fatalf("RLS %s count %d want %d", table, count, want)
			}
		}
	}
}
