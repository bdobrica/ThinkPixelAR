package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// ReserveEmpty requires access to validate the saved Session provisioning intent,
// selected profile, source and capacities. Inputs are trusted worker inputs, not
// a public storage-allocation API. It never changes Session readiness/authority.
func (s *WorkspaceStorage) ReserveEmpty(ctx context.Context, r workspace.CreateRequest) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	for _, id := range []primitives.ID{r.TenantID, r.SessionID, r.WorkspaceID, r.Operation.ID} {
		if _, err := primitives.ParseID(string(id)); err != nil {
			return false, workspace.ErrInvalid
		}
	}
	if r.CapacityBytes <= 0 || r.StateCapacityBytes <= 0 || r.StateCapacityBytes > r.CapacityBytes || (r.AccessMode != "single-writer" && r.AccessMode != "single-pod-writer") || r.StorageProfile == "" || r.Operation.Digest != workspace.CreateDigest(r) {
		return false, workspace.ErrInvalid
	}
	cmd := workspace.Command{Kind: "create", TenantID: r.TenantID, WorkspaceID: r.WorkspaceID, Operation: r.Operation, Create: r}
	ready := false
	err := s.journal(ctx, r.TenantID, func(tx *sql.Tx) error {
		if s.access(ctx, cmd) != nil {
			return workspace.ErrUnavailable
		}
		// Lock the Session to serialize standalone Workspace reservation and closure.
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM sessions WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, r.TenantID, r.SessionID).Scan(&state); err != nil {
			return err
		}
		if state != "PROVISIONING" {
			// A completed reservation remains replayable after Session readiness, but
			// this branch cannot allocate storage or restart initialization.
			var existing workspace.CreateRequest
			var raw []byte
			var workspaceState string
			err := tx.QueryRowContext(ctx, `SELECT provenance,state FROM workspaces WHERE tenant_id=$1 AND workspace_id=$2 AND session_id=$3 AND provider_kind='kubernetes' AND source_type='empty'`, r.TenantID, r.WorkspaceID, r.SessionID).Scan(&raw, &workspaceState)
			if err != nil {
				return err
			}
			if json.Unmarshal(raw, &existing) != nil || existing != r || workspaceState != "READY" || state == "CLOSING" || state == "CLOSED" {
				return workspace.ErrConflict
			}
			ready = true
			return nil
		}
		encryption := "none"
		if r.EncryptionRequired {
			encryption = "required"
		}
		raw, _ := json.Marshal(r)
		_, err := tx.ExecContext(ctx, `INSERT INTO workspaces(tenant_id,workspace_id,session_id,state,provider_kind,provider_reference,capacity_bytes,access_mode,volume_mode,encryption_class,storage_profile,config_digest,source_type,source_reference,provenance,provenance_digest,create_operation_id,retention_disposition) VALUES($1,$2,$3,'PROVISIONING','kubernetes',$4,$5,$6,'filesystem',$7,$8,$9,'empty','empty',$10,$11,$12,'retain') ON CONFLICT(tenant_id,workspace_id) DO NOTHING`, r.TenantID, r.WorkspaceID, r.SessionID, "intent/"+string(r.WorkspaceID), r.CapacityBytes, r.AccessMode, encryption, r.StorageProfile, r.ConfigurationDigest, raw, workspace.EvidenceDigest(raw), r.Operation.ID)
		if err != nil {
			return err
		}
		var saved []byte
		var provider, source string
		if err = tx.QueryRowContext(ctx, `SELECT provenance,state,provider_kind,source_type FROM workspaces WHERE tenant_id=$1 AND workspace_id=$2`, r.TenantID, r.WorkspaceID).Scan(&saved, &state, &provider, &source); err != nil {
			return err
		}
		var existing workspace.CreateRequest
		if json.Unmarshal(saved, &existing) != nil || existing != r || provider != "kubernetes" || source != "empty" {
			return workspace.ErrConflict
		}
		if state != "PROVISIONING" && state != "READY" {
			return workspace.ErrConflict
		}
		ready = state == "READY"
		return nil
	})
	return ready, err
}
func (s *WorkspaceStorage) publishEmpty(ctx context.Context, tx *sql.Tx, saved workspace.Reservation) error {
	r := saved.Request
	proof := saved.EmptyProof
	if proof == nil || proof.WorkspaceReference == "" || proof.StateReference == "" {
		return workspace.ErrIntegrity
	}
	evidence, _ := json.Marshal(proof)
	// Empty manifest v1 is exactly [] (zero files, zero bytes); it is not a snapshot.
	root := workspace.EmptyManifestDigest()
	_, err := tx.ExecContext(ctx, `INSERT INTO workspace_generations(tenant_id,workspace_generation_id,workspace_id,session_id,generation,operation_id,integrity_algorithm,integrity_root,manifest_digest,logical_bytes,logical_files,storage_evidence,storage_evidence_digest,classification,retention_disposition) VALUES($1,$2,$3,$4,0,$2,'empty-manifest-v1',$5,$5,0,0,$6,$7,'CONFIDENTIAL','retain')`, r.TenantID, r.Operation.ID, r.WorkspaceID, r.SessionID, root, evidence, workspace.EvidenceDigest(evidence))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE workspaces SET state='READY',state_version=state_version+1,current_generation=0,current_workspace_generation_id=$3,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND workspace_id=$2 AND state='PROVISIONING'`, r.TenantID, r.WorkspaceID, r.Operation.ID)
	return err
}
