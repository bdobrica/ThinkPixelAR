package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
)

// CheckpointAccess performs current authorization (including replay disclosure).
// Abort requires cleanup permission; it does not require renewed execution authority.
type CheckpointAccess func(context.Context, string, workspace.CheckpointRequest) error

// CheckpointBoundary is read from locked authoritative metadata, never a harness.
type CheckpointBoundary struct{ ProviderKind, ProviderReference, StorageProfile, ConfigurationDigest string }

// CheckpointVerifier must independently prove the exact source is quiesced and
// writes remain blocked through commit. At publish it also verifies immutable
// provider readiness, integrity, both Workspace/vendor-state durability and
// runtime compatibility. A harness report or provider ready flag alone is not
// sufficient. It must not mutate the locked database rows or unblock writes.
type CheckpointVerifier func(context.Context, string, workspace.CheckpointRequest, CheckpointBoundary, workspace.CheckpointProof) error

type WorkspaceCheckpoints struct {
	db     *sql.DB
	access CheckpointAccess
	verify CheckpointVerifier
}

var _ workspace.CheckpointPublisher = (*WorkspaceCheckpoints)(nil)

func NewWorkspaceCheckpoints(db *sql.DB, access CheckpointAccess, verify CheckpointVerifier) (*WorkspaceCheckpoints, error) {
	if db == nil || access == nil || verify == nil {
		return nil, workspace.ErrInvalid
	}
	return &WorkspaceCheckpoints{db, access, verify}, nil
}
func (s *WorkspaceCheckpoints) Prepare(ctx context.Context, r workspace.CheckpointRequest) (workspace.CheckpointResult, error) {
	return s.change(ctx, "prepare", r, workspace.CheckpointProof{})
}
func (s *WorkspaceCheckpoints) Publish(ctx context.Context, r workspace.CheckpointRequest, p workspace.CheckpointProof) (workspace.CheckpointResult, error) {
	if err := p.Validate(); err != nil {
		return workspace.CheckpointResult{}, err
	}
	// Own a copy across callbacks; callers cannot replace proof bytes mid-publication.
	p.Evidence = append(json.RawMessage(nil), p.Evidence...)
	return s.change(ctx, "publish", r, p)
}
func (s *WorkspaceCheckpoints) Abort(ctx context.Context, r workspace.CheckpointRequest) error {
	_, err := s.change(ctx, "abort", r, workspace.CheckpointProof{})
	return err
}

func (s *WorkspaceCheckpoints) change(ctx context.Context, action string, r workspace.CheckpointRequest, p workspace.CheckpointProof) (workspace.CheckpointResult, error) {
	result := workspace.CheckpointResult{}
	if err := r.Validate(); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	journal := WorkspaceStorage{db: s.db}
	err := journal.journal(ctx, r.TenantID, func(tx *sql.Tx) error {
		// Match admission's lock order. These locks also serialize event allocation.
		var sessionState, currentExecution string
		var epoch int64
		if err := tx.QueryRowContext(ctx, `SELECT state,COALESCE(current_execution_id::text,''),execution_generation FROM sessions WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, r.TenantID, r.SessionID).Scan(&sessionState, &currentExecution, &epoch); err != nil {
			return err
		}
		var state, attachment, parent string
		var generation, version sql.NullInt64
		var boundary CheckpointBoundary
		if err := tx.QueryRowContext(ctx, `SELECT state,COALESCE(current_attachment_id::text,''),COALESCE(current_workspace_generation_id::text,''),current_generation,state_version,provider_kind,provider_reference,storage_profile,config_digest FROM workspaces WHERE tenant_id=$1 AND workspace_id=$2 AND session_id=$3 FOR UPDATE`, r.TenantID, r.WorkspaceID, r.SessionID).Scan(&state, &attachment, &parent, &generation, &version, &boundary.ProviderKind, &boundary.ProviderReference, &boundary.StorageProfile, &boundary.ConfigurationDigest); err != nil {
			return err
		}
		// This publisher owns only standalone Kubernetes storage, never WS generations.
		if boundary.ProviderKind != "kubernetes" || boundary.ConfigurationDigest != r.ConfigurationDigest {
			return workspace.ErrConflict
		}
		if s.access(ctx, action, r) != nil {
			return workspace.ErrUnavailable
		}
		var saved []byte
		var opState, prior, proofDigest string
		err := tx.QueryRowContext(ctx, `SELECT request,state,prior_state,COALESCE(proof_digest,'') FROM workspace_checkpoint_operations WHERE tenant_id=$1 AND operation_id=$2`, r.TenantID, r.Operation.ID).Scan(&saved, &opState, &prior, &proofDigest)
		exists := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if exists {
			var old workspace.CheckpointRequest
			if json.Unmarshal(saved, &old) != nil || old != r {
				return workspace.ErrConflict
			}
			result = workspace.CheckpointResult{GenerationID: r.Operation.ID, Generation: r.ParentGeneration + 1, State: opState}
			if opState == "COMMITTED" {
				if action == "abort" {
					return workspace.ErrConflict
				}
				if action == "publish" {
					raw, _ := json.Marshal(p)
					if workspace.EvidenceDigest(raw) != proofDigest {
						return workspace.ErrConflict
					}
				}
				return nil // Exact historical result, even after later generation advancement.
			}
			if opState == "ABORTED" {
				if action == "abort" {
					return nil
				}
				return workspace.ErrConflict
			}
		}
		if action == "abort" {
			if !exists {
				return workspace.ErrNotFound
			}
			// Cleanup remains possible after authority/Attempt loss, but never resumes writes.
			if _, err = tx.ExecContext(ctx, `UPDATE workspace_checkpoint_operations SET state='ABORTED' WHERE tenant_id=$1 AND operation_id=$2`, r.TenantID, r.Operation.ID); err != nil {
				return err
			}
			if state == "SNAPSHOTTING" {
				_, err = tx.ExecContext(ctx, `UPDATE workspaces SET state='DEGRADED',state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND workspace_id=$2`, r.TenantID, r.WorkspaceID)
			}
			result.State = "ABORTED"
			return err
		}
		if !generation.Valid || parent != string(r.ParentID) || generation.Int64 != r.ParentGeneration || epoch != r.SessionGeneration {
			return workspace.ErrConflict
		}
		if r.ExecutionID == "" {
			if currentExecution != "" || attachment != "" || (sessionState != "READY" && sessionState != "IDLE") {
				return workspace.ErrConflict
			}
		} else {
			if sessionState != "ACTIVE" || currentExecution != string(r.ExecutionID) || attachment != string(r.AttachmentID) {
				return workspace.ErrConflict
			}
			var executionState, attemptState string
			var current bool
			if err = tx.QueryRowContext(ctx, `SELECT state FROM executions WHERE tenant_id=$1 AND execution_id=$2 AND session_id=$3 AND session_generation=$4 FOR UPDATE`, r.TenantID, r.ExecutionID, r.SessionID, r.SessionGeneration).Scan(&executionState); err != nil {
				return err
			}
			if executionState != "RUNNING" {
				return workspace.ErrConflict
			}
			if err = tx.QueryRowContext(ctx, `SELECT state,is_current FROM attempts WHERE tenant_id=$1 AND attempt_id=$2 AND execution_id=$3 AND execution_generation=$4 FOR UPDATE`, r.TenantID, r.AttemptID, r.ExecutionID, r.SessionGeneration).Scan(&attemptState, &current); err != nil {
				return err
			}
			if !current || attemptState != "RUNNING" {
				return workspace.ErrConflict
			}
		}
		if !exists {
			if action != "prepare" || (state != "READY" && state != "ATTACHED") || (state == "ATTACHED") != (r.ExecutionID != "") {
				return workspace.ErrConflict
			}
			if s.verify(ctx, "prepare", r, boundary, workspace.CheckpointProof{}) != nil {
				return workspace.ErrIntegrity
			}
			raw, _ := json.Marshal(r)
			if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_checkpoint_operations(tenant_id,operation_id,workspace_id,session_id,request,request_digest,prior_state,state) VALUES($1,$2,$3,$4,$5,$6,$7,'PREPARED')`, r.TenantID, r.Operation.ID, r.WorkspaceID, r.SessionID, raw, r.Operation.Digest, state); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE workspaces SET state='SNAPSHOTTING',state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND workspace_id=$2`, r.TenantID, r.WorkspaceID)
			result = workspace.CheckpointResult{GenerationID: r.Operation.ID, Generation: r.ParentGeneration + 1, State: "PREPARED"}
			return err
		}
		if state != "SNAPSHOTTING" {
			return workspace.ErrConflict
		}
		if action == "prepare" {
			return nil
		}
		// Validation receives an independent copy so even a verifier cannot accidentally
		// alter the evidence persisted after it has inspected it.
		checked := p
		checked.Evidence = append(json.RawMessage(nil), p.Evidence...)
		if s.verify(ctx, "publish", r, boundary, checked) != nil {
			return workspace.ErrIntegrity
		}
		raw, _ := json.Marshal(p)
		if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_generations(tenant_id,workspace_generation_id,workspace_id,session_id,generation,parent_workspace_generation_id,parent_generation,operation_id,provider_snapshot_reference,integrity_algorithm,integrity_root,manifest_digest,logical_bytes,logical_files,creator_execution_id,creator_attempt_id,creator_execution_generation,storage_evidence,storage_evidence_digest,classification,retention_disposition) VALUES($1,$2,$3,$4,$5,$6,$7,$2,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,'CONFIDENTIAL','retain')`, r.TenantID, r.Operation.ID, r.WorkspaceID, r.SessionID, r.ParentGeneration+1, r.ParentID, r.ParentGeneration, p.SnapshotReference, p.IntegrityAlgorithm, p.IntegrityRoot, p.ManifestDigest, p.LogicalBytes, p.LogicalFiles, nullID(r.ExecutionID), nullID(r.AttemptID), checkpointEpoch(r), raw, workspace.EvidenceDigest(raw)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE workspaces SET state=$3,state_version=state_version+1,current_generation=$4,current_workspace_generation_id=$5,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND workspace_id=$2`, r.TenantID, r.WorkspaceID, prior, r.ParentGeneration+1, r.Operation.ID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE workspace_checkpoint_operations SET state='COMMITTED',proof_digest=$3,proof=$4 WHERE tenant_id=$1 AND operation_id=$2`, r.TenantID, r.Operation.ID, workspace.EvidenceDigest(raw), raw); err != nil {
			return err
		}
		if err = checkpointEvent(ctx, tx, r, p, version.Int64+1); err != nil {
			return err
		}
		result.State = "COMMITTED"
		return nil
	})
	if err != nil {
		return workspace.CheckpointResult{}, err
	}
	return result, nil
}
func checkpointEpoch(r workspace.CheckpointRequest) any {
	if r.ExecutionID == "" {
		return nil
	}
	return r.SessionGeneration
}

func checkpointEvent(ctx context.Context, tx *sql.Tx, r workspace.CheckpointRequest, p workspace.CheckpointProof, version int64) error {
	// Stable operation/generation linkage is the candidate for later signed checkpoint
	// assembly. No checkpoint.committed event or restorable Checkpoint is claimed here.
	payload, _ := json.Marshal(map[string]any{"workspace_id": r.WorkspaceID, "workspace_generation_id": r.Operation.ID, "generation": r.ParentGeneration + 1, "parent_workspace_generation_id": r.ParentID, "operation_id": r.Operation.ID, "integrity_root": p.IntegrityRoot, "manifest_digest": p.ManifestDigest})
	if _, err := tx.ExecContext(ctx, `INSERT INTO runtime_event_streams(tenant_id,session_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, r.TenantID, r.SessionID); err != nil {
		return err
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT last_sequence FROM runtime_event_streams WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, r.TenantID, r.SessionID).Scan(&seq); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO runtime_events(tenant_id,event_id,session_id,execution_id,attempt_id,sequence,aggregate_version,schema_version,event_type,occurred_at,source,classification,payload,retention_policy) VALUES($1,$2,$3,$4,$5,$6,$7,'thinkpixel.runtime-event/v1','workspace.generation_committed',CURRENT_TIMESTAMP,'agent-runtime','Confidential',$8,'retain')`, r.TenantID, r.Operation.ID, r.SessionID, nullID(r.ExecutionID), nullID(r.AttemptID), seq+1, version, payload); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO outbox_messages(tenant_id,message_id,topic,schema_version,event_id,aggregate_type,aggregate_id,aggregate_version,payload,payload_digest,state,available_at,created_at,updated_at) VALUES($1,$2,'workspace.generation_committed','thinkpixel.workspace-generation/v1',$2,'workspace',$3,$4,$5,$6,'PENDING',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, r.TenantID, r.Operation.ID, r.WorkspaceID, version, payload, workspace.EvidenceDigest(payload))
	return err
}
