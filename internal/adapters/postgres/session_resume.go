package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	checkpoint "github.com/bdobrica/ThinkPixelAR/internal/app/checkpoint"
	app "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// ResumeAccess includes current tenant/principal disclosure on historical replay.
type ResumeAccess func(context.Context, app.ResumeRequest) error

// ResumePolicy independently checks current eligibility of the exact saved
// runtime/profile, retention pins and absence of old authority. It grants no
// Execution authority. It runs before allocation and again before publication.
type ResumePolicy func(context.Context, app.ResumeIntent) error

// ResumeReady independently proves exact ownership, effective runtime/storage,
// fresh bootstrap, semantic vendor readiness, no old credentials and no forward
// work. It must keep the candidate quiescent through commit, honor context, and
// never mutate locked rows. An agent/provider readiness claim alone is inadequate.
type ResumeReady func(context.Context, app.ResumeIntent, app.ResumeObservation) error

type SessionResumes struct {
	db        *sql.DB
	access    ResumeAccess
	policy    ResumePolicy
	ready     ResumeReady
	validator *checkpoint.RestoreValidator
}

var _ app.ResumeStore = (*SessionResumes)(nil)

func NewSessionResumes(db *sql.DB, access ResumeAccess, policy ResumePolicy, ready ResumeReady, validator *checkpoint.RestoreValidator) (*SessionResumes, error) {
	if db == nil || access == nil || policy == nil || ready == nil || validator == nil {
		return nil, workspace.ErrInvalid
	}
	return &SessionResumes{db, access, policy, ready, validator}, nil
}

type resumeSession struct {
	state, head, current string
	version, generation  int64
}

func (s *SessionResumes) transaction(ctx context.Context, tenant, session primitives.ID, fn func(*sql.Tx, resumeSession) error) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return restoreError(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, tenant); err != nil {
		return restoreError(err)
	}
	var v resumeSession
	if err = tx.QueryRowContext(ctx, `SELECT state,state_version,execution_generation,COALESCE(current_checkpoint_id::text,''),COALESCE(current_execution_id::text,'') FROM sessions WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, tenant, session).Scan(&v.state, &v.version, &v.generation, &v.head, &v.current); err != nil {
		return restoreError(err)
	}
	if err = fn(tx, v); err != nil {
		return restoreError(err)
	}
	return restoreError(tx.Commit())
}
func resumeLoad(ctx context.Context, tx *sql.Tx, r app.ResumeRequest) (app.ResumeIntent, app.ResumeResult, error) {
	var i app.ResumeIntent
	var result app.ResumeResult
	var raw, res []byte
	var digest, idigest string
	err := tx.QueryRowContext(ctx, `SELECT request_digest,intent,intent_digest,result FROM session_resume_operations WHERE tenant_id=$1 AND operation_id=$2 AND session_id=$3 FOR UPDATE`, r.Caller.TenantID, r.OperationID, r.SessionID).Scan(&digest, &raw, &idigest, &res)
	if err != nil {
		return i, result, err
	}
	if digest != r.Digest() {
		return i, result, workspace.ErrConflict
	}
	if json.Unmarshal(raw, &i) != nil || i.Digest() != idigest || i.Request.Digest() != digest || json.Unmarshal(res, &result) != nil {
		return i, result, workspace.ErrIntegrity
	}
	return i, result, nil
}
func cloneResume(i app.ResumeIntent) app.ResumeIntent {
	raw, _ := json.Marshal(i)
	var out app.ResumeIntent
	_ = json.Unmarshal(raw, &out)
	return out
}

func (s *SessionResumes) Prepare(ctx context.Context, r app.ResumeRequest) (app.ResumeIntent, app.ResumeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	var i app.ResumeIntent
	var result app.ResumeResult
	if err := r.Validate(); err != nil {
		return i, result, err
	}
	if s.access(ctx, r) != nil {
		return i, result, workspace.ErrUnavailable
	}
	err := s.transaction(ctx, r.Caller.TenantID, r.SessionID, func(tx *sql.Tx, v resumeSession) error {
		var err error
		i, result, err = resumeLoad(ctx, tx, r)
		if err == nil {
			if result.State != "RESUMING" {
				return nil
			}
			if err = s.fence(ctx, tx, i, v); err != nil {
				return err
			}
			if s.policy(ctx, cloneResume(i)) != nil {
				return workspace.ErrUnavailable
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if v.state != "SUSPENDED" || v.version != r.ExpectedVersion || v.current != "" || v.head != string(r.CheckpointID) {
			return workspace.ErrConflict
		}
		i = app.ResumeIntent{Request: r, Version: v.version + 1, ExecutionGeneration: v.generation, Deadline: time.Now().UTC().Add(15 * time.Minute)}
		// Only the exact SES-004 boundary is admitted; older restore is a different operation.
		if err = tx.QueryRowContext(ctx, `SELECT prior_state FROM session_suspend_operations WHERE tenant_id=$1 AND session_id=$2 AND checkpoint_id=$3 AND state_version=$4 AND execution_generation=$5`, r.Caller.TenantID, r.SessionID, r.CheckpointID, v.version, v.generation).Scan(&i.TargetState); err != nil {
			return workspace.ErrConflict
		}
		var attachment, state, provider string
		if err = tx.QueryRowContext(ctx, `SELECT w.workspace_id,w.current_workspace_generation_id,w.current_generation,w.state,COALESCE(w.current_attachment_id::text,''),w.provider_kind FROM workspaces w JOIN checkpoints c ON c.tenant_id=w.tenant_id AND c.workspace_id=w.workspace_id AND c.workspace_generation_id=w.current_workspace_generation_id AND c.workspace_generation=w.current_generation WHERE w.tenant_id=$1 AND w.session_id=$2 AND c.checkpoint_id=$3 FOR UPDATE OF w`, r.Caller.TenantID, r.SessionID, r.CheckpointID).Scan(&i.WorkspaceID, &i.WorkspaceGenerationID, &i.WorkspaceGeneration, &state, &attachment, &provider); err != nil {
			return err
		}
		if state != "READY" || attachment != "" || provider != "kubernetes" {
			return workspace.ErrConflict
		}
		if err = resumeNoWriters(ctx, tx, r); err != nil {
			return err
		}
		restored, err := validateCheckpointTx(ctx, tx, s.validator, r.Caller.TenantID, r.SessionID, r.CheckpointID)
		if err != nil {
			return err
		}
		i.Manifest = restored.Manifest
		b := &i.Runtime
		if err = tx.QueryRowContext(ctx, `SELECT authority_mode,authority_namespace,agent_id,agent_version_id,runtime_spec_schema_version,runtime_spec,runtime_spec_digest,runtime_profile_schema_version,runtime_profile_snapshot,runtime_profile_digest FROM sessions WHERE tenant_id=$1 AND session_id=$2`, r.Caller.TenantID, r.SessionID).Scan(&b.AuthorityMode, &b.AuthorityNamespace, &b.AgentID, &b.AgentVersionID, &b.RuntimeSpecSchemaVersion, &b.RuntimeSpec, &b.RuntimeSpecDigest, &b.RuntimeProfileSchemaVersion, &b.RuntimeProfileSnapshot, &b.RuntimeProfileDigest); err != nil {
			return err
		}
		for _, id := range []*primitives.ID{&i.SandboxID, &i.BootstrapID, &i.AttachmentID} {
			*id, err = primitives.NewID(time.Now())
			if err != nil {
				return err
			}
		}
		if s.policy(ctx, cloneResume(i)) != nil {
			return workspace.ErrUnavailable
		}
		result = resumeResult(i, "RESUMING", i.Version)
		raw, _ := json.Marshal(i)
		res, _ := json.Marshal(result)
		if _, err = tx.ExecContext(ctx, `INSERT INTO session_resume_operations(tenant_id,operation_id,session_id,checkpoint_id,sandbox_id,bootstrap_id,attachment_id,request_digest,intent,intent_digest,state,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'RESUMING',$11)`, r.Caller.TenantID, r.OperationID, r.SessionID, r.CheckpointID, i.SandboxID, i.BootstrapID, i.AttachmentID, r.Digest(), raw, i.Digest(), res); err != nil {
			return err
		}
		// Reserve the single writer before any provider call. This is an infrastructure
		// attachment reservation, not permission for user work or a physical observation.
		if _, err = tx.ExecContext(ctx, `UPDATE workspaces SET current_attachment_id=$3,state='ATTACHED',state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2`, r.Caller.TenantID, i.WorkspaceID, i.AttachmentID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE sessions SET state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND session_id=$2`, r.Caller.TenantID, r.SessionID); err != nil {
			return err
		}
		return resumeEvent(ctx, tx, i, result, "SUSPENDED", "RESUME_PREPARED")
	})
	return i, result, err
}
func resumeNoWriters(ctx context.Context, tx *sql.Tx, r app.ResumeRequest) error {
	var busy bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM executions WHERE tenant_id=$1 AND session_id=$2 AND state IN ('QUEUED','MATERIALIZING','RUNNING','CANCELLING','TIMING_OUT')) OR EXISTS(SELECT 1 FROM sandbox_bindings WHERE tenant_id=$1 AND session_id=$2 AND state<>'RELEASED')`, r.Caller.TenantID, r.SessionID).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return workspace.ErrConflict
	}
	return resumeCredentialsRetired(ctx, tx, r)
}

// The caller holds the Session lock, also required by credential registration
// and connection admission. Released bindings fence retained certificate history;
// cleanup_requested alone is not proof that a bootstrap Secret is gone.
// External gateway/AG revocation and restored-content exclusions remain mandatory
// ResumePolicy/ResumeReady checks, not facts inferred from this local registry.
func resumeCredentialsRetired(ctx context.Context, tx *sql.Tx, r app.ResumeRequest) error {
	var unsafe bool
	err := tx.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM agentd_credential_state c
 JOIN sandbox_bindings b USING(tenant_id,sandbox_binding_id)
 WHERE b.tenant_id=$1 AND b.session_id=$2 AND c.connection_id IS NOT NULL)
 OR EXISTS(SELECT 1 FROM agentd_bootstrap_delivery d
 JOIN agentd_credentials c USING(tenant_id,credential_id)
 JOIN sandbox_bindings b USING(tenant_id,sandbox_binding_id)
 WHERE b.tenant_id=$1 AND b.session_id=$2 AND NOT d.cleaned
 AND GREATEST(d.expires_at,c.expires_at)>clock_timestamp())`, r.Caller.TenantID, r.SessionID).Scan(&unsafe)
	if err != nil {
		return err
	}
	if unsafe {
		return workspace.ErrConflict
	}
	return nil
}
func (s *SessionResumes) fence(ctx context.Context, tx *sql.Tx, i app.ResumeIntent, v resumeSession) error {
	r := i.Request
	if v.state != "SUSPENDED" || v.version != i.Version || v.generation != i.ExecutionGeneration || v.head != string(r.CheckpointID) || v.current != "" {
		return workspace.ErrConflict
	}
	if err := resumeNoWriters(ctx, tx, r); err != nil {
		return err
	}
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT state='ATTACHED' AND current_attachment_id=$3 AND current_workspace_generation_id=$4 AND current_generation=$5 FROM workspaces WHERE tenant_id=$1 AND workspace_id=$2 AND session_id=$6 FOR UPDATE`, r.Caller.TenantID, i.WorkspaceID, i.AttachmentID, i.WorkspaceGenerationID, i.WorkspaceGeneration, r.SessionID).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return workspace.ErrConflict
	}
	checked, err := validateCheckpointTx(ctx, tx, s.validator, r.Caller.TenantID, r.SessionID, r.CheckpointID)
	if err != nil {
		return err
	}
	if !bytes.Equal(checked.Manifest, i.Manifest) {
		return workspace.ErrIntegrity
	}
	return nil
}
func resumeResult(i app.ResumeIntent, state string, version int64) app.ResumeResult {
	return app.ResumeResult{OperationID: i.Request.OperationID, SessionID: i.Request.SessionID, CheckpointID: i.Request.CheckpointID, SandboxID: i.SandboxID, BootstrapID: i.BootstrapID, AttachmentID: i.AttachmentID, State: state, Version: version, Generation: i.ExecutionGeneration}
}
func (s *SessionResumes) Complete(ctx context.Context, r app.ResumeRequest, o app.ResumeObservation) (app.ResumeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	var result app.ResumeResult
	if err := r.Validate(); err != nil {
		return result, err
	}
	if s.access(ctx, r) != nil {
		return result, workspace.ErrUnavailable
	}
	err := s.transaction(ctx, r.Caller.TenantID, r.SessionID, func(tx *sql.Tx, v resumeSession) error {
		i, saved, err := resumeLoad(ctx, tx, r)
		if err != nil {
			return err
		}
		result = saved
		if result.State != "RESUMING" {
			return nil
		}
		if o.Validate() != nil {
			return workspace.ErrIntegrity
		}
		if err = s.fence(ctx, tx, i, v); err != nil {
			return err
		}
		if !time.Now().Before(i.Deadline) {
			return workspace.ErrConflict
		}
		if s.policy(ctx, cloneResume(i)) != nil {
			return workspace.ErrUnavailable
		}
		var reused bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox_bindings WHERE tenant_id=$1 AND provider_reference=$2) OR EXISTS(SELECT 1 FROM session_resume_operations WHERE tenant_id=$1 AND observation IS NOT NULL AND convert_from(observation,'UTF8')::jsonb->>'SandboxReference'=$2)`, r.Caller.TenantID, o.SandboxReference).Scan(&reused); err != nil {
			return err
		}
		if reused {
			return workspace.ErrIntegrity
		}
		if err = s.ready(ctx, cloneResume(i), o); err != nil {
			// A bounded provider/readiness outage is not evidence of corrupt
			// restored state. Retain this candidate for exact reconciliation.
			if errors.Is(err, workspace.ErrUnavailable) {
				return workspace.ErrUnavailable
			}
			return workspace.ErrIntegrity
		}
		result = resumeResult(i, i.TargetState, v.version+1)
		raw, _ := json.Marshal(result)
		proof, _ := json.Marshal(o)
		if _, err = tx.ExecContext(ctx, `UPDATE session_resume_operations SET state=$3,result=$4,observation=$5 WHERE tenant_id=$1 AND operation_id=$2`, r.Caller.TenantID, r.OperationID, result.State, raw, proof); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE sessions SET state=$3,state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND session_id=$2`, r.Caller.TenantID, r.SessionID, result.State); err != nil {
			return err
		}
		return resumeEvent(ctx, tx, i, result, "SUSPENDED", "RESUME_COMMITTED")
	})
	return result, err
}

// Fail abandons only this candidate. It never changes a newer/closing Session.
// Worker authorization comes from the existing exact durable reservation.
func (s *SessionResumes) Fail(ctx context.Context, r app.ResumeRequest) (app.ResumeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	var result app.ResumeResult
	if err := r.Validate(); err != nil {
		return result, err
	}
	err := s.transaction(ctx, r.Caller.TenantID, r.SessionID, func(tx *sql.Tx, v resumeSession) error {
		i, saved, err := resumeLoad(ctx, tx, r)
		if err != nil {
			return err
		}
		result = saved
		if result.State != "RESUMING" {
			return nil
		}
		version := v.version
		if v.state == "SUSPENDED" && v.version == i.Version && v.generation == i.ExecutionGeneration {
			if _, err = tx.ExecContext(ctx, `UPDATE sessions SET state='DEGRADED',recovery_state='SUSPENDED',state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND session_id=$2`, r.Caller.TenantID, r.SessionID); err != nil {
				return err
			}
			version++
		}
		result = resumeResult(i, "DEGRADED", version)
		raw, _ := json.Marshal(result)
		if _, err = tx.ExecContext(ctx, `UPDATE session_resume_operations SET state='DEGRADED',result=$3 WHERE tenant_id=$1 AND operation_id=$2`, r.Caller.TenantID, r.OperationID, raw); err != nil {
			return err
		}
		// The journal is the cleanup intent even when the provider response/UID was lost.
		return resumeEvent(ctx, tx, i, result, v.state, "RESUME_ABANDONED")
	})
	return result, err
}
func (s *SessionResumes) CleanupIntent(ctx context.Context, tenant, operation primitives.ID) (app.ResumeIntent, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	var i app.ResumeIntent
	var done bool
	var raw []byte
	var digest, state string
	for _, id := range []primitives.ID{tenant, operation} {
		if _, err := primitives.ParseID(string(id)); err != nil {
			return i, false, workspace.ErrInvalid
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return i, false, restoreError(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, tenant); err != nil {
		return i, false, restoreError(err)
	}
	err = tx.QueryRowContext(ctx, `SELECT intent,intent_digest,state,cleaned FROM session_resume_operations WHERE tenant_id=$1 AND operation_id=$2`, tenant, operation).Scan(&raw, &digest, &state, &done)
	if err != nil {
		return i, false, restoreError(err)
	}
	if state != "DEGRADED" {
		return i, false, workspace.ErrConflict
	}
	if json.Unmarshal(raw, &i) != nil || i.Digest() != digest || i.Request.Caller.TenantID != tenant || i.Request.OperationID != operation {
		return i, false, workspace.ErrIntegrity
	}
	return i, done, nil
}
func (s *SessionResumes) Cleaned(ctx context.Context, tenant, operation primitives.ID) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	i, done, err := s.CleanupIntent(ctx, tenant, operation)
	if err != nil || done {
		return err
	}
	return s.transaction(ctx, tenant, i.Request.SessionID, func(tx *sql.Tx, _ resumeSession) error {
		var cleaned bool
		if err := tx.QueryRowContext(ctx, `SELECT cleaned FROM session_resume_operations WHERE tenant_id=$1 AND operation_id=$2 FOR UPDATE`, tenant, operation).Scan(&cleaned); err != nil {
			return err
		}
		if cleaned {
			return nil
		}
		// Physical absence was independently confirmed; retain degraded storage until
		// a recovery path verifies the durable head. Never clear a newer attachment.
		if _, err := tx.ExecContext(ctx, `UPDATE workspaces SET current_attachment_id=NULL,state='DEGRADED',state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND current_attachment_id=$3 AND state='ATTACHED'`, tenant, i.WorkspaceID, i.AttachmentID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE session_resume_operations SET cleaned=true WHERE tenant_id=$1 AND operation_id=$2`, tenant, operation)
		return err
	})
}
func resumeEvent(ctx context.Context, tx *sql.Tx, i app.ResumeIntent, result app.ResumeResult, prior, reason string) error {
	r := i.Request
	id, err := primitives.NewID(time.Now())
	if err != nil {
		return err
	}
	// RESUMING is exposed in operation_state, not invented as a Session enum value.
	state := result.State
	if state == "RESUMING" {
		state = "SUSPENDED"
	}
	if reason == "RESUME_ABANDONED" {
		if err = tx.QueryRowContext(ctx, `SELECT state FROM sessions WHERE tenant_id=$1 AND session_id=$2`, r.Caller.TenantID, r.SessionID).Scan(&state); err != nil {
			return err
		}
	}
	payload, _ := json.Marshal(map[string]any{"operation_id": r.OperationID, "checkpoint_id": r.CheckpointID, "sandbox_id": i.SandboxID, "bootstrap_id": i.BootstrapID, "previous_state": prior, "state": state, "operation_state": result.State, "reason_code": reason, "state_version": result.Version})
	if _, err = tx.ExecContext(ctx, `INSERT INTO runtime_event_streams(tenant_id,session_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, r.Caller.TenantID, r.SessionID); err != nil {
		return err
	}
	var seq int64
	if err = tx.QueryRowContext(ctx, `SELECT last_sequence+1 FROM runtime_event_streams WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, r.Caller.TenantID, r.SessionID).Scan(&seq); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO runtime_events(tenant_id,event_id,session_id,sequence,aggregate_version,schema_version,event_type,occurred_at,source,classification,payload,retention_policy) VALUES($1,$2,$3,$4,$5,'thinkpixel.runtime-event/v1','session.state_changed',CURRENT_TIMESTAMP,'agent-runtime','Internal',$6,'runtime-control')`, r.Caller.TenantID, id, r.SessionID, seq, result.Version, payload); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO outbox_messages(tenant_id,message_id,topic,schema_version,event_id,aggregate_type,aggregate_id,aggregate_version,payload,payload_digest,state,available_at,created_at,updated_at) VALUES($1,$2,'session.resume','thinkpixel.session-resume/v1',$2,'session',$3,$4,$5,$6,'PENDING',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, r.Caller.TenantID, id, r.SessionID, result.Version, payload, workspace.EvidenceDigest(payload))
	return err
}
