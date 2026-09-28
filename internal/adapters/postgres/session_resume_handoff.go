package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	executions "github.com/bdobrica/ThinkPixelAR/internal/app/execution"
	sessions "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/authority"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
)

// resumeHandoff runs only after the normal current Execution/Attempt fence and
// immutable runtime checks. It transfers a completed infrastructure reservation
// to the first freshly admitted Execution without issuing or reusing authority.
// Generic acquisitions have no resume journal and retain their existing path.
func resumeHandoff(ctx context.Context, tx *sql.Tx, r sandbox.AcquireRequest) (string, error) {
	var raw, proof []byte
	var digest, state string
	err := tx.QueryRowContext(ctx, `SELECT intent,intent_digest,state,observation FROM session_resume_operations WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, r.Scope.TenantID, r.Scope.SandboxID).Scan(&raw, &digest, &state, &proof)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var i sessions.ResumeIntent
	var o sessions.ResumeObservation
	if json.Unmarshal(raw, &i) != nil || i.Digest() != digest || json.Unmarshal(proof, &o) != nil || o.Validate() != nil || state != i.TargetState || i.Request.SessionID != r.Scope.SessionID || uint64(i.ExecutionGeneration+1) != r.Scope.Generation {
		return "", sandbox.ErrConflict
	}
	// The supported concrete export lane records exact PVC UIDs in the attachment
	// receipt. Other providers must implement their own explicit handoff.
	parts := strings.Split(o.AttachmentReference, "/")
	if len(parts) != 4 || parts[1] != r.Workspace.Reference || parts[3] != string(i.AttachmentID) {
		return "", sandbox.ErrIntegrity
	}
	var current bool
	err = tx.QueryRowContext(ctx, `SELECT state='ATTACHED' AND current_attachment_id=$3 AND current_workspace_generation_id=$4 AND current_generation=$5 FROM workspaces WHERE tenant_id=$1 AND workspace_id=$2 AND session_id=$6 FOR UPDATE`, r.Scope.TenantID, i.WorkspaceID, i.AttachmentID, i.WorkspaceGenerationID, i.WorkspaceGeneration, r.Scope.SessionID).Scan(&current)
	if err != nil {
		return "", err
	}
	if !current {
		return "", sandbox.ErrConflict
	}
	repos := &repositories{tx: tx, tenantID: r.Scope.TenantID}
	_, g, err := executions.LoadLocalBinding(ctx, repos, r.Scope.ExecutionID)
	if err != nil {
		return "", sandbox.ErrPermission
	}
	record, err := repos.LocalGrants().Get(ctx, g.ID)
	if err != nil || record.State != string(authority.Active) || !time.Now().Before(g.ExpiresAt) || g.Generation != r.Scope.Generation || r.Deadline.After(g.ExpiresAt) {
		return "", sandbox.ErrPermission
	}
	return o.SandboxReference, nil
}
