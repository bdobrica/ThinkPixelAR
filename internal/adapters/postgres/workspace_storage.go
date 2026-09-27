package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// WorkspaceStorageAccess performs current trusted admission/disclosure checks.
// Delete must also prove retention eligibility and independently authorized
// cleanup. It receives no authority from the provider handle or operation ID.
type WorkspaceStorageAccess func(context.Context, workspace.Command) error

type WorkspaceStorage struct {
	db     *sql.DB
	access WorkspaceStorageAccess
}

var _ workspace.Operations = (*WorkspaceStorage)(nil)

// The pool needs at least two available connections: one holds the Workspace
// lifecycle lock while short transactions durably journal external observations.
func NewWorkspaceStorage(db *sql.DB, access WorkspaceStorageAccess) (*WorkspaceStorage, error) {
	if db == nil || access == nil {
		return nil, workspace.ErrInvalid
	}
	return &WorkspaceStorage{db: db, access: access}, nil
}
func storageDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.ErrNotFound
	}
	for _, e := range []error{workspace.ErrInvalid, workspace.ErrIntegrity, workspace.ErrConflict, workspace.ErrNotFound, workspace.ErrUnsupported, workspace.ErrUnavailable} {
		if errors.Is(err, e) {
			return e
		}
	}
	return workspace.ErrUnavailable
}
func (s *WorkspaceStorage) journal(ctx context.Context, tenant primitives.ID, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageDBError(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, tenant); err != nil {
		return storageDBError(err)
	}
	if err = fn(tx); err != nil {
		return storageDBError(err)
	}
	return storageDBError(tx.Commit())
}
func (s *WorkspaceStorage) Do(ctx context.Context, cmd workspace.Command, work func(context.Context, workspace.Reservation, func(string, string) error) error) error {
	for _, id := range []primitives.ID{cmd.TenantID, cmd.WorkspaceID} {
		if _, err := primitives.ParseID(string(id)); err != nil {
			return workspace.ErrInvalid
		}
	}
	if work == nil || (cmd.Kind != "create" && cmd.Kind != "get" && cmd.Kind != "delete") {
		return workspace.ErrInvalid
	}
	if cmd.Kind != "get" {
		if _, err := primitives.ParseID(string(cmd.Operation.ID)); err != nil {
			return workspace.ErrInvalid
		}
	}
	if cmd.Kind == "create" && (cmd.Create.TenantID != cmd.TenantID || cmd.Create.WorkspaceID != cmd.WorkspaceID || cmd.Create.Operation != cmd.Operation || workspace.CreateDigest(cmd.Create) != cmd.Operation.Digest) {
		return workspace.ErrInvalid
	}
	if cmd.Kind == "delete" && workspace.DeleteDigest(cmd.TenantID, cmd.WorkspaceID, cmd.Operation) != cmd.Operation.Digest {
		return workspace.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	// NO KEY UPDATE excludes all lifecycle updates but permits the journal's FK
	// key-share lock. Never hold an uncommitted reservation across a provider call.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageDBError(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, cmd.TenantID); err != nil {
		return storageDBError(err)
	}
	var sid, createID primitives.ID
	var state, provider, profile, config, access, volume, encryption string
	var capacity int64
	var attachment sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT session_id,state,provider_kind,storage_profile,config_digest,capacity_bytes,access_mode,volume_mode,encryption_class,create_operation_id,current_attachment_id FROM workspaces WHERE tenant_id=$1 AND workspace_id=$2 FOR NO KEY UPDATE`, cmd.TenantID, cmd.WorkspaceID).Scan(&sid, &state, &provider, &profile, &config, &capacity, &access, &volume, &encryption, &createID, &attachment)
	if err != nil {
		return storageDBError(err)
	}
	if provider != "kubernetes" {
		return workspace.ErrUnsupported
	}
	if cmd.Kind == "create" && state != "PROVISIONING" || cmd.Kind == "delete" && (state != "DELETING" || attachment.Valid) {
		return workspace.ErrConflict
	}
	if err = s.access(ctx, cmd); err != nil {
		return workspace.ErrUnavailable
	}
	var saved workspace.Reservation
	err = s.journal(ctx, cmd.TenantID, func(j *sql.Tx) error {
		if cmd.Kind != "get" {
			// Serialize operation-ID reuse even across different Workspaces.
			if _, e := j.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, string(cmd.TenantID)+"/workspace-storage/"+string(cmd.Operation.ID)); e != nil {
				return e
			}
			var conflict bool
			if e := j.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_storage_operations WHERE tenant_id=$1 AND ((create_operation_id=$2 AND ($3<>'create' OR workspace_id<>$4)) OR (delete_operation_id=$2 AND ($3<>'delete' OR workspace_id<>$4))))`, cmd.TenantID, cmd.Operation.ID, cmd.Kind, cmd.WorkspaceID).Scan(&conflict); e != nil {
				return e
			}
			if conflict {
				return workspace.ErrConflict
			}
		}
		if cmd.Kind == "create" {
			r := cmd.Create
			if r.SessionID != sid || r.Operation.ID != createID || r.StorageProfile != profile || r.ConfigurationDigest != config || r.CapacityBytes != capacity || r.AccessMode != access || volume != "filesystem" || (encryption != "none" && !r.EncryptionRequired) {
				return workspace.ErrIntegrity
			}
			raw, e := json.Marshal(r)
			if e != nil {
				return workspace.ErrInvalid
			}
			if _, e = j.ExecContext(ctx, `INSERT INTO workspace_storage_operations(tenant_id,workspace_id,create_operation_id,request) VALUES($1,$2,$3,$4) ON CONFLICT(tenant_id,workspace_id) DO NOTHING`, cmd.TenantID, cmd.WorkspaceID, cmd.Operation.ID, raw); e != nil {
				return e
			}
		}
		var raw []byte
		var deleteID, deleteDigest sql.NullString
		e := j.QueryRowContext(ctx, `SELECT request,COALESCE(workspace_reference,''),COALESCE(state_reference,''),delete_operation_id,delete_digest FROM workspace_storage_operations WHERE tenant_id=$1 AND workspace_id=$2 FOR UPDATE`, cmd.TenantID, cmd.WorkspaceID).Scan(&raw, &saved.WorkspaceReference, &saved.StateReference, &deleteID, &deleteDigest)
		if e != nil {
			return e
		}
		if json.Unmarshal(raw, &saved.Request) != nil || saved.Request.TenantID != cmd.TenantID || saved.Request.WorkspaceID != cmd.WorkspaceID || workspace.CreateDigest(saved.Request) != saved.Request.Operation.Digest {
			return workspace.ErrIntegrity
		}
		if cmd.Kind == "create" && (saved.Request != cmd.Create || deleteID.Valid) {
			return workspace.ErrConflict
		}
		if cmd.Kind == "delete" {
			if deleteID.Valid && (deleteID.String != string(cmd.Operation.ID) || deleteDigest.String != cmd.Operation.Digest) {
				return workspace.ErrConflict
			}
			_, e = j.ExecContext(ctx, `UPDATE workspace_storage_operations SET delete_operation_id=$3,delete_digest=$4 WHERE tenant_id=$1 AND workspace_id=$2`, cmd.TenantID, cmd.WorkspaceID, cmd.Operation.ID, cmd.Operation.Digest)
		}
		return e
	})
	if err != nil {
		return err
	}
	// Each UID survives even if a later call fails or this outer read-only lock
	// transaction rolls back. References are immutable at the database boundary.
	return work(ctx, saved, func(role, ref string) error {
		if ref == "" || len(ref) > 2048 {
			return workspace.ErrInvalid
		}
		column := "workspace_reference"
		if role == "state" {
			column = "state_reference"
		} else if role != "workspace" {
			return workspace.ErrInvalid
		}
		return s.journal(ctx, cmd.TenantID, func(j *sql.Tx) error {
			result, e := j.ExecContext(ctx, `UPDATE workspace_storage_operations SET `+column+`=$3 WHERE tenant_id=$1 AND workspace_id=$2 AND (`+column+` IS NULL OR `+column+`=$3)`, cmd.TenantID, cmd.WorkspaceID, ref)
			if e != nil {
				return e
			}
			n, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if n != 1 {
				return workspace.ErrIntegrity
			}
			return nil
		})
	})
}
