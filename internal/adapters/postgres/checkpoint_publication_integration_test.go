package postgres_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	app "github.com/bdobrica/ThinkPixelAR/internal/app/checkpoint"
	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/checkpoint"
	port "github.com/bdobrica/ThinkPixelAR/internal/ports/checkpoint"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
)

type checkpointSigner struct {
	key  ed25519.PrivateKey
	fail bool
}

func (s checkpointSigner) Identity() (string, string, ed25519.PublicKey) {
	return "test-control-plane", "test-key", s.key.Public().(ed25519.PublicKey)
}
func (s checkpointSigner) Sign(_ context.Context, b []byte) ([]byte, error) {
	if s.fail {
		return nil, errors.New("private signing failure")
	}
	return ed25519.Sign(s.key, b), nil
}
func publicationAllow(context.Context, port.Request) error                              { return nil }
func publicationVerify(context.Context, port.Request, postgres.PublicationSource) error { return nil }
func publicationFixture(t *testing.T, idle bool) (*sql.DB, port.Request, workspace.CheckpointProof, checkpointSigner) {
	t.Helper()
	db, w, p := checkpointFixture(t, idle)
	p.SnapshotReference = "immutable-snapshot-uid"
	id := concurrencyIDs(t, time.Now(), 1)[0]
	var spec, profile string
	if e := db.QueryRow(`SELECT runtime_spec_digest,runtime_profile_digest FROM sessions WHERE tenant_id=$1 AND session_id=$2`, w.TenantID, w.SessionID).Scan(&spec, &profile); e != nil {
		t.Fatal(e)
	}
	r := port.Request{ID: id, Boundary: w, Binding: domain.Binding{SessionID: w.SessionID, WorkspaceID: w.WorkspaceID, WorkspaceGenerationID: w.Operation.ID, WorkspaceGeneration: 1, OperationID: id, Purpose: domain.PurposeCheckpoint, RuntimeSpecID: "fixture-runtime", RuntimeSpecDigest: app.Digest(spec), AdapterKind: "codex", AdapterVersion: "0.155.0", AdapterBuildDigest: app.Digest(testDigest('a')), ProtocolName: "app-server", ProtocolVersion: "v2", StateFormatName: "codex", StateFormatVersion: "v1", RuntimeProfileDigest: app.Digest(profile), RetentionDisposition: "retain"}, VendorState: []port.VendorObject{{ID: "vendor-1", Reference: "immutable-vendor-1", MediaType: "application/octet-stream", Format: port.VersionedName{Name: "codex", Version: "v1"}, Algorithm: "sha-256", Digest: app.Digest(testDigest('b')), Size: 25, Classification: "Confidential", Role: "required"}}}
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	return db, r, p, checkpointSigner{key: key}
}
func commitWorkspace(t *testing.T, db *sql.DB, r port.Request, p workspace.CheckpointProof) {
	t.Helper()
	s, _ := postgres.NewWorkspaceCheckpoints(db, checkpointAllow, checkpointVerify)
	if _, e := s.Prepare(context.Background(), r.Boundary); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Publish(context.Background(), r.Boundary, p); e != nil {
		t.Fatal(e)
	}
}
func assertPublicationCount(t *testing.T, db *sql.DB, r port.Request, want int) {
	t.Helper()
	for _, q := range []string{`SELECT count(*) FROM checkpoints WHERE tenant_id=$1`, `SELECT count(*) FROM runtime_events WHERE tenant_id=$1 AND event_type='checkpoint.committed'`, `SELECT count(*) FROM outbox_messages WHERE tenant_id=$1 AND topic='checkpoint.committed'`, `SELECT count(*) FROM sessions WHERE tenant_id=$1 AND current_checkpoint_id IS NOT NULL`} {
		var n int
		if e := db.QueryRow(q, r.Boundary.TenantID).Scan(&n); e != nil || n != want {
			t.Fatalf("publication count %d want %d: %v", n, want, e)
		}
	}
}
func TestCheckpointPublication(t *testing.T) {
	db, r, p, key := publicationFixture(t, false)
	ctx := context.Background()
	s, _ := postgres.NewCheckpoints(db, publicationAllow, publicationVerify, key)
	if _, e := s.Publish(ctx, r); !errors.Is(e, workspace.ErrConflict) {
		t.Fatalf("uncommitted generation: %v", e)
	}
	assertPublicationCount(t, db, r, 0)
	commitWorkspace(t, db, r, p)
	fail, _ := postgres.NewCheckpoints(db, publicationAllow, func(context.Context, port.Request, postgres.PublicationSource) error {
		return errors.New("credential canary or missing vendor object")
	}, key)
	if _, e := fail.Publish(ctx, r); !errors.Is(e, workspace.ErrIntegrity) {
		t.Fatal(e)
	}
	fail, _ = postgres.NewCheckpoints(db, publicationAllow, publicationVerify, checkpointSigner{key: key.key, fail: true})
	if _, e := fail.Publish(ctx, r); !errors.Is(e, workspace.ErrIntegrity) {
		t.Fatal(e)
	}
	assertPublicationCount(t, db, r, 0)
	// Fail the last insert. Head, immutable checkpoint, event and sequence must roll back.
	if _, e := db.Exec(`INSERT INTO outbox_messages(tenant_id,message_id,topic,schema_version,event_id,aggregate_type,aggregate_id,aggregate_version,payload,payload_digest,state,available_at,created_at,updated_at) VALUES($1,$2,'collision','test/v1',$2,'session',$3,1,'{}',$4,'PENDING',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, r.Boundary.TenantID, r.ID, r.Boundary.SessionID, workspace.EvidenceDigest([]byte(`{}`))); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Publish(ctx, r); !errors.Is(e, workspace.ErrUnavailable) {
		t.Fatal(e)
	}
	assertPublicationCount(t, db, r, 0)
	if _, e := db.Exec(`DELETE FROM outbox_messages WHERE tenant_id=$1 AND message_id=$2`, r.Boundary.TenantID, r.ID); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan port.Result, 6)
	errs := make(chan error, 6)
	for n := 0; n < 6; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); v, e := s.Publish(ctx, r); results <- v; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var manifest []byte
	for v := range results {
		if v.ID != r.ID {
			t.Fatal(v.ID)
		}
		if manifest != nil && !bytes.Equal(manifest, v.Manifest) {
			t.Fatal("nonidentical replay")
		}
		manifest = v.Manifest
	}
	assertPublicationCount(t, db, r, 1)
	// Process replacement must replay without invoking a signer or durability verifier.
	replay, _ := postgres.NewCheckpoints(db, publicationAllow, func(context.Context, port.Request, postgres.PublicationSource) error {
		t.Error("reverified historical boundary")
		return workspace.ErrIntegrity
	}, checkpointSigner{key: key.key, fail: true})
	v, e := replay.Publish(ctx, r)
	if e != nil || !bytes.Equal(v.Manifest, manifest) {
		t.Fatal(e)
	}
	changed := r
	changed.VendorState = append([]port.VendorObject(nil), r.VendorState...)
	changed.VendorState[0].Reference = "another-object"
	if _, e = s.Publish(ctx, changed); !errors.Is(e, workspace.ErrConflict) {
		t.Fatal(e)
	}
	deny, _ := postgres.NewCheckpoints(db, func(context.Context, port.Request) error { return errors.New("private policy error") }, publicationVerify, key)
	if _, e = deny.Publish(ctx, r); !errors.Is(e, workspace.ErrUnavailable) {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE checkpoints SET publication_request_digest=$3 WHERE tenant_id=$1 AND checkpoint_id=$2`, r.Boundary.TenantID, r.ID, testDigest('e')); e == nil {
		t.Fatal("mutable request")
	}
	// New generation and checkpoint do not rewrite the old immutable result.
	next := r
	next.ID = concurrencyIDs(t, time.Now(), 1)[0]
	next.Binding.OperationID = next.ID
	next.Binding.ParentCheckpointID = r.ID
	next.Boundary.ParentID = r.Boundary.Operation.ID
	next.Boundary.ParentGeneration = 1
	next.Boundary.Operation.ID = concurrencyIDs(t, time.Now(), 1)[0]
	next.Boundary.Operation.Digest = workspace.CheckpointDigest(next.Boundary)
	next.Binding.WorkspaceGenerationID = next.Boundary.Operation.ID
	next.Binding.WorkspaceGeneration = 2
	commitWorkspace(t, db, next, p)
	if _, e = s.Publish(ctx, next); e != nil {
		t.Fatal(e)
	}
	v, e = replay.Publish(ctx, r)
	if e != nil || !bytes.Equal(v.Manifest, manifest) {
		t.Fatal(e)
	}
}
func TestCheckpointPublicationFences(t *testing.T) {
	cases := []struct{ name, q string }{
		{"attempt", `UPDATE attempts SET is_current=false,state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1`},
		{"cancelling", `UPDATE executions SET state='CANCELLING',state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1`},
		{"attachment", `UPDATE workspaces SET current_attachment_id=NULL WHERE tenant_id=$1`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, r, p, key := publicationFixture(t, false)
			commitWorkspace(t, db, r, p)
			if _, e := db.Exec(tc.q, r.Boundary.TenantID); e != nil {
				t.Fatal(e)
			}
			s, _ := postgres.NewCheckpoints(db, publicationAllow, publicationVerify, key)
			if _, e := s.Publish(context.Background(), r); !errors.Is(e, workspace.ErrConflict) {
				t.Fatal(e)
			}
			assertPublicationCount(t, db, r, 0)
		})
	}
}
func TestCheckpointPublicationIdle(t *testing.T) {
	db, r, p, key := publicationFixture(t, true)
	commitWorkspace(t, db, r, p)
	called := false
	s, _ := postgres.NewCheckpoints(db, publicationAllow, func(_ context.Context, got port.Request, src postgres.PublicationSource) error {
		called = true
		if got.ID != r.ID || src.Proof.SnapshotReference != p.SnapshotReference || len(src.RuntimeSpec) == 0 || src.EvidenceDigest == "" {
			t.Fatal("wrong trusted source")
		}
		return nil
	}, key)
	if _, e := s.Publish(context.Background(), r); e != nil || !called {
		t.Fatal(e)
	}
	assertPublicationCount(t, db, r, 1)
}

func TestCheckpointPublicationBindingMismatch(t *testing.T) {
	db, r, p, key := publicationFixture(t, false)
	commitWorkspace(t, db, r, p)
	s, _ := postgres.NewCheckpoints(db, publicationAllow, publicationVerify, key)
	for _, name := range []string{"runtime", "profile", "epoch", "parent", "tenant"} {
		t.Run(name, func(t *testing.T) {
			changed := r
			switch name {
			case "runtime":
				changed.Binding.RuntimeSpecDigest = app.Digest(testDigest('e'))
			case "profile":
				changed.Binding.RuntimeProfileDigest = app.Digest(testDigest('e'))
			case "epoch":
				changed.Boundary.SessionGeneration++
			case "parent":
				changed.Binding.ParentCheckpointID = concurrencyIDs(t, time.Now(), 1)[0]
			case "tenant":
				changed.Boundary.TenantID = concurrencyIDs(t, time.Now(), 1)[0]
			}
			changed.Boundary.Operation.Digest = workspace.CheckpointDigest(changed.Boundary)
			_, e := s.Publish(context.Background(), changed)
			want := workspace.ErrConflict
			if name == "tenant" {
				want = workspace.ErrNotFound
			}
			if !errors.Is(e, want) {
				t.Fatal(e)
			}
			assertPublicationCount(t, db, r, 0)
		})
	}
}
