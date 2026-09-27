package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// This verifies the persisted lifecycle boundary, not the future Session close
// handler or an integrated WS service. No Execution or compute needs cleanup.
func TestSessionClosePreservesWorkspaceMetadata(t *testing.T) {
	dsn := os.Getenv("THINKPIXELAR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("THINKPIXELAR_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db := openReplayDatabase(t, dsn)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC().Truncate(time.Microsecond)
	ids := make([]primitives.ID, 6)
	for i := range ids {
		var err error
		ids[i], err = primitives.NewID(now)
		if err != nil {
			t.Fatal(err)
		}
	}
	tenant, sid, wid, gid := ids[0], ids[1], ids[2], ids[3]
	createReplayTenant(t, ctx, db, tenant)
	store, err := postgres.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	s, err := session.New(tenant, sid, session.RuntimeBinding{
		AuthorityMode: "LOCAL", AuthorityNamespace: "close-retention-test", AgentID: "agent", AgentVersionID: "v1",
		RuntimeSpecSchemaVersion: "v1", RuntimeSpec: []byte(`{}`), RuntimeSpecDigest: testDigest('a'),
		RuntimeProfileSchemaVersion: "v1", RuntimeProfileSnapshot: []byte(`{}`), RuntimeProfileDigest: testDigest('b'),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.WithinTransaction(ctx, tenant, func(ctx context.Context, r persistence.Repositories) error { return r.Sessions().Add(ctx, s) }); err != nil {
		t.Fatal(err)
	}
	// Seed AR's existing standalone Workspace metadata. WS canonical records live
	// in a different service; this fixture deliberately does not impersonate them.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspaces
 (tenant_id,workspace_id,session_id,state,provider_kind,provider_reference,capacity_bytes,access_mode,volume_mode,encryption_class,storage_profile,config_digest,source_type,source_reference,provenance,provenance_digest,create_operation_id,retention_disposition)
 VALUES ($1,$2,$3,'PROVISIONING','test-storage',$4,1024,'ReadWriteOnce','Filesystem','test','test',$5,'empty','empty','{}',$5,$6,'retain')`, tenant, wid, sid, "storage/"+string(wid), testDigest('c'), ids[4]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_generations
 (tenant_id,workspace_generation_id,workspace_id,session_id,generation,operation_id,provider_snapshot_reference,integrity_algorithm,integrity_root,manifest_digest,logical_bytes,logical_files,storage_evidence,storage_evidence_digest,classification,retention_disposition)
 VALUES ($1,$2,$3,$4,0,$5,'retained-snapshot','sha256',$6,$6,12,1,'{}',$6,'CONFIDENTIAL','retain')`, tenant, gid, wid, sid, ids[5], testDigest('d')); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspaces SET state='READY',current_generation=0,current_workspace_generation_id=$3 WHERE tenant_id=$1 AND workspace_id=$2`, tenant, wid, gid); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	snapshot := func() string {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, `SELECT set_config('thinkpixelar.tenant_id',$1,true)`, tenant); err != nil {
			t.Fatal(err)
		}
		var workspace, generation string
		if err = tx.QueryRowContext(ctx, `SELECT to_jsonb(w)::text FROM workspaces w WHERE tenant_id=$1 AND workspace_id=$2`, tenant, wid).Scan(&workspace); err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRowContext(ctx, `SELECT to_jsonb(g)::text FROM workspace_generations g WHERE tenant_id=$1 AND workspace_generation_id=$2`, tenant, gid).Scan(&generation); err != nil {
			t.Fatal(err)
		}
		var cleanupCount int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM cleanup_intents WHERE tenant_id=$1`, tenant).Scan(&cleanupCount); err != nil {
			t.Fatal(err)
		}
		if cleanupCount != 0 {
			t.Fatal("Session transition queued resource deletion")
		}
		return workspace + generation
	}
	before := snapshot()
	for _, next := range []session.State{session.Ready, session.Closing, session.Closing, session.Closed, session.Closed} {
		if err = store.WithinTransaction(ctx, tenant, func(ctx context.Context, r persistence.Repositories) error {
			current, err := r.Sessions().Get(ctx, sid)
			if err != nil {
				return err
			}
			version := current.StateVersion()
			if err = current.Transition(next, version, now); err != nil {
				return err
			}
			// Replays are no-ops and do not advance the persisted version.
			if current.StateVersion() == version {
				return nil
			}
			return r.Sessions().Update(ctx, current, version)
		}); err != nil {
			t.Fatalf("transition to %s: %v", next, err)
		}
		if got := snapshot(); got != before {
			t.Fatalf("%s changed Workspace or generation metadata", next)
		}
	}
	// Reopen the connection/store so retained state cannot be an in-memory copy.
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openReplayDatabase(t, dsn)
	store, err = postgres.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.WithinTransaction(ctx, tenant, func(ctx context.Context, r persistence.Repositories) error {
		current, err := r.Sessions().Get(ctx, sid)
		if err != nil {
			return err
		}
		if current.State() != session.Closed || current.StateVersion() != 3 {
			return fmt.Errorf("unexpected persisted Session: %s v%d", current.State(), current.StateVersion())
		}
		if _, ok := current.ClosedAt(); !ok {
			return fmt.Errorf("missing close timestamp")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(); got != before {
		t.Fatal("Workspace or generation changed after reconnect")
	}
}
