package http

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/authority/local"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/harness/codex"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	checkpoints "github.com/bdobrica/ThinkPixelAR/internal/app/checkpoint"
	executions "github.com/bdobrica/ThinkPixelAR/internal/app/execution"
	sessions "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/config/sessionruntimes"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/attempt"
	checkpointdomain "github.com/bdobrica/ThinkPixelAR/internal/domain/checkpoint"
	domainsession "github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/authority"
	checkpointport "github.com/bdobrica/ThinkPixelAR/internal/ports/checkpoint"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	canonical "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

type e2eSigner struct{ key ed25519.PrivateKey }

func (s e2eSigner) Identity() (string, string, ed25519.PublicKey) {
	return "e2e001", "ephemeral-test-key", s.key.Public().(ed25519.PublicKey)
}
func (s e2eSigner) Sign(_ context.Context, b []byte) ([]byte, error) {
	return ed25519.Sign(s.key, b), nil
}

type e2eMaterializer struct {
	reconcile func(sessions.ResumeIntent) (sessions.ResumeObservation, error)
}

func (m e2eMaterializer) Reconcile(_ context.Context, i sessions.ResumeIntent) (sessions.ResumeObservation, error) {
	return m.reconcile(i)
}
func (m e2eMaterializer) Cleanup(context.Context, sessions.ResumeIntent) error {
	return workspace.ErrUnavailable
} // Retain failed candidates for diagnosis.

// The same Session and actual checkpoint flow through the production services.
// Operator authentication, qualification, lifecycle worker and infrastructure
// composition below are explicit test fixtures, not executable server wiring.
func TestStandaloneKubernetesContinuation(t *testing.T) {
	if os.Getenv("THINKPIXELAR_E2E_KUBERNETES") != "1" {
		t.Skip("set THINKPIXELAR_E2E_KUBERNETES=1 for the live scenario")
	}
	url := os.Getenv("THINKPIXELAR_TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("disposable migrated database required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	db, err := sql.Open("pgx", url)
	e2eCheck(t, err)
	defer db.Close()
	e2eCheck(t, db.PingContext(ctx))
	store, err := postgres.NewStore(db)
	e2eCheck(t, err)
	caller := callerFixture(t)
	_, err = db.ExecContext(ctx, `INSERT INTO tenants(tenant_id) VALUES($1)`, caller.TenantID)
	e2eCheck(t, err)
	k := newE2EKube(t, ctx)
	_, registry, approved := sessionCatalog(t)
	// Retain fixture qualification, but bind the actual digest used by both guests.
	var spec map[string]any
	e2eCheck(t, json.Unmarshal(approved.Manifest, &spec))
	spec["image"] = map[string]string{"reference": k.image, "digest": k.image[strings.LastIndex(k.image, "@")+1:]}
	approved.Manifest, err = json.Marshal(spec)
	e2eCheck(t, err)
	normalized, err := canonical.Transform(approved.Manifest)
	e2eCheck(t, err)
	approved.Digest = sandbox.Digest(normalized)
	catalog, err := sessionruntimes.NewLocal([]sessionruntimes.Approved{approved}, registry, qualifyFixture)
	e2eCheck(t, err)
	resolution, err := catalog.Resolve("approved-codex", "coding-homelab-arm64")
	e2eCheck(t, err)
	profile, _, _, _, _, _ := registry.Lookup("coding-homelab-arm64")
	a, err := local.New(local.Config{Mode: "local", Revision: sandbox.Digest([]byte("e2e001-policy")), DefaultProfile: profile.Name, Profiles: []string{profile.Name}, DefaultDuration: 10 * time.Minute, MaximumDuration: 15 * time.Minute, CPU: profile.Resources.CPU.Limit, Memory: profile.Resources.Memory.Limit, EphemeralStorage: profile.Resources.EphemeralStorage.Limit, WorkspaceBytes: profile.Storage.WorkspaceBytes, MaxProcesses: profile.Resources.MaxProcesses, Architectures: []string{"arm64"}, Networks: []string{profile.Network.Profile}, Runtimes: []local.ApprovedRuntime{{Binding: resolution.Binding, Profiles: []string{profile.Name}}}}, registry, store, clock.UTC{})
	e2eCheck(t, err)
	sc, err := sessions.NewCreator(store, catalog, clock.UTC{}, allowSession)
	e2eCheck(t, err)
	ec, err := executions.NewLocalCreator(store, a, clock.UTC{}, allowExecution)
	e2eCheck(t, err)
	reader, err := executions.NewReader(store, func(context.Context, executions.Caller, primitives.ID, primitives.ID) error { return nil })
	e2eCheck(t, err)
	server, err := NewServer(Options{Clock: clock.UTC{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Sessions: sc, Executions: ec, ExecutionReader: reader, AuthenticateSession: func(r *stdhttp.Request) (sessions.Caller, error) {
		if r.Header.Get("Authorization") != "Bearer e2e001-test-only" {
			return sessions.Caller{}, fmt.Errorf("unauthenticated")
		}
		return caller, nil
	}})
	e2eCheck(t, err)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	request := func(method, path, key string, version int64, body string, want int, out any) {
		t.Helper()
		r, e := stdhttp.NewRequestWithContext(ctx, method, httpServer.URL+path, strings.NewReader(body))
		e2eCheck(t, e)
		r.Header.Set("Authorization", "Bearer e2e001-test-only")
		r.Header.Set("Content-Type", "application/json")
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if version > 0 {
			r.Header.Set("If-Match", fmt.Sprintf(`"%d"`, version))
		}
		response, e := httpServer.Client().Do(r)
		e2eCheck(t, e)
		defer response.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		e2eCheck(t, e)
		if response.StatusCode != want {
			t.Fatalf("%s %s: status=%d want=%d body=%.1024s", method, path, response.StatusCode, want, raw)
		}
		if out != nil {
			e2eCheck(t, json.Unmarshal(raw, out))
		}
	}
	var sessionView sessions.View
	request("POST", "/v1/sessions", "e2e001-session-create", 0, createBody, 201, &sessionView)
	sid, wid := sessionView.ID, sessionView.WorkspaceID
	t.Logf("created Session=%s Workspace=%s authority=local", sid, wid)
	parent := e2eID(t)
	configDigest := sandbox.Digest([]byte("e2e001-bounded-export-v1"))
	// Test worker consumes the creation intent. Storage provisioning is live;
	// these metadata writes stand in for the unfinished executable worker.
	e2eSQL(t, db, `INSERT INTO workspaces(tenant_id,workspace_id,session_id,state,provider_kind,provider_reference,capacity_bytes,access_mode,volume_mode,encryption_class,storage_profile,config_digest,source_type,source_reference,provenance,provenance_digest,create_operation_id,retention_disposition) VALUES($1,$2,$3,'PROVISIONING','kubernetes',$2::uuid::text,268435456,'single-writer','filesystem','none','e2e001-local-path',$4,'empty','empty','{}',$4,$5,'retain')`, caller.TenantID, wid, sid, configDigest, parent)
	e2eSQL(t, db, `INSERT INTO workspace_generations(tenant_id,workspace_generation_id,workspace_id,session_id,generation,operation_id,integrity_algorithm,integrity_root,manifest_digest,logical_bytes,logical_files,storage_evidence,storage_evidence_digest,classification,retention_disposition) VALUES($1,$2,$3,$4,0,$2,'empty-manifest-v1',$5,$5,0,0,'{}',$5,'CONFIDENTIAL','retain')`, caller.TenantID, parent, wid, sid, configDigest)
	e2eSQL(t, db, `UPDATE workspaces SET state='READY',current_generation=0,current_workspace_generation_id=$2 WHERE tenant_id=$1`, caller.TenantID, parent)
	e2eCheck(t, store.WithinTransaction(ctx, caller.TenantID, func(ctx context.Context, r persistence.Repositories) error {
		s, e := r.Sessions().GetForUpdate(ctx, sid)
		if e != nil {
			return e
		}
		v := s.StateVersion()
		if e = s.Transition(domainsession.Ready, v, time.Now()); e != nil {
			return e
		}
		return r.Sessions().Update(ctx, s, v)
	}))
	version := func() int64 {
		var v int64
		e2eCheck(t, db.QueryRowContext(ctx, `SELECT state_version FROM sessions WHERE tenant_id=$1 AND session_id=$2`, caller.TenantID, sid).Scan(&v))
		return v
	}
	var first executions.View
	request("POST", "/v1/sessions/"+string(sid)+"/executions", "e2e001-execution-first", version(), `{"input":"Continue the conversation. Do not run tools."}`, 201, &first)
	firstCompute := k.acquire("first")
	k.write(firstCompute, "/workspace/context.txt", []byte("E2E-001 durable workspace\n"))
	bindings, err := postgres.NewSandboxBindings(db)
	e2eCheck(t, err)
	start := func(v executions.View, c e2eCompute, sandboxID primitives.ID) (*agentdv1.Binding, authority.Grant) {
		t.Helper()
		var grant authority.Grant
		e2eCheck(t, store.WithinTransaction(ctx, caller.TenantID, func(ctx context.Context, r persistence.Repositories) error {
			_, g, e := executions.LoadLocalBinding(ctx, r, v.ID)
			grant = g
			return e
		}))
		status, e := a.Validate(ctx, authority.Caller{TenantID: caller.TenantID, PrincipalDigest: caller.PrincipalDigest}, grant)
		e2eCheck(t, e)
		if status.State != authority.Active {
			t.Fatal("inactive grant")
		}
		e2eSQL(t, db, `UPDATE executions SET state='MATERIALIZING',state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND execution_id=$2 AND state='QUEUED'`, caller.TenantID, v.ID)
		aid := e2eID(t)
		at, e := attempt.New(caller.TenantID, aid, attempt.Binding{ExecutionID: v.ID, ExecutionGeneration: v.Generation, Number: 1}, time.Now())
		e2eCheck(t, e)
		e2eCheck(t, store.WithinTransaction(ctx, caller.TenantID, func(ctx context.Context, r persistence.Repositories) error { return r.Attempts().Add(ctx, at) }))
		r := sandbox.AcquireRequest{Scope: sandbox.Scope{TenantID: caller.TenantID, SessionID: sid, ExecutionID: v.ID, AttemptID: aid, SandboxID: sandboxID, Generation: v.Generation, AttemptOrdinal: 1}, Operation: sandbox.Operation{ID: string(e2eID(t))}, Runtime: sandbox.Runtime{Image: k.image, Architecture: "arm64", Entrypoint: []string{"codex", "app-server"}}, Workspace: sandbox.Attachment{Reference: c.workspaceUID, MountPath: "/workspace"}, BootstrapReference: "e2e001-exec-fixture-no-secret", Deadline: grant.ExpiresAt}
		profileRaw, e := json.Marshal(grant.Profile)
		e2eCheck(t, e)
		profileRaw, e = canonical.Transform(profileRaw)
		e2eCheck(t, e)
		e2eSQL(t, db, `INSERT INTO runtime_profile_resolution_snapshots(tenant_id,execution_id,schema_version,profile_name,canonical_resolution,resolution_digest,implementation_reference,implementation_version,implementation_digest,canonical_supported_versions,supported_versions_digest,decision_reason) VALUES($1,$2,1,$3,$4,$5,'e2e001-fixture','v1',$6,$7,$8,'E2E001_FIXTURE')`, caller.TenantID, v.ID, grant.Profile.Name, profileRaw, sandbox.Digest(profileRaw), grant.ImplementationDigest, []byte(`{}`), sandbox.Digest([]byte(`{}`)))
		var raw []byte
		e2eCheck(t, db.QueryRowContext(ctx, `SELECT canonical_resolution,resolution_digest,implementation_digest FROM runtime_profile_resolution_snapshots WHERE tenant_id=$1 AND execution_id=$2`, caller.TenantID, v.ID).Scan(&raw, &r.ProfileDigest, &r.ImplementationDigest))
		e2eCheck(t, json.Unmarshal(raw, &r.Profile))
		r.Operation.Digest, e = sandbox.RequestDigest(r)
		e2eCheck(t, e)
		_, e = bindings.Reserve(ctx, r)
		e2eCheck(t, e)
		e2eCheck(t, bindings.BindReference(ctx, caller.TenantID, sandboxID, k.namespace+"/"+c.name+"/"+c.sandboxUID))
		e2eSQL(t, db, `UPDATE executions SET state='RUNNING',state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND execution_id=$2`, caller.TenantID, v.ID)
		e2eSQL(t, db, `UPDATE attempts SET state='RUNNING',state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND attempt_id=$2`, caller.TenantID, aid)
		return &agentdv1.Binding{TenantId: string(caller.TenantID), SessionId: string(sid), ExecutionId: string(v.ID), AttemptId: string(aid), SandboxBindingId: string(sandboxID), SessionGeneration: v.Generation}, grant
	}
	firstBinding, firstGrant := start(first, firstCompute, e2eID(t))
	firstResult := k.guest(firstCompute, "first", firstBinding)
	if firstResult.Calls != 1 {
		t.Fatal("first turn not observed")
	}
	finish := func(v executions.View, b *agentdv1.Binding) {
		t.Helper()
		tx, e := db.BeginTx(ctx, nil)
		e2eCheck(t, e)
		defer tx.Rollback()
		for _, q := range []string{`UPDATE attempts SET state='SUCCEEDED',is_current=false,terminal_at=clock_timestamp(),terminal_result_reference='e2e001-verified-completion',terminal_result_digest=$3,state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND execution_id=$2`, `UPDATE executions SET state='SUCCEEDED',terminal_at=clock_timestamp(),terminal_result_reference='e2e001-verified-completion',terminal_result_digest=$3,state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND execution_id=$2`} {
			_, e = tx.ExecContext(ctx, q, caller.TenantID, v.ID, configDigest)
			e2eCheck(t, e)
		}
		_, e = tx.ExecContext(ctx, `UPDATE sessions SET state='IDLE',current_execution_id=NULL,state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND session_id=$2`, caller.TenantID, sid)
		e2eCheck(t, e)
		_, e = tx.ExecContext(ctx, `UPDATE workspaces SET state='READY',current_attachment_id=NULL WHERE tenant_id=$1 AND workspace_id=$2`, caller.TenantID, wid)
		e2eCheck(t, e)
		e2eCheck(t, tx.Commit())
	}
	finish(first, firstBinding)
	e2eCheck(t, a.Cancel(ctx, authority.Caller{TenantID: caller.TenantID, PrincipalDigest: caller.PrincipalDigest}, firstGrant))
	t.Logf("first Execution=%s completed; grant=%s retired; thread=%s", first.ID, firstGrant.ID, firstResult.Thread)
	// Bounded immutable file export, external to both disposable guests. No
	// archive extraction, vendor-selected paths, credential files or live home.
	archive := t.TempDir()
	objects := map[string][]byte{}
	for name, path := range map[string]string{"workspace": "/workspace/context.txt", "rollout": "/state/rollout.jsonl", "restore": "/state/restore.json"} {
		raw := k.read(firstCompute, path)
		if len(raw) == 0 || len(raw) > 1<<20 || bytes.Contains(raw, []byte("E2E-001-old-credential-canary")) {
			t.Fatal("unsafe export")
		}
		objects[name] = raw
		f, e := os.OpenFile(filepath.Join(archive, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
		e2eCheck(t, e)
		_, e = f.Write(raw)
		e2eCheck(t, e)
		e2eCheck(t, f.Sync())
		e2eCheck(t, f.Close())
	}
	archiveDir, err := os.Open(archive)
	e2eCheck(t, err)
	e2eCheck(t, archiveDir.Sync())
	e2eCheck(t, archiveDir.Close())
	readObject := func(name string) ([]byte, error) {
		raw, e := os.ReadFile(filepath.Join(archive, name))
		if e != nil || !bytes.Equal(raw, objects[name]) {
			return nil, workspace.ErrIntegrity
		}
		return raw, nil
	}
	expectedWorkspace := []byte("E2E-001 durable workspace\ncompleted execution: " + string(first.ID) + "\n")
	if !bytes.Equal(objects["workspace"], expectedWorkspace) || "sha256:"+firstResult.WorkspaceSHA256 != sandbox.Digest(expectedWorkspace) || firstResult.History {
		t.Fatal("first-execution workspace edit or fresh conversation mismatch")
	}
	root := sandbox.Digest(objects["workspace"])
	vendorDigest := checkpoints.Digest(sandbox.Digest(objects["rollout"]))
	if vendorDigest != firstResult.SHA256 {
		t.Fatal("export mismatch")
	}
	w := workspace.CheckpointRequest{TenantID: caller.TenantID, SessionID: sid, WorkspaceID: wid, ParentID: parent, Operation: workspace.Operation{ID: e2eID(t)}, SessionGeneration: 1, ConfigurationDigest: configDigest}
	w.Operation.Digest = workspace.CheckpointDigest(w)
	proof := workspace.CheckpointProof{SnapshotReference: "e2e001-export:" + string(w.Operation.ID), IntegrityAlgorithm: "sha-256", IntegrityRoot: root, ManifestDigest: root, LogicalFiles: 1, LogicalBytes: int64(len(objects["workspace"])), Evidence: []byte(`{"provider":"e2e001-bounded-file-export","consistency":"process-reaped"}`)}
	verifyObjects := func() error {
		for name := range objects {
			if _, e := readObject(name); e != nil {
				return e
			}
		}
		return nil
	}
	wp, err := postgres.NewWorkspaceCheckpoints(db, func(_ context.Context, _ string, r workspace.CheckpointRequest) error {
		if r != w {
			return workspace.ErrInvalid
		}
		return nil
	}, func(_ context.Context, _ string, r workspace.CheckpointRequest, _ postgres.CheckpointBoundary, _ workspace.CheckpointProof) error {
		return verifyObjects()
	})
	e2eCheck(t, err)
	_, err = wp.Prepare(ctx, w)
	e2eCheck(t, err)
	_, err = wp.Publish(ctx, w, proof)
	e2eCheck(t, err)
	id := e2eID(t)
	cp := checkpointport.Request{ID: id, Boundary: w, Binding: checkpointdomain.Binding{SessionID: sid, WorkspaceID: wid, WorkspaceGenerationID: w.Operation.ID, WorkspaceGeneration: 1, OperationID: id, Purpose: checkpointdomain.PurposeCheckpoint, RuntimeSpecID: approved.ID, RuntimeSpecDigest: checkpoints.Digest(resolution.Binding.RuntimeSpecDigest), AdapterKind: "codex", AdapterVersion: codex.Version, AdapterBuildDigest: codex.LinuxARM64SHA256, ProtocolName: "app-server", ProtocolVersion: "v2", StateFormatName: "codex", StateFormatVersion: "v1", RuntimeProfileDigest: checkpoints.Digest(resolution.Binding.RuntimeProfileDigest), RetentionDisposition: "retain"}, VendorState: []checkpointport.VendorObject{{ID: "rollout", Reference: "e2e001-export:rollout", MediaType: "application/x-ndjson", Format: checkpointport.VersionedName{Name: "codex", Version: "v1"}, Algorithm: "sha-256", Digest: vendorDigest, Size: int64(len(objects["rollout"])), Classification: "Confidential", Role: "required"}}}
	cp.VendorState = append(cp.VendorState, checkpointport.VendorObject{ID: "restore", Reference: "e2e001-export:restore", MediaType: "application/json", Format: checkpointport.VersionedName{Name: "codex", Version: "v1"}, Algorithm: "sha-256", Digest: checkpoints.Digest(sandbox.Digest(objects["restore"])), Size: int64(len(objects["restore"])), Classification: "Confidential", Role: "required"})
	cp.VendorState[0], cp.VendorState[1] = cp.VendorState[1], cp.VendorState[0]
	_, private, err := ed25519.GenerateKey(rand.Reader)
	e2eCheck(t, err)
	signer := e2eSigner{private}
	publication, err := postgres.NewCheckpoints(db, func(_ context.Context, r checkpointport.Request) error {
		if r.Digest() != cp.Digest() {
			return workspace.ErrInvalid
		}
		return nil
	}, func(context.Context, checkpointport.Request, postgres.PublicationSource) error {
		return verifyObjects()
	}, signer)
	e2eCheck(t, err)
	published, err := publication.Publish(ctx, cp)
	e2eCheck(t, err)
	validator, err := checkpoints.NewRestoreValidator(checkpoints.RestoreChecks{
		Keys: func(_ context.Context, tenant primitives.ID, issuer, key string, _ time.Time) (ed25519.PublicKey, error) {
			if tenant != caller.TenantID || issuer != "e2e001" || key != "ephemeral-test-key" {
				return nil, workspace.ErrIntegrity
			}
			return private.Public().(ed25519.PublicKey), nil
		},
		Compatible: func(_ context.Context, r checkpoints.RuntimeManifest, v []checkpointport.VendorObject) error {
			if r.SpecDigest != cp.Binding.RuntimeSpecDigest || r.AdapterBuild != codex.LinuxARM64SHA256 || len(v) != 2 || v[0] != cp.VendorState[0] || v[1] != cp.VendorState[1] {
				return workspace.ErrIntegrity
			}
			return nil
		},
		Workspace: func(_ context.Context, tenant primitives.ID, m checkpoints.WorkspaceManifest) error {
			if tenant != caller.TenantID || m.ID != wid || m.Snapshot != proof.SnapshotReference || m.Root != checkpoints.Digest(root) {
				return workspace.ErrIntegrity
			}
			return verifyObjects()
		},
		OpenVendor: func(_ context.Context, tenant primitives.ID, v checkpointport.VendorObject) (io.ReadCloser, error) {
			if tenant != caller.TenantID || (v != cp.VendorState[0] && v != cp.VendorState[1]) {
				return nil, workspace.ErrIntegrity
			}
			raw, e := readObject(v.ID)
			return io.NopCloser(bytes.NewReader(raw)), e
		}, MaxVendorBytes: 1 << 20,
	})
	e2eCheck(t, err)
	suspendRequest := sessions.SuspendRequest{Caller: caller, SessionID: sid, CheckpointID: id, OperationID: e2eID(t), ExpectedVersion: version()}
	suspender, err := postgres.NewSessionSuspends(db, func(_ context.Context, r sessions.SuspendRequest) error {
		if r != suspendRequest {
			return workspace.ErrInvalid
		}
		return nil
	}, func(context.Context, sessions.SuspendRequest, postgres.SuspendBoundary) error { return verifyObjects() }, validator)
	e2eCheck(t, err)
	suspended, err := suspender.Suspend(ctx, suspendRequest)
	e2eCheck(t, err)
	if suspended.State != "SUSPENDED" {
		t.Fatal("not suspended")
	}
	request("POST", "/v1/sessions/"+string(sid)+"/executions", "e2e001-while-suspended", version(), `{"input":"must not execute"}`, 409, nil)
	t.Logf("signed Checkpoint=%s committed; Session SUSPENDED", id)
	intent, err := bindings.LoadCompute(ctx, caller.TenantID, primitives.ID(firstBinding.SandboxBindingId))
	e2eCheck(t, err)
	if !intent.ReleaseAuthorized || intent.Binding.ProviderReference != k.namespace+"/first/"+firstCompute.sandboxUID {
		t.Fatal("release not authorized")
	}
	k.release(firstCompute)
	k.discardOldVolumes(firstCompute)
	e2eCheck(t, bindings.RecordCompute(ctx, intent, sandbox.ComputeObservation{State: sandbox.Released, Code: "COMPUTE_ABSENT", Converged: true}))
	// Fresh store/coordinator objects load only committed metadata after deletion.
	rr := sessions.ResumeRequest(suspendRequest)
	rr.OperationID = e2eID(t)
	rr.ExpectedVersion = version()
	var secondCompute e2eCompute
	var readyResult e2eGuestResult
	var candidate sessions.ResumeIntent
	resumeStore, err := postgres.NewSessionResumes(db, func(_ context.Context, r sessions.ResumeRequest) error {
		if r != rr {
			return workspace.ErrInvalid
		}
		return nil
	}, func(_ context.Context, i sessions.ResumeIntent) error {
		if i.Runtime.RuntimeSpecDigest != resolution.Binding.RuntimeSpecDigest || !bytes.Equal(i.Manifest, published.Manifest) {
			return workspace.ErrIntegrity
		}
		return verifyObjects()
	}, func(_ context.Context, i sessions.ResumeIntent, o sessions.ResumeObservation) error {
		if i.SandboxID != candidate.SandboxID || o.SandboxReference != k.namespace+"/"+secondCompute.name+"/"+secondCompute.sandboxUID || readyResult.Thread != firstResult.Thread || readyResult.Calls != 0 {
			return workspace.ErrIntegrity
		}
		return verifyObjects()
	}, validator)
	e2eCheck(t, err)
	worker, err := sessions.NewResumer(resumeStore, e2eMaterializer{func(i sessions.ResumeIntent) (sessions.ResumeObservation, error) {
		if i.Request.SessionID != sid || i.WorkspaceID != wid || i.WorkspaceGenerationID != w.Operation.ID || i.WorkspaceGeneration != 1 || i.ExecutionGeneration != 1 {
			return sessions.ResumeObservation{}, workspace.ErrIntegrity
		}
		candidate = i
		secondCompute = k.acquire("resume-" + string(i.SandboxID))
		if secondCompute.sandboxUID == firstCompute.sandboxUID || secondCompute.podUID == firstCompute.podUID || secondCompute.workspaceUID == firstCompute.workspaceUID || secondCompute.stateUID == firstCompute.stateUID {
			return sessions.ResumeObservation{}, workspace.ErrIntegrity
		}
		for name, path := range map[string]string{"workspace": "/workspace/context.txt", "rollout": "/state/rollout.jsonl", "restore": "/state/restore.json"} {
			raw, e := readObject(name)
			if e != nil {
				return sessions.ResumeObservation{}, e
			}
			k.write(secondCompute, path, raw)
		}
		// No Execution is fabricated for readiness. These are synthetic guest-test
		// correlation IDs; this bootstrap never receives an ExecutionGrant.
		b := &agentdv1.Binding{TenantId: string(caller.TenantID), SessionId: string(sid), ExecutionId: string(i.BootstrapID), AttemptId: string(i.BootstrapID), SandboxBindingId: string(i.SandboxID), SessionGeneration: 1}
		readyResult = k.guest(secondCompute, "ready", b)
		return sessions.ResumeObservation{SandboxReference: k.namespace + "/" + secondCompute.name + "/" + secondCompute.sandboxUID, AttachmentReference: string(i.AttachmentID), EvidenceDigest: sandbox.Digest([]byte(secondCompute.podUID))}, nil
	}})
	e2eCheck(t, err)
	resumed, err := worker.Resume(ctx, rr)
	e2eCheck(t, err)
	if resumed.State != "IDLE" || resumed.Generation != 1 || resumed.SessionID != sid || resumed.CheckpointID != id || readyResult.WorkspaceSHA256 != firstResult.WorkspaceSHA256 || readyResult.SHA256 != firstResult.SHA256 {
		t.Fatal("resume publication", resumed.State)
	}
	replayed, err := worker.Resume(ctx, rr)
	e2eCheck(t, err)
	if replayed != resumed {
		t.Fatal("resume replay changed")
	}
	var second executions.View
	request("POST", "/v1/sessions/"+string(sid)+"/executions", "e2e001-execution-second", version(), `{"input":"Continue the conversation. Do not run tools."}`, 201, &second)
	secondBinding, secondGrant := start(second, secondCompute, resumed.SandboxID)
	if second.ID == first.ID || second.Generation != 2 || secondGrant.ID == firstGrant.ID || secondGrant.Generation != 2 {
		t.Fatal("fresh admission missing")
	}
	secondResult := k.guest(secondCompute, "second", secondBinding)
	if secondResult.Thread != firstResult.Thread || secondResult.Calls != 1 || !secondResult.History || secondResult.HistoryMarker != "e2e-002-conversation-"+string(first.ID) || secondResult.WorkspaceSHA256 != firstResult.WorkspaceSHA256 {
		t.Fatal("second execution lost conversation")
	}
	finish(second, secondBinding)
	var status struct {
		State string `json:"state"`
	}
	request("GET", "/v1/executions/"+string(second.ID), "", 0, "", 200, &status)
	if status.State != "SUCCEEDED" {
		t.Fatal("terminal status missing")
	}
	for _, q := range []string{`SELECT count(*) FROM executions WHERE tenant_id=$1 AND state='SUCCEEDED'`, `SELECT count(*) FROM local_authority_grants WHERE tenant_id=$1`} {
		var count int
		e2eCheck(t, db.QueryRowContext(ctx, q, caller.TenantID).Scan(&count))
		if count != 2 {
			t.Fatal("expected two distinct Executions/grants", count)
		}
	}
	e2eCheck(t, verifyObjects())
	var persistedWorkspace, persistedCheckpoint, persistedGenerationID primitives.ID
	var sessionState string
	var executionGeneration, workspaceGeneration int64
	e2eCheck(t, db.QueryRowContext(ctx, `SELECT s.state,s.execution_generation,s.current_checkpoint_id,w.workspace_id,w.current_generation,w.current_workspace_generation_id FROM sessions s JOIN workspaces w ON w.tenant_id=s.tenant_id AND w.session_id=s.session_id WHERE s.tenant_id=$1 AND s.session_id=$2`, caller.TenantID, sid).Scan(&sessionState, &executionGeneration, &persistedCheckpoint, &persistedWorkspace, &workspaceGeneration, &persistedGenerationID))
	if sessionState != "IDLE" || executionGeneration != 2 || persistedCheckpoint != id || persistedWorkspace != wid || workspaceGeneration != 1 || persistedGenerationID != w.Operation.ID {
		t.Fatal("persisted Session/Workspace continuity lost")
	}
	if secondBinding.AttemptId == firstBinding.AttemptId || secondBinding.SandboxBindingId == firstBinding.SandboxBindingId {
		t.Fatal("execution reused disposable identity")
	}
	t.Logf("E2E-002 LIVE PASS Session=%s Workspace=%s WorkspaceGeneration=%s workspaceSHA256=%s thread=%s; first-execution edit and unique assistant history survived deletion of old Sandbox, Pod and PVCs", sid, wid, w.Operation.ID, root, firstResult.Thread)
	t.Logf("E2E-001 LIVE PASS Session=%s Checkpoint=%s Executions=%s,%s generations=1,2 thread=%s; real Kata compute replacement, fresh PVCs, loopback model, test-only worker/auth/transport", sid, id, first.ID, second.ID, firstResult.Thread)
}

func e2eCheck(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func e2eID(t *testing.T) primitives.ID {
	t.Helper()
	id, e := primitives.NewID(time.Now())
	e2eCheck(t, e)
	return id
}
func e2eSQL(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	_, e := db.Exec(q, args...)
	e2eCheck(t, e)
}
