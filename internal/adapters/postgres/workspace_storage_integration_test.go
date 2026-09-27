package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
)

func TestWorkspaceStorageJournal(t *testing.T) {
	db, sandboxRequest := sandboxDatabaseFixture(t)
	ctx := context.Background()
	ids := concurrencyIDs(t, time.Now(), 3)
	r := workspace.CreateRequest{TenantID: sandboxRequest.Scope.TenantID, SessionID: sandboxRequest.Scope.SessionID, WorkspaceID: ids[0], Operation: workspace.Operation{ID: ids[1]}, StorageProfile: "csi-test", ConfigurationDigest: testDigest('c'), CapacityBytes: 1024, StateCapacityBytes: 512, AccessMode: "single-pod-writer", EncryptionRequired: true}
	r.Operation.Digest = workspace.CreateDigest(r)
	_, err := db.Exec(`INSERT INTO workspaces (tenant_id,workspace_id,session_id,state,provider_kind,provider_reference,capacity_bytes,access_mode,volume_mode,encryption_class,storage_profile,config_digest,source_type,source_reference,provenance,provenance_digest,create_operation_id,retention_disposition) VALUES($1,$2,$3,'PROVISIONING','kubernetes',$4,1024,'single-pod-writer','filesystem','encrypted','csi-test',$5,'empty','empty','{}',$5,$6,'retain')`, r.TenantID, r.WorkspaceID, r.SessionID, "intent/"+string(r.WorkspaceID), r.ConfigurationDigest, r.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	allow := func(context.Context, workspace.Command) error { return nil }
	s, _ := postgres.NewWorkspaceStorage(db, allow)
	cmd := workspace.Command{Kind: "create", TenantID: r.TenantID, WorkspaceID: r.WorkspaceID, Operation: r.Operation, Create: r}
	partial := errors.New("partial provider failure")
	err = s.Do(ctx, cmd, func(ctx context.Context, b workspace.Reservation, bind func(string, string) error) error {
		if b.Request != r || b.WorkspaceReference != "" {
			t.Fatal(b)
		}
		if err := bind("workspace", "agents/workspace/uid-1"); err != nil {
			return err
		}
		return partial
	})
	if !errors.Is(err, partial) {
		t.Fatal(err)
	}
	// Recreate the adapter, proving the partial observation was committed despite
	// the failed external operation and rollback of the lifecycle lock transaction.
	s, _ = postgres.NewWorkspaceStorage(db, allow)
	err = s.Do(ctx, cmd, func(ctx context.Context, b workspace.Reservation, bind func(string, string) error) error {
		if b.WorkspaceReference != "agents/workspace/uid-1" {
			t.Fatal("lost durable identity", b)
		}
		if err := bind("workspace", "agents/workspace/replacement"); !errors.Is(err, workspace.ErrIntegrity) {
			t.Fatal(err)
		}
		return bind("state", "agents/state/uid-2")
	})
	if err != nil {
		t.Fatal(err)
	}
	changed := cmd
	changed.Create.CapacityBytes++
	changed.Create.Operation.Digest = workspace.CreateDigest(changed.Create)
	changed.Operation = changed.Create.Operation
	noWork := func(context.Context, workspace.Reservation, func(string, string) error) error {
		t.Fatal("unexpected provider work")
		return nil
	}
	if err = s.Do(ctx, changed, noWork); err == nil {
		t.Fatal("accepted changed request")
	}
	foreign := cmd
	foreign.Kind = "get"
	foreign.TenantID = ids[2]
	if err = s.Do(ctx, foreign, noWork); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatal(err)
	}
	denied, _ := postgres.NewWorkspaceStorage(db, func(context.Context, workspace.Command) error { return errors.New("private policy") })
	if err = denied.Do(ctx, cmd, noWork); !errors.Is(err, workspace.ErrUnavailable) {
		t.Fatal(err)
	}
	// A competing lifecycle update must wait until provider work releases the
	// Workspace lock. A short database statement deadline makes this deterministic.
	err = s.Do(ctx, cmd, func(ctx context.Context, b workspace.Reservation, bind func(string, string) error) error {
		blocked, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		_, e := db.ExecContext(blocked, `UPDATE workspaces SET state='DELETING',state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND workspace_id=$2`, r.TenantID, r.WorkspaceID)
		if e == nil {
			t.Fatal("lifecycle changed during provider operation")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	del := workspace.Command{Kind: "delete", TenantID: r.TenantID, WorkspaceID: r.WorkspaceID, Operation: workspace.Operation{ID: ids[2]}}
	del.Operation.Digest = workspace.DeleteDigest(r.TenantID, r.WorkspaceID, del.Operation)
	if err = s.Do(ctx, del, noWork); !errors.Is(err, workspace.ErrConflict) {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE workspaces SET state='DELETING',state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND workspace_id=$2`, r.TenantID, r.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	work := func(context.Context, workspace.Reservation, func(string, string) error) error { return nil }
	if err = s.Do(ctx, del, work); err != nil {
		t.Fatal(err)
	}
	if err = s.Do(ctx, del, work); err != nil {
		t.Fatal(err)
	}
	if err = s.Do(ctx, cmd, noWork); !errors.Is(err, workspace.ErrConflict) {
		t.Fatal(err)
	}
	var refs int
	if err = db.QueryRow(`SELECT count(*) FROM workspace_storage_operations WHERE tenant_id=$1 AND workspace_id=$2 AND workspace_reference='agents/workspace/uid-1' AND state_reference='agents/state/uid-2' AND delete_operation_id=$3`, r.TenantID, r.WorkspaceID, del.Operation.ID).Scan(&refs); err != nil || refs != 1 {
		t.Fatal(refs, err)
	}
	if _, err = db.Exec(`UPDATE workspace_storage_operations SET workspace_reference='replacement' WHERE tenant_id=$1 AND workspace_id=$2`, r.TenantID, r.WorkspaceID); err == nil {
		t.Fatal("database permitted UID replacement")
	}
}
