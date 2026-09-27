package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	app "github.com/bdobrica/ThinkPixelAR/internal/app/checkpoint"
	port "github.com/bdobrica/ThinkPixelAR/internal/ports/checkpoint"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// RestoreAccess performs current authorization and revocation checks for this
// exact tenant/Session/checkpoint. The selected checkpoint grants no authority.
type RestoreAccess func(context.Context, primitives.ID, primitives.ID, primitives.ID) error

type CheckpointRestores struct {
	db        *sql.DB
	access    RestoreAccess
	validator *app.RestoreValidator
}

func NewCheckpointRestores(db *sql.DB, access RestoreAccess, validator *app.RestoreValidator) (*CheckpointRestores, error) {
	if db == nil || access == nil || validator == nil {
		return nil, workspace.ErrInvalid
	}
	return &CheckpointRestores{db, access, validator}, nil
}

// Validate selects persisted metadata under tenant isolation and shared locks,
// excluding concurrent Session/Workspace/checkpoint mutation during validation.
// Physical retention pins are the validator adapters' responsibility. Returned
// bytes are NOT admission: the resume coordinator must recheck authority, lifecycle
// and execution fences and consume the same pinned objects before starting work.
// This read-only operation does not transition a Session or issue credentials.
func (s *CheckpointRestores) Validate(ctx context.Context, tenant, session, checkpoint primitives.ID) (port.Result, error) {
	for _, id := range []primitives.ID{tenant, session, checkpoint} {
		if _, err := primitives.ParseID(string(id)); err != nil {
			return port.Result{}, workspace.ErrInvalid
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if s.access(ctx, tenant, session, checkpoint) != nil {
		return port.Result{}, workspace.ErrUnavailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return port.Result{}, storageDBError(err)
	}
	defer tx.Rollback()
	fail := func(e error) (port.Result, error) {
		return port.Result{}, restoreError(e)
	}
	if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, tenant); err != nil {
		return fail(err)
	}
	result, err := validateCheckpointTx(ctx, tx, s.validator, tenant, session, checkpoint)
	if err != nil {
		return fail(err)
	}
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	return result, nil
}

// Acquire or retain Session, Workspace and checkpoint locks through the caller's
// commit so suspend cannot use a stale validation result.
func validateCheckpointTx(ctx context.Context, tx *sql.Tx, validator *app.RestoreValidator, tenant, session, checkpoint primitives.ID) (port.Result, error) {
	var err error
	var spec, profile string
	if err = tx.QueryRowContext(ctx, `SELECT runtime_spec_digest,runtime_profile_digest FROM sessions WHERE tenant_id=$1 AND session_id=$2 FOR SHARE`, tenant, session).Scan(&spec, &profile); err != nil {
		return port.Result{}, restoreError(err)
	}
	// Session first, then Workspace, then checkpoint: consistent with publication.
	var workspaceID primitives.ID
	if err = tx.QueryRowContext(ctx, `SELECT workspace_id FROM checkpoints WHERE tenant_id=$1 AND session_id=$2 AND checkpoint_id=$3`, tenant, session, checkpoint).Scan(&workspaceID); err != nil {
		return port.Result{}, restoreError(err)
	}
	var workspaceState string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM workspaces WHERE tenant_id=$1 AND workspace_id=$2 AND session_id=$3 FOR SHARE`, tenant, workspaceID, session).Scan(&workspaceState); err != nil {
		return port.Result{}, restoreError(err)
	}
	if workspaceState == "DELETING" || workspaceState == "DELETED" {
		return port.Result{}, restoreError(app.ErrInvalidRestore)
	}
	target := app.RestoreTarget{TenantID: tenant, CheckpointID: checkpoint}
	b := &target.Binding
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT state,session_id,workspace_id,workspace_generation_id,workspace_generation,operation_id,COALESCE(parent_checkpoint_id::text,''),lineage_purpose,runtime_spec_id,runtime_spec_digest,adapter_kind,adapter_version,adapter_build_digest,protocol_name,protocol_version,state_format_name,state_format_version,runtime_profile_digest,retention_disposition,canonical_manifest FROM checkpoints WHERE tenant_id=$1 AND session_id=$2 AND checkpoint_id=$3 FOR SHARE`, tenant, session, checkpoint).Scan(&target.State, &b.SessionID, &b.WorkspaceID, &b.WorkspaceGenerationID, &b.WorkspaceGeneration, &b.OperationID, &b.ParentCheckpointID, &b.Purpose, &b.RuntimeSpecID, &b.RuntimeSpecDigest, &b.AdapterKind, &b.AdapterVersion, &b.AdapterBuildDigest, &b.ProtocolName, &b.ProtocolVersion, &b.StateFormatName, &b.StateFormatVersion, &b.RuntimeProfileDigest, &b.RetentionDisposition, &raw)
	if err != nil {
		return port.Result{}, restoreError(err)
	}
	if b.RuntimeSpecDigest != app.Digest(spec) || b.RuntimeProfileDigest != app.Digest(profile) {
		return port.Result{}, restoreError(app.ErrIncompatible)
	}
	var proofBytes []byte
	var proofDigest, opState, evidence, manifestDigest string
	err = tx.QueryRowContext(ctx, `SELECT o.state,o.proof,o.proof_digest,g.storage_evidence_digest,g.manifest_digest FROM workspace_checkpoint_operations o JOIN workspace_generations g ON g.tenant_id=o.tenant_id AND g.workspace_generation_id=o.operation_id AND g.workspace_id=o.workspace_id AND g.session_id=o.session_id WHERE g.tenant_id=$1 AND g.workspace_generation_id=$2 AND g.workspace_id=$3 AND g.session_id=$4 AND g.generation=$5 FOR SHARE OF o,g`, tenant, b.WorkspaceGenerationID, b.WorkspaceID, session, b.WorkspaceGeneration).Scan(&opState, &proofBytes, &proofDigest, &evidence, &manifestDigest)
	if err != nil {
		return port.Result{}, restoreError(err)
	}
	var proof workspace.CheckpointProof
	if opState != "COMMITTED" || workspace.EvidenceDigest(proofBytes) != proofDigest || proofDigest != evidence || json.Unmarshal(proofBytes, &proof) != nil || proof.Validate() != nil || proof.ManifestDigest != manifestDigest {
		return port.Result{}, restoreError(app.ErrInvalidRestore)
	}
	target.Workspace = app.WorkspaceManifest{ID: b.WorkspaceID, GenerationID: b.WorkspaceGenerationID, Generation: b.WorkspaceGeneration, Snapshot: proof.SnapshotReference, Root: app.Digest(proof.ManifestDigest), Algorithm: "sha-256", EvidenceDigest: app.Digest(evidence)}
	if err = validator.Validate(ctx, raw, target); err != nil {
		return port.Result{}, restoreError(err)
	}
	return port.Result{ID: checkpoint, Manifest: raw}, nil
}

func restoreError(err error) error {
	if errors.Is(err, app.ErrInvalidRestore) || errors.Is(err, app.ErrIncompatible) {
		return err
	}
	return storageDBError(err)
}
