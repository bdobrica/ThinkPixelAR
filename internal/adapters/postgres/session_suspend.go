package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	checkpoint "github.com/bdobrica/ThinkPixelAR/internal/app/checkpoint"
	app "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// SuspendAccess checks current caller authorization, runtime eligibility and
// retention policy, including disclosure of historical results on every replay.
type SuspendAccess func(context.Context, app.SuspendRequest) error

// SuspendBoundary contains locked, authoritative identities, not agent claims.
type SuspendBoundary struct {
	WorkspaceID, GenerationID primitives.ID
	ExecutionGeneration       int64
	Sandboxes                 []sandbox.ComputeIntent
}

// SuspendVerifier must independently prove quiescence remains enforced, that no
// Workspace/vendor writes occurred since the selected checkpoint, and that all
// external execution credentials and pending side effects are durably resolved
// or revoked. It must honor context, retain storage pins through commit, and
// neither release compute nor mutate locked rows. No permissive default exists.
type SuspendVerifier func(context.Context, app.SuspendRequest, SuspendBoundary) error

type SessionSuspends struct {
	db        *sql.DB
	access    SuspendAccess
	verify    SuspendVerifier
	validator *checkpoint.RestoreValidator
}

var _ app.Suspender = (*SessionSuspends)(nil)

func NewSessionSuspends(db *sql.DB, access SuspendAccess, verify SuspendVerifier, validator *checkpoint.RestoreValidator) (*SessionSuspends, error) {
	if db == nil || access == nil || verify == nil || validator == nil {
		return nil, workspace.ErrInvalid
	}
	return &SessionSuspends{db, access, verify, validator}, nil
}

// Suspend commits the release boundary. Snapshot/export happens before this
// call; no provider mutation occurs here. The Session lock excludes admission,
// close and checkpoint publication while the exact durable inputs are checked.
func (s *SessionSuspends) Suspend(ctx context.Context, r app.SuspendRequest) (app.SuspendResult, error) {
	if err := r.Validate(); err != nil {
		return app.SuspendResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if s.access(ctx, r) != nil {
		return app.SuspendResult{}, workspace.ErrUnavailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return app.SuspendResult{}, storageDBError(err)
	}
	defer tx.Rollback()
	fail := func(e error) (app.SuspendResult, error) { return app.SuspendResult{}, restoreError(e) }
	tenant := r.Caller.TenantID
	if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, tenant); err != nil {
		return fail(err)
	}
	var state, current, head string
	var version, generation int64
	if err = tx.QueryRowContext(ctx, `SELECT state,state_version,execution_generation,COALESCE(current_execution_id::text,''),COALESCE(current_checkpoint_id::text,'') FROM sessions WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, tenant, r.SessionID).Scan(&state, &version, &generation, &current, &head); err != nil {
		return fail(err)
	}
	var savedDigest string
	result := app.SuspendResult{State: "SUSPENDED", OperationID: r.OperationID}
	err = tx.QueryRowContext(ctx, `SELECT request_digest,session_id,checkpoint_id,state_version,execution_generation FROM session_suspend_operations WHERE tenant_id=$1 AND operation_id=$2`, tenant, r.OperationID).Scan(&savedDigest, &result.SessionID, &result.CheckpointID, &result.Version, &result.Generation)
	if err == nil {
		if savedDigest != r.Digest() || result.SessionID != r.SessionID {
			return fail(workspace.ErrConflict)
		}
		// Historical replay is not a new transition or a new cleanup authorization.
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fail(err)
	}
	if (state != "READY" && state != "IDLE") || version != r.ExpectedVersion || current != "" || head != string(r.CheckpointID) {
		return fail(workspace.ErrConflict)
	}
	var mutable bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM executions WHERE tenant_id=$1 AND session_id=$2 AND state IN ('QUEUED','MATERIALIZING','RUNNING','CANCELLING','TIMING_OUT'))`, tenant, r.SessionID).Scan(&mutable); err != nil {
		return fail(err)
	}
	if mutable {
		return fail(workspace.ErrConflict)
	}
	if _, err = validateCheckpointTx(ctx, tx, s.validator, tenant, r.SessionID, r.CheckpointID); err != nil {
		return fail(err)
	}
	// Require a detached boundary published after the last Execution, not merely
	// an old checkpoint whose files happen to still exist.
	var boundary SuspendBoundary
	var requestBytes []byte
	var workspaceState, attachment, config string
	var currentGeneration int64
	err = tx.QueryRowContext(ctx, `SELECT w.workspace_id,w.current_workspace_generation_id,w.current_generation,w.state,COALESCE(w.current_attachment_id::text,''),w.config_digest,o.request FROM checkpoints c JOIN workspaces w ON w.tenant_id=c.tenant_id AND w.workspace_id=c.workspace_id JOIN workspace_checkpoint_operations o ON o.tenant_id=c.tenant_id AND o.operation_id=c.workspace_generation_id WHERE c.tenant_id=$1 AND c.session_id=$2 AND c.checkpoint_id=$3 AND w.current_workspace_generation_id=c.workspace_generation_id AND w.current_generation=c.workspace_generation AND w.provider_kind='kubernetes'`, tenant, r.SessionID, r.CheckpointID).Scan(&boundary.WorkspaceID, &boundary.GenerationID, &currentGeneration, &workspaceState, &attachment, &config, &requestBytes)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = workspace.ErrConflict
		}
		return fail(err)
	}
	var w workspace.CheckpointRequest
	if json.Unmarshal(requestBytes, &w) != nil || w.Validate() != nil || w.TenantID != tenant || w.SessionID != r.SessionID || w.WorkspaceID != boundary.WorkspaceID || w.Operation.ID != boundary.GenerationID || w.ParentGeneration+1 != currentGeneration || w.ConfigurationDigest != config || w.SessionGeneration != generation || w.ExecutionID != "" || workspaceState != "READY" || attachment != "" {
		return fail(workspace.ErrConflict)
	}
	boundary.ExecutionGeneration = generation
	// Bounded retained compute set. Unknown ownership must be reconciled first.
	rows, err := tx.QueryContext(ctx, `SELECT sandbox_binding_id FROM sandbox_bindings WHERE tenant_id=$1 AND session_id=$2 AND state<>'RELEASED' ORDER BY sandbox_binding_id LIMIT 65`, tenant, r.SessionID)
	if err != nil {
		return fail(err)
	}
	var ids []primitives.ID
	for rows.Next() {
		var id primitives.ID
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return fail(err)
	}
	if len(ids) > 64 {
		return fail(workspace.ErrConflict)
	}
	for _, id := range ids {
		intent, e := loadCompute(ctx, tx, tenant, id)
		if e != nil {
			return fail(e)
		}
		if intent.Current || intent.Binding.ProviderReference == "" || intent.Desired == sandbox.ComputeReleased && !intent.ReleaseAuthorized {
			return fail(workspace.ErrConflict)
		}
		boundary.Sandboxes = append(boundary.Sandboxes, intent)
	}
	// Give callbacks an independent copy: policy cannot rewrite cleanup targets.
	raw, _ := json.Marshal(boundary)
	var checked SuspendBoundary
	if err = json.Unmarshal(raw, &checked); err != nil {
		return fail(err)
	}
	if s.verify(ctx, r, checked) != nil {
		return fail(workspace.ErrIntegrity)
	}
	for _, intent := range boundary.Sandboxes {
		if err = suspendRelease(ctx, tx, intent); err != nil {
			return fail(err)
		}
	}
	// Clear all live connection projections, including already released bindings.
	if _, err = tx.ExecContext(ctx, `UPDATE agentd_credential_state c SET version=version+1,connection_id=NULL,connection_digest=NULL,connection_deadline=NULL FROM sandbox_bindings b WHERE c.tenant_id=$1 AND b.tenant_id=c.tenant_id AND b.sandbox_binding_id=c.sandbox_binding_id AND b.session_id=$2`, tenant, r.SessionID); err != nil {
		return fail(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE agentd_bootstrap_delivery d SET cleanup_requested=true,retry_after=clock_timestamp() FROM agentd_credentials c JOIN sandbox_bindings b ON b.tenant_id=c.tenant_id AND b.sandbox_binding_id=c.sandbox_binding_id WHERE d.tenant_id=$1 AND c.tenant_id=d.tenant_id AND c.credential_id=d.credential_id AND b.session_id=$2 AND NOT d.cleaned`, tenant, r.SessionID); err != nil {
		return fail(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sessions SET state='SUSPENDED',state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND session_id=$2`, tenant, r.SessionID); err != nil {
		return fail(err)
	}
	result = app.SuspendResult{OperationID: r.OperationID, SessionID: r.SessionID, CheckpointID: r.CheckpointID, State: "SUSPENDED", Version: version + 1, Generation: generation}
	if _, err = tx.ExecContext(ctx, `INSERT INTO session_suspend_operations(tenant_id,operation_id,session_id,checkpoint_id,request_digest,prior_state,state_version,execution_generation) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, tenant, r.OperationID, r.SessionID, r.CheckpointID, r.Digest(), state, result.Version, generation); err != nil {
		return fail(err)
	}
	if err = suspendedEvent(ctx, tx, r, result, state); err != nil {
		return fail(err)
	}
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	return result, nil
}

func suspendRelease(ctx context.Context, tx *sql.Tx, intent sandbox.ComputeIntent) error {
	b := intent.Binding
	scope := b.Request.Scope
	if intent.Desired != sandbox.ComputeReleased {
		id, err := primitives.NewID(time.Now())
		if err != nil {
			return err
		}
		digest := sandbox.LifecycleDigest(scope.TenantID, scope.SandboxID, "release", string(id))
		if _, err = tx.ExecContext(ctx, `INSERT INTO cleanup_intents(tenant_id,cleanup_intent_id,owner_type,owner_id,target_type,provider_kind,external_reference,cleanup_operation_id,request_digest,ownership_proof_digest,is_orphan,state,next_attempt_at,created_at,updated_at) VALUES($1,$2,'sandbox-binding',$3,'sandbox',$4,$5,$2,$6,$7,false,'PENDING',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, scope.TenantID, id, scope.SandboxID, b.Request.Profile.Implementation.ProviderKind, b.ProviderReference, digest, b.Request.Operation.Digest); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO sandbox_operations(tenant_id,sandbox_binding_id,operation_id,kind,request_digest,revision) VALUES($1,$2,$3,'release',$4,$5)`, scope.TenantID, scope.SandboxID, id, digest, intent.Revision+1); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE sandbox_binding_requests SET operation_revision=$3 WHERE tenant_id=$1 AND sandbox_binding_id=$2`, scope.TenantID, scope.SandboxID, intent.Revision+1); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE sandbox_bindings SET release_operation_id=$3,release_request_digest=$4,state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_binding_id=$2`, scope.TenantID, scope.SandboxID, id, digest); err != nil {
			return err
		}
	}
	return queueCompute(ctx, tx, scope.TenantID, scope.SandboxID)
}

func suspendedEvent(ctx context.Context, tx *sql.Tx, r app.SuspendRequest, result app.SuspendResult, prior string) error {
	payload, _ := json.Marshal(map[string]any{"operation_id": r.OperationID, "checkpoint_id": r.CheckpointID, "previous_state": prior, "state": "SUSPENDED", "execution_generation": result.Generation, "reason_code": "SUSPEND_COMMITTED", "state_version": result.Version})
	tenant := r.Caller.TenantID
	if _, err := tx.ExecContext(ctx, `INSERT INTO runtime_event_streams(tenant_id,session_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, tenant, r.SessionID); err != nil {
		return err
	}
	var sequence int64
	if err := tx.QueryRowContext(ctx, `SELECT last_sequence+1 FROM runtime_event_streams WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, tenant, r.SessionID).Scan(&sequence); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO runtime_events(tenant_id,event_id,session_id,sequence,aggregate_version,schema_version,event_type,occurred_at,source,classification,payload,retention_policy) VALUES($1,$2,$3,$4,$5,'thinkpixel.runtime-event/v1','session.state_changed',CURRENT_TIMESTAMP,'agent-runtime','Internal',$6,'runtime-control')`, tenant, r.OperationID, r.SessionID, sequence, result.Version, payload); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO outbox_messages(tenant_id,message_id,topic,schema_version,event_id,aggregate_type,aggregate_id,aggregate_version,payload,payload_digest,state,available_at,created_at,updated_at) VALUES($1,$2,'session.suspended','thinkpixel.session-suspend/v1',$2,'session',$3,$4,$5,$6,'PENDING',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, tenant, r.OperationID, r.SessionID, result.Version, payload, workspace.EvidenceDigest(payload))
	return err
}
