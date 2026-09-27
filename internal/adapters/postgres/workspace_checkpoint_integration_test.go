package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
)

func checkpointFixture(t *testing.T, idle ...bool) (*sql.DB, workspace.CheckpointRequest, workspace.CheckpointProof) {
	t.Helper()
	db, base := sandboxDatabaseFixture(t)
	ids := concurrencyIDs(t, time.Now(), 6)
	r := workspace.CheckpointRequest{TenantID: base.Scope.TenantID, SessionID: base.Scope.SessionID, WorkspaceID: ids[0], ParentID: ids[1], Operation: workspace.Operation{ID: ids[2]}, SessionGeneration: 1, ExecutionID: base.Scope.ExecutionID, AttemptID: base.Scope.AttemptID, AttachmentID: ids[3], ConfigurationDigest: testDigest('c')}
	if len(idle) > 0 && idle[0] {
		seedConcurrencySession(t, db, ids[4], ids[5], time.Now(), false)
		r.TenantID, r.SessionID = ids[4], ids[5]
		r.ExecutionID, r.AttemptID, r.AttachmentID = "", "", ""
		r.SessionGeneration = 0
		if _, err := db.Exec(`UPDATE sessions SET state='READY',state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND session_id=$2`, r.TenantID, r.SessionID); err != nil {
			t.Fatal(err)
		}
	}
	r.Operation.Digest = workspace.CheckpointDigest(r)
	statements := []struct {
		q string
		a []any
	}{
		{`UPDATE executions SET state='RUNNING',state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND execution_id=$2`, []any{r.TenantID, r.ExecutionID}},
		{`UPDATE attempts SET state='RUNNING',state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND attempt_id=$2`, []any{r.TenantID, r.AttemptID}},
		{`INSERT INTO workspaces(tenant_id,workspace_id,session_id,state,provider_kind,provider_reference,capacity_bytes,access_mode,volume_mode,encryption_class,storage_profile,config_digest,source_type,source_reference,provenance,provenance_digest,create_operation_id,retention_disposition) VALUES($1,$2,$3,'PROVISIONING','kubernetes',$2::uuid::text,1024,'single-writer','filesystem','none','test',$4,'empty','empty','{}',$4,$5,'retain')`, []any{r.TenantID, r.WorkspaceID, r.SessionID, r.ConfigurationDigest, r.ParentID}},
		{`INSERT INTO workspace_generations(tenant_id,workspace_generation_id,workspace_id,session_id,generation,operation_id,integrity_algorithm,integrity_root,manifest_digest,logical_bytes,logical_files,storage_evidence,storage_evidence_digest,classification,retention_disposition) VALUES($1,$2,$3,$4,0,$2,'empty-manifest-v1',$5,$5,0,0,'{}',$5,'CONFIDENTIAL','retain')`, []any{r.TenantID, r.ParentID, r.WorkspaceID, r.SessionID, testDigest('a')}},
		{`UPDATE workspaces SET state='READY',current_generation=0,current_workspace_generation_id=$3 WHERE tenant_id=$1 AND workspace_id=$2`, []any{r.TenantID, r.WorkspaceID, r.ParentID}},
		{`UPDATE workspaces SET state='ATTACHED',current_attachment_id=$3 WHERE tenant_id=$1 AND workspace_id=$2`, []any{r.TenantID, r.WorkspaceID, r.AttachmentID}},
	}
	for i, s := range statements {
		if r.ExecutionID == "" && (i < 2 || i == len(statements)-1) {
			continue
		}
		if _, err := db.Exec(s.q, s.a...); err != nil {
			t.Fatal(err)
		}
	}
	p := workspace.CheckpointProof{SnapshotReference: "test/snapshot/immutable-uid", IntegrityAlgorithm: "test-manifest-v1", IntegrityRoot: testDigest('a'), ManifestDigest: testDigest('b'), LogicalFiles: 2, LogicalBytes: 25, Evidence: []byte(`{"workspace":"fixture-snapshot","vendor_state":"fixture-state-snapshot","consistency":"quiesced"}`)}
	return db, r, p
}
func checkpointAllow(context.Context, string, workspace.CheckpointRequest) error { return nil }
func checkpointVerify(context.Context, string, workspace.CheckpointRequest, postgres.CheckpointBoundary, workspace.CheckpointProof) error {
	return nil
}

func TestWorkspaceCheckpointPublication(t *testing.T) {
	db, r, p := checkpointFixture(t)
	ctx := context.Background()
	s, _ := postgres.NewWorkspaceCheckpoints(db, checkpointAllow, checkpointVerify)
	assert := func(state string, generation, events int) {
		t.Helper()
		var gotState string
		var gen, count, out int
		if err := db.QueryRow(`SELECT state,current_generation FROM workspaces WHERE tenant_id=$1 AND workspace_id=$2`, r.TenantID, r.WorkspaceID).Scan(&gotState, &gen); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT count(*) FROM runtime_events WHERE tenant_id=$1 AND event_type='workspace.generation_committed'`, r.TenantID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT count(*) FROM outbox_messages WHERE tenant_id=$1 AND topic='workspace.generation_committed'`, r.TenantID).Scan(&out); err != nil {
			t.Fatal(err)
		}
		if gotState != state || gen != generation || count != events || out != events {
			t.Fatalf("state=%s gen=%d events=%d outbox=%d", gotState, gen, count, out)
		}
	}
	if _, err := s.Publish(ctx, r, p); !errors.Is(err, workspace.ErrConflict) {
		t.Fatal(err)
	}
	if _, err := s.Prepare(ctx, r); err != nil {
		t.Fatal(err)
	}
	assert("SNAPSHOTTING", 0, 0)
	// Same durable operation resumes after process replacement.
	s, _ = postgres.NewWorkspaceCheckpoints(db, checkpointAllow, checkpointVerify)
	if v, err := s.Prepare(ctx, r); err != nil || v.State != "PREPARED" {
		t.Fatal(v, err)
	}
	competing := r
	competing.Operation.ID = concurrencyIDs(t, time.Now(), 1)[0]
	competing.Operation.Digest = workspace.CheckpointDigest(competing)
	if _, err := s.Prepare(ctx, competing); !errors.Is(err, workspace.ErrConflict) {
		t.Fatal(err)
	}
	failing, _ := postgres.NewWorkspaceCheckpoints(db, checkpointAllow, func(context.Context, string, workspace.CheckpointRequest, postgres.CheckpointBoundary, workspace.CheckpointProof) error {
		return errors.New("private verification failure")
	})
	if _, err := failing.Publish(ctx, r, p); !errors.Is(err, workspace.ErrIntegrity) {
		t.Fatal(err)
	}
	assert("SNAPSHOTTING", 0, 0)
	// Force the LAST write (outbox) to fail; generation, state, journal and event
	// sequence must all roll back. The conflicting message belongs only to this fixture.
	_, err := db.Exec(`INSERT INTO outbox_messages(tenant_id,message_id,topic,schema_version,event_id,aggregate_type,aggregate_id,aggregate_version,payload,payload_digest,state,available_at,created_at,updated_at) VALUES($1,$2,'fixture','v1',$2,'workspace',$3,1,'{}',$4,'PENDING',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, r.TenantID, r.Operation.ID, r.WorkspaceID, testDigest('a'))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(ctx, r, p); !errors.Is(err, workspace.ErrUnavailable) {
		t.Fatal(err)
	}
	assert("SNAPSHOTTING", 0, 0)
	if _, err = db.Exec(`DELETE FROM outbox_messages WHERE tenant_id=$1 AND message_id=$2`, r.TenantID, r.Operation.ID); err != nil {
		t.Fatal(err)
	}
	for _, err = range concurrently(6, func(int) error {
		v, e := s.Publish(ctx, r, p)
		if e == nil && (v.State != "COMMITTED" || v.Generation != 1 || v.GenerationID != r.Operation.ID) {
			return errors.New("wrong generation")
		}
		return e
	}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	assert("ATTACHED", 1, 1)
	// Exact bytes remain available after JSONB normalization for digest verification
	// and loss-of-response retries by a restarted worker.
	var proofBytes []byte
	var proofDigest string
	if err = db.QueryRow(`SELECT proof,proof_digest FROM workspace_checkpoint_operations WHERE tenant_id=$1 AND operation_id=$2`, r.TenantID, r.Operation.ID).Scan(&proofBytes, &proofDigest); err != nil || workspace.EvidenceDigest(proofBytes) != proofDigest {
		t.Fatal("lost proof encoding", err)
	}
	var parent, snapshot string
	var gen int
	if err = db.QueryRow(`SELECT parent_workspace_generation_id,provider_snapshot_reference,generation FROM workspace_generations WHERE tenant_id=$1 AND workspace_generation_id=$2`, r.TenantID, r.Operation.ID).Scan(&parent, &snapshot, &gen); err != nil || parent != string(r.ParentID) || snapshot != p.SnapshotReference || gen != 1 {
		t.Fatal(parent, snapshot, gen, err)
	}
	changed := p
	changed.SnapshotReference = "different"
	if _, err = s.Publish(ctx, r, changed); !errors.Is(err, workspace.ErrConflict) {
		t.Fatal(err)
	}
	second := r
	second.ParentID = r.Operation.ID
	second.ParentGeneration = 1
	second.Operation.ID = concurrencyIDs(t, time.Now(), 1)[0]
	second.Operation.Digest = workspace.CheckpointDigest(second)
	if _, err = s.Prepare(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(ctx, second, p); err != nil {
		t.Fatal(err)
	}
	if v, e := s.Publish(ctx, r, p); e != nil || v.Generation != 1 {
		t.Fatal(v, e)
	}
	assert("ATTACHED", 2, 2)
	// Generation evidence and completed journal cannot be rewritten.
	if _, err = db.Exec(`UPDATE workspace_generations SET integrity_root=$3 WHERE tenant_id=$1 AND workspace_generation_id=$2`, r.TenantID, r.Operation.ID, testDigest('d')); err == nil {
		t.Fatal("mutable generation")
	}
	if _, err = db.Exec(`UPDATE workspace_checkpoint_operations SET state='ABORTED',proof_digest=NULL WHERE tenant_id=$1 AND operation_id=$2`, r.TenantID, r.Operation.ID); err == nil {
		t.Fatal("mutable completed operation")
	}
	denied, _ := postgres.NewWorkspaceCheckpoints(db, func(context.Context, string, workspace.CheckpointRequest) error { return errors.New("private denial") }, checkpointVerify)
	if _, err = denied.Publish(ctx, r, p); !errors.Is(err, workspace.ErrUnavailable) {
		t.Fatal(err)
	}
	foreign := r
	foreign.TenantID = concurrencyIDs(t, time.Now(), 1)[0]
	foreign.Operation.Digest = workspace.CheckpointDigest(foreign)
	if _, err = s.Prepare(ctx, foreign); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestWorkspaceCheckpointStaleAndAbort(t *testing.T) {
	db, r, p := checkpointFixture(t)
	ctx := context.Background()
	s, _ := postgres.NewWorkspaceCheckpoints(db, checkpointAllow, checkpointVerify)
	if _, err := s.Prepare(ctx, r); err != nil {
		t.Fatal(err)
	}
	// A cancellation accepted between snapshot preparation and publication fences
	// the publication even if provider evidence subsequently reports success.
	if _, err := db.Exec(`UPDATE executions SET state='CANCELLING',state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND execution_id=$2`, r.TenantID, r.ExecutionID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(ctx, r, p); !errors.Is(err, workspace.ErrConflict) {
		t.Fatal(err)
	}
	if err := s.Abort(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.Abort(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(ctx, r, p); !errors.Is(err, workspace.ErrConflict) {
		t.Fatal(err)
	}
	var state string
	var generation int
	if err := db.QueryRow(`SELECT state,current_generation FROM workspaces WHERE tenant_id=$1 AND workspace_id=$2`, r.TenantID, r.WorkspaceID).Scan(&state, &generation); err != nil || state != "DEGRADED" || generation != 0 {
		t.Fatal(state, generation, err)
	}
}

func TestWorkspaceCheckpointRejectsChangedFence(t *testing.T) {
	for _, field := range []string{"attempt", "attachment", "source", "config", "epoch"} {
		t.Run(field, func(t *testing.T) {
			db, r, p := checkpointFixture(t)
			ctx := context.Background()
			s, _ := postgres.NewWorkspaceCheckpoints(db, checkpointAllow, checkpointVerify)
			if _, err := s.Prepare(ctx, r); err != nil {
				t.Fatal(err)
			}
			changed := r
			switch field {
			case "attempt":
				if _, err := db.Exec(`UPDATE attempts SET is_current=false,state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND attempt_id=$2`, r.TenantID, r.AttemptID); err != nil {
					t.Fatal(err)
				}
			case "attachment":
				if _, err := db.Exec(`UPDATE workspaces SET current_attachment_id=NULL WHERE tenant_id=$1 AND workspace_id=$2`, r.TenantID, r.WorkspaceID); err != nil {
					t.Fatal(err)
				}
			case "source":
				changed.ParentGeneration++
			case "config":
				changed.ConfigurationDigest = testDigest('d')
			case "epoch":
				changed.SessionGeneration++
			}
			changed.Operation.Digest = workspace.CheckpointDigest(changed)
			if _, err := s.Publish(ctx, changed, p); !errors.Is(err, workspace.ErrConflict) {
				t.Fatal(err)
			}
			var count int
			if err := db.QueryRow(`SELECT count(*) FROM workspace_generations WHERE tenant_id=$1 AND workspace_id=$2`, r.TenantID, r.WorkspaceID).Scan(&count); err != nil || count != 1 {
				t.Fatal(count, err)
			}
		})
	}
}

func TestWorkspaceCheckpointReadyBoundary(t *testing.T) {
	db, r, p := checkpointFixture(t, true)
	ctx := context.Background()
	verifies := 0
	verify := func(_ context.Context, phase string, got workspace.CheckpointRequest, b postgres.CheckpointBoundary, _ workspace.CheckpointProof) error {
		if got != r || b.ProviderKind != "kubernetes" || b.ConfigurationDigest != r.ConfigurationDigest {
			return errors.New("wrong trusted boundary")
		}
		verifies++
		return nil
	}
	s, _ := postgres.NewWorkspaceCheckpoints(db, checkpointAllow, verify)
	if _, err := s.Prepare(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(ctx, r, p); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM workspaces WHERE tenant_id=$1 AND workspace_id=$2`, r.TenantID, r.WorkspaceID).Scan(&state); err != nil || state != "READY" || verifies != 2 {
		t.Fatal(state, verifies, err)
	}
}
