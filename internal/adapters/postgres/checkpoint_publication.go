package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	app "github.com/bdobrica/ThinkPixelAR/internal/app/checkpoint"
	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/checkpoint"
	port "github.com/bdobrica/ThinkPixelAR/internal/ports/checkpoint"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// PublicationSource is locked AR metadata, not a harness attestation.
type PublicationSource struct {
	RuntimeSpec, RuntimeProfile json.RawMessage
	Proof                       workspace.CheckpointProof
	EvidenceDigest              string
}

// PublicationAccess must authorize publication or historical disclosure against
// current policy. Request fields confer no authority.
type PublicationAccess func(context.Context, port.Request) error

// PublicationVerifier independently verifies immutable Workspace AND all required
// vendor objects, byte counts/digests, source/consistency, exact runtime/adapter/
// protocol compatibility, and credential exclusions/canaries. It must also pin
// objects against deletion under the requested retention policy before returning.
// It receives locked source metadata; callbacks must honor context and must not
// mutate these rows. No default verifier is provided.
type PublicationVerifier func(context.Context, port.Request, PublicationSource) error

type Checkpoints struct {
	db     *sql.DB
	access PublicationAccess
	verify PublicationVerifier
	signer app.Signer
}

var _ port.Publisher = (*Checkpoints)(nil)

func NewCheckpoints(db *sql.DB, access PublicationAccess, verify PublicationVerifier, signer app.Signer) (*Checkpoints, error) {
	if db == nil || access == nil || verify == nil || signer == nil {
		return nil, workspace.ErrInvalid
	}
	return &Checkpoints{db, access, verify, signer}, nil
}
func (s *Checkpoints) Publish(ctx context.Context, r port.Request) (port.Result, error) {
	var result port.Result
	// Own request slices before invoking trusted callbacks.
	r.VendorState = append([]port.VendorObject(nil), r.VendorState...)
	b, w := r.Binding, r.Boundary
	if len(r.VendorState) > 64 || w.Validate() != nil || b.SessionID != w.SessionID || b.WorkspaceID != w.WorkspaceID || b.WorkspaceGenerationID != w.Operation.ID || b.WorkspaceGeneration != uint64(w.ParentGeneration+1) || b.Purpose != domain.PurposeCheckpoint || b.OperationID != r.ID || r.ID == w.Operation.ID {
		return result, workspace.ErrInvalid
	}
	if _, e := primitives.ParseID(string(r.ID)); e != nil {
		return result, workspace.ErrInvalid
	}
	if _, e := domain.New(w.TenantID, r.ID, b, time.Now()); e != nil {
		return result, workspace.ErrInvalid
	}
	requestDigest := r.Digest()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	journal := WorkspaceStorage{db: s.db}
	err := journal.journal(ctx, w.TenantID, func(tx *sql.Tx) error {
		var state, current, parent, specDigest, profileDigest string
		var epoch, version int64
		var source PublicationSource
		if e := tx.QueryRowContext(ctx, `SELECT state,COALESCE(current_execution_id::text,''),execution_generation,state_version,COALESCE(current_checkpoint_id::text,''),runtime_spec,runtime_spec_digest,runtime_profile_snapshot,runtime_profile_digest FROM sessions WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, w.TenantID, w.SessionID).Scan(&state, &current, &epoch, &version, &parent, &source.RuntimeSpec, &specDigest, &source.RuntimeProfile, &profileDigest); e != nil {
			return e
		}
		accessRequest := r
		accessRequest.VendorState = append([]port.VendorObject(nil), r.VendorState...)
		if s.access(ctx, accessRequest) != nil {
			return workspace.ErrUnavailable
		}
		var savedDigest, savedState, savedSession string
		var raw []byte
		e := tx.QueryRowContext(ctx, `SELECT publication_request_digest,state,session_id,canonical_manifest FROM checkpoints WHERE tenant_id=$1 AND operation_id=$2`, w.TenantID, b.OperationID).Scan(&savedDigest, &savedState, &savedSession, &raw)
		if e == nil {
			if savedDigest != requestDigest || savedState != "COMMITTED" || savedSession != string(w.SessionID) {
				return workspace.ErrConflict
			}
			result = port.Result{ID: r.ID, Manifest: raw}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if epoch != w.SessionGeneration || parent != string(b.ParentCheckpointID) || app.Digest(specDigest) != b.RuntimeSpecDigest || app.Digest(profileDigest) != b.RuntimeProfileDigest {
			return workspace.ErrConflict
		}
		var workspaceState, attachment, generationID, provider, configDigest string
		var generation int64
		if e = tx.QueryRowContext(ctx, `SELECT state,COALESCE(current_attachment_id::text,''),COALESCE(current_workspace_generation_id::text,''),current_generation,provider_kind,config_digest FROM workspaces WHERE tenant_id=$1 AND workspace_id=$2 AND session_id=$3 FOR UPDATE`, w.TenantID, w.WorkspaceID, w.SessionID).Scan(&workspaceState, &attachment, &generationID, &generation, &provider, &configDigest); e != nil {
			return e
		}
		if configDigest != w.ConfigurationDigest || provider != "kubernetes" || generationID != string(b.WorkspaceGenerationID) || generation != int64(b.WorkspaceGeneration) {
			return workspace.ErrConflict
		}
		if w.ExecutionID == "" {
			if (state != "READY" && state != "IDLE") || current != "" || attachment != "" || workspaceState != "READY" {
				return workspace.ErrConflict
			}
		} else {
			if state != "ACTIVE" || current != string(w.ExecutionID) || attachment != string(w.AttachmentID) || workspaceState != "ATTACHED" {
				return workspace.ErrConflict
			}
			var executionState, attemptState string
			var isCurrent bool
			if e = tx.QueryRowContext(ctx, `SELECT state FROM executions WHERE tenant_id=$1 AND execution_id=$2 AND session_id=$3 AND session_generation=$4 FOR UPDATE`, w.TenantID, w.ExecutionID, w.SessionID, epoch).Scan(&executionState); e != nil {
				return e
			}
			if e = tx.QueryRowContext(ctx, `SELECT state,is_current FROM attempts WHERE tenant_id=$1 AND attempt_id=$2 AND execution_id=$3 AND execution_generation=$4 FOR UPDATE`, w.TenantID, w.AttemptID, w.ExecutionID, epoch).Scan(&attemptState, &isCurrent); e != nil {
				return e
			}
			if executionState != "RUNNING" || attemptState != "RUNNING" || !isCurrent {
				return workspace.ErrConflict
			}
		}
		// The exact WSP-003 journal proves generation publication completed; JSONB is
		// not used to reconstruct the proof's original byte representation.
		var oldRequest, proof []byte
		var opState, digest string
		if e = tx.QueryRowContext(ctx, `SELECT request,state,proof,proof_digest FROM workspace_checkpoint_operations WHERE tenant_id=$1 AND operation_id=$2 AND workspace_id=$3 AND session_id=$4`, w.TenantID, w.Operation.ID, w.WorkspaceID, w.SessionID).Scan(&oldRequest, &opState, &proof, &digest); e != nil {
			return e
		}
		var saved workspace.CheckpointRequest
		if opState != "COMMITTED" || json.Unmarshal(oldRequest, &saved) != nil || saved != w || workspace.EvidenceDigest(proof) != digest || json.Unmarshal(proof, &source.Proof) != nil || source.Proof.Validate() != nil {
			return workspace.ErrIntegrity
		}
		if e = tx.QueryRowContext(ctx, `SELECT storage_evidence_digest FROM workspace_generations WHERE tenant_id=$1 AND workspace_generation_id=$2 AND workspace_id=$3 AND session_id=$4 AND generation=$5`, w.TenantID, w.Operation.ID, w.WorkspaceID, w.SessionID, generation).Scan(&source.EvidenceDigest); e != nil {
			return e
		}
		if source.EvidenceDigest != digest {
			return workspace.ErrIntegrity
		}
		if parent != "" {
			var parentState string
			if e = tx.QueryRowContext(ctx, `SELECT state FROM checkpoints WHERE tenant_id=$1 AND checkpoint_id=$2 AND session_id=$3 FOR SHARE`, w.TenantID, parent, w.SessionID).Scan(&parentState); e != nil {
				return e
			}
			if parentState != "COMMITTED" {
				return workspace.ErrConflict
			}
		}
		checked := source
		checked.RuntimeSpec = append(json.RawMessage(nil), source.RuntimeSpec...)
		checked.RuntimeProfile = append(json.RawMessage(nil), source.RuntimeProfile...)
		checked.Proof.Evidence = append(json.RawMessage(nil), source.Proof.Evidence...)
		verifiedRequest := r
		verifiedRequest.VendorState = append([]port.VendorObject(nil), r.VendorState...)
		if s.verify(ctx, verifiedRequest, checked) != nil {
			return workspace.ErrIntegrity
		}
		var now time.Time
		if e = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
			return e
		}
		i, e := app.Build(ctx, r, source.Proof, source.EvidenceDigest, now, s.signer)
		if e != nil {
			return e
		}
		c, e := domain.New(w.TenantID, r.ID, b, now)
		if e != nil {
			return workspace.ErrInvalid
		}
		if e = c.Commit(i, 0, now); e != nil {
			return workspace.ErrIntegrity
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO checkpoints(tenant_id,checkpoint_id,session_id,workspace_id,workspace_generation_id,workspace_generation,operation_id,parent_checkpoint_id,lineage_purpose,state,state_version,runtime_spec_id,runtime_spec_digest,adapter_kind,adapter_version,adapter_build_digest,protocol_name,protocol_version,state_format_name,state_format_version,runtime_profile_digest,canonical_manifest,vendor_state,exclusions,canonicalization,digest_algorithm,payload_digest,composite_root,signature_algorithm,signer,key_id,signature,signed_at,retention_disposition,created_at,updated_at,committed_at,publication_request_digest) VALUES($1,$2,$3,$4,$5,$6,$2,$7,'checkpoint','COMMITTED',1,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$29,$29,$29,$31)`, w.TenantID, r.ID, w.SessionID, w.WorkspaceID, b.WorkspaceGenerationID, b.WorkspaceGeneration, nullID(b.ParentCheckpointID), b.RuntimeSpecID, b.RuntimeSpecDigest, b.AdapterKind, b.AdapterVersion, b.AdapterBuildDigest, b.ProtocolName, b.ProtocolVersion, b.StateFormatName, b.StateFormatVersion, b.RuntimeProfileDigest, i.CanonicalManifest, i.VendorState, i.Exclusions, i.Canonicalization, i.DigestAlgorithm, i.PayloadDigest, i.CompositeRoot, i.SignatureAlgorithm, i.Signer, i.KeyID, i.Signature, now, b.RetentionDisposition, requestDigest)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE sessions SET current_checkpoint_id=$3,state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND session_id=$2`, w.TenantID, w.SessionID, r.ID); e != nil {
			return e
		}
		if e = publishedCheckpointEvent(ctx, tx, r, i, version+1); e != nil {
			return e
		}
		result = port.Result{ID: r.ID, Manifest: append([]byte(nil), i.CanonicalManifest...)}
		return nil
	})
	if err != nil {
		return port.Result{}, err
	}
	return result, nil
}

func publishedCheckpointEvent(ctx context.Context, tx *sql.Tx, r port.Request, i domain.Integrity, version int64) error {
	w := r.Boundary
	payload, _ := json.Marshal(map[string]any{"checkpoint_id": r.ID, "operation_id": r.Binding.OperationID, "workspace_id": w.WorkspaceID, "workspace_generation_id": w.Operation.ID, "generation": r.Binding.WorkspaceGeneration, "payload_digest": i.PayloadDigest, "composite_root": i.CompositeRoot})
	if _, e := tx.ExecContext(ctx, `INSERT INTO runtime_event_streams(tenant_id,session_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, w.TenantID, w.SessionID); e != nil {
		return e
	}
	var seq int64
	if e := tx.QueryRowContext(ctx, `SELECT last_sequence FROM runtime_event_streams WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, w.TenantID, w.SessionID).Scan(&seq); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `INSERT INTO runtime_events(tenant_id,event_id,session_id,execution_id,attempt_id,sequence,aggregate_version,schema_version,event_type,occurred_at,source,classification,payload,retention_policy) VALUES($1,$2,$3,$4,$5,$6,$7,'thinkpixel.runtime-event/v1','checkpoint.committed',CURRENT_TIMESTAMP,'agent-runtime','Confidential',$8,$9)`, w.TenantID, r.ID, w.SessionID, nullID(w.ExecutionID), nullID(w.AttemptID), seq+1, version, payload, r.Binding.RetentionDisposition); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO outbox_messages(tenant_id,message_id,topic,schema_version,event_id,aggregate_type,aggregate_id,aggregate_version,payload,payload_digest,state,available_at,created_at,updated_at) VALUES($1,$2,'checkpoint.committed','thinkpixel.checkpoint-event/v1',$2,'session',$3,$4,$5,$6,'PENDING',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, w.TenantID, r.ID, w.SessionID, version, payload, workspace.EvidenceDigest(payload))
	return e
}
