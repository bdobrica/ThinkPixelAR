package postgres_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	checkpoint "github.com/bdobrica/ThinkPixelAR/internal/app/checkpoint"
	app "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	port "github.com/bdobrica/ThinkPixelAR/internal/ports/checkpoint"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func suspendAllow(context.Context, app.SuspendRequest) error                            { return nil }
func suspendVerify(context.Context, app.SuspendRequest, postgres.SuspendBoundary) error { return nil }

func suspendFixture(t *testing.T, retained bool) (*sql.DB, app.SuspendRequest, *checkpoint.RestoreValidator, primitives.ID) {
	t.Helper()
	db, r, p, key := publicationFixture(t, !retained)
	var sandboxID primitives.ID
	if retained {
		ids := concurrencyIDs(t, time.Now(), 2)
		sandboxID = ids[0]
		b := sandbox.AcquireRequest{Scope: sandbox.Scope{TenantID: r.Boundary.TenantID, SessionID: r.Boundary.SessionID, ExecutionID: r.Boundary.ExecutionID, AttemptID: r.Boundary.AttemptID, SandboxID: sandboxID, Generation: 1, AttemptOrdinal: 1}, Operation: sandbox.Operation{ID: string(ids[1])}, Runtime: sandbox.Runtime{Image: "registry.invalid/agent@" + testDigest('a'), Architecture: "amd64", Entrypoint: []string{"/agentd"}}, Workspace: sandbox.Attachment{Reference: "attachment-1", MountPath: "/workspace"}, BootstrapReference: "bootstrap-1", Deadline: time.Now().Add(30 * time.Minute)}
		var raw []byte
		if err := db.QueryRow(`SELECT canonical_resolution,resolution_digest,implementation_digest FROM runtime_profile_resolution_snapshots WHERE tenant_id=$1 AND execution_id=$2`, r.Boundary.TenantID, r.Boundary.ExecutionID).Scan(&raw, &b.ProfileDigest, &b.ImplementationDigest); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &b.Profile); err != nil {
			t.Fatal(err)
		}
		b.Operation.Digest, _ = sandbox.RequestDigest(b)
		store, _ := postgres.NewSandboxBindings(db)
		if _, err := store.Reserve(context.Background(), b); err != nil {
			t.Fatal(err)
		}
		if err := store.BindReference(context.Background(), b.Scope.TenantID, sandboxID, "namespace/sandbox/exact-uid"); err != nil {
			t.Fatal(err)
		}
		// Terminalization fixture: authoritative terminal facts precede the idle
		// checkpoint. Suspension itself must never terminalize mutable work.
		for _, q := range []string{
			`UPDATE attempts SET state='SUCCEEDED',is_current=false,terminal_at=clock_timestamp(),terminal_result_reference='fixture-result',terminal_result_digest=$2,state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1`,
			`UPDATE executions SET state='SUCCEEDED',terminal_at=clock_timestamp(),terminal_result_reference='fixture-result',terminal_result_digest=$2,state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1`,
		} {
			if _, err := db.Exec(q, r.Boundary.TenantID, testDigest('a')); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(`UPDATE sessions SET state='IDLE',current_execution_id=NULL,state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1`, r.Boundary.TenantID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE workspaces SET state='READY',current_attachment_id=NULL WHERE tenant_id=$1`, r.Boundary.TenantID); err != nil {
			t.Fatal(err)
		}
		r.Boundary.ExecutionID, r.Boundary.AttemptID, r.Boundary.AttachmentID = "", "", ""
		r.Boundary.Operation.Digest = workspace.CheckpointDigest(r.Boundary)
		// Seed an old stream and bootstrap projection so suspension must fence and
		// queue cleanup without deleting the credential history.
		credential := concurrencyIDs(t, time.Now(), 1)[0]
		if _, err := db.Exec(`INSERT INTO agentd_credential_state(tenant_id,sandbox_binding_id,attempt_id,connection_id,connection_epoch,connection_digest,connection_deadline) SELECT tenant_id,sandbox_binding_id,attempt_id,$3,1,$4,clock_timestamp()+interval '1 minute' FROM sandbox_bindings WHERE tenant_id=$1 AND sandbox_binding_id=$2`, r.Boundary.TenantID, sandboxID, credential, testDigest('b')); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO agentd_credentials(tenant_id,sandbox_binding_id,credential_id,certificate_digest,issuer_digest,proof_digest,bootstrap,not_before,expires_at) VALUES($1,$2,$3,$4,$4,$4,true,clock_timestamp(),clock_timestamp()+interval '1 minute')`, r.Boundary.TenantID, sandboxID, credential, testDigest('b')); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO agentd_bootstrap_delivery(tenant_id,credential_id,reference,provider_uid,expires_at) VALUES($1,$2,'{}','secret-uid',clock_timestamp()+interval '1 minute')`, r.Boundary.TenantID, credential); err != nil {
			t.Fatal(err)
		}
	}
	content := "immutable vendor state"
	digest := sha256.Sum256([]byte(content))
	r.VendorState[0].Size = int64(len(content))
	r.VendorState[0].Digest = hex.EncodeToString(digest[:])
	commitWorkspace(t, db, r, p)
	publisher, _ := postgres.NewCheckpoints(db, publicationAllow, publicationVerify, key)
	if _, err := publisher.Publish(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	validator, err := checkpoint.NewRestoreValidator(checkpoint.RestoreChecks{
		Keys: func(context.Context, primitives.ID, string, string, time.Time) (ed25519.PublicKey, error) {
			return key.key.Public().(ed25519.PublicKey), nil
		},
		Compatible: func(context.Context, checkpoint.RuntimeManifest, []port.VendorObject) error { return nil },
		Workspace:  func(context.Context, primitives.ID, checkpoint.WorkspaceManifest) error { return nil },
		OpenVendor: func(context.Context, primitives.ID, port.VendorObject) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(content)), nil
		}, MaxVendorBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := app.SuspendRequest{Caller: app.Caller{TenantID: r.Boundary.TenantID, PrincipalDigest: testDigest('c')}, SessionID: r.Boundary.SessionID, CheckpointID: r.ID, OperationID: concurrencyIDs(t, time.Now(), 1)[0]}
	if err = db.QueryRow(`SELECT state_version FROM sessions WHERE tenant_id=$1 AND session_id=$2`, request.Caller.TenantID, request.SessionID).Scan(&request.ExpectedVersion); err != nil {
		t.Fatal(err)
	}
	return db, request, validator, sandboxID
}

func TestSessionSuspendDurableReplayAndRelease(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready", true: "idle-retained-compute"}[retained], func(t *testing.T) {
			db, r, v, id := suspendFixture(t, retained)
			s, _ := postgres.NewSessionSuspends(db, suspendAllow, suspendVerify, v)
			// Inject failure at the final write: Session, revocation and cleanup roll back.
			if _, err := db.Exec(`INSERT INTO outbox_messages(tenant_id,message_id,topic,schema_version,event_id,aggregate_type,aggregate_id,aggregate_version,payload,payload_digest,state,available_at,created_at,updated_at) VALUES($1,$2,'fixture','v1',$2,'session',$3,1,'{}',$4,'PENDING',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, r.Caller.TenantID, r.OperationID, r.SessionID, testDigest('a')); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Suspend(context.Background(), r); !errors.Is(err, workspace.ErrUnavailable) {
				t.Fatal("rollback", err)
			}
			var count int
			for _, q := range []string{`SELECT count(*) FROM session_suspend_operations WHERE tenant_id=$1`, `SELECT count(*) FROM cleanup_intents WHERE tenant_id=$1`, `SELECT count(*) FROM runtime_events WHERE tenant_id=$1 AND event_type='session.state_changed'`, `SELECT count(*) FROM sessions WHERE tenant_id=$1 AND state='SUSPENDED'`, `SELECT count(*) FROM agentd_bootstrap_delivery WHERE tenant_id=$1 AND cleanup_requested`} {
				if err := db.QueryRow(q, r.Caller.TenantID).Scan(&count); err != nil || count != 0 {
					t.Fatal("partial commit", count, err)
				}
			}
			if _, err := db.Exec(`DELETE FROM outbox_messages WHERE tenant_id=$1 AND message_id=$2`, r.Caller.TenantID, r.OperationID); err != nil {
				t.Fatal(err)
			}
			for _, err := range concurrently(5, func(int) error {
				got, e := s.Suspend(context.Background(), r)
				if e == nil && (got.State != "SUSPENDED" || got.Version != r.ExpectedVersion+1 || got.CheckpointID != r.CheckpointID) {
					return errors.New("wrong result")
				}
				return e
			}) {
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, q := range []string{`SELECT count(*) FROM session_suspend_operations WHERE tenant_id=$1`, `SELECT count(*) FROM runtime_events WHERE tenant_id=$1 AND event_type='session.state_changed'`, `SELECT count(*) FROM outbox_messages WHERE tenant_id=$1 AND topic='session.suspended'`} {
				if err := db.QueryRow(q, r.Caller.TenantID).Scan(&count); err != nil || count != 1 {
					t.Fatal(count, err)
				}
			}
			if retained {
				store, _ := postgres.NewSandboxBindings(db)
				intent, err := store.LoadCompute(context.Background(), r.Caller.TenantID, id)
				if err != nil || intent.Current || intent.Desired != sandbox.ComputeReleased || !intent.ReleaseAuthorized || intent.Binding.ProviderReference != "namespace/sandbox/exact-uid" {
					t.Fatal("release intent", intent, err)
				}
				if err = db.QueryRow(`SELECT count(*) FROM agentd_credential_state WHERE tenant_id=$1 AND connection_id IS NOT NULL`, r.Caller.TenantID).Scan(&count); err != nil || count != 0 {
					t.Fatal("old connection", err)
				}
				if err = db.QueryRow(`SELECT count(*) FROM agentd_bootstrap_delivery WHERE tenant_id=$1 AND cleanup_requested`, r.Caller.TenantID).Scan(&count); err != nil || count != 1 {
					t.Fatal("secret cleanup", err)
				}
				// Failed release is retained reconciliation work, never Session rollback.
				if err = store.RecordCompute(context.Background(), intent, sandbox.ComputeObservation{State: sandbox.Unknown, Code: "PROVIDER_UNAVAILABLE"}); err != nil {
					t.Fatal(err)
				}
				intent, err = store.LoadCompute(context.Background(), r.Caller.TenantID, id)
				if err != nil {
					t.Fatal(err)
				}
				if err = store.RecordCompute(context.Background(), intent, sandbox.ComputeObservation{State: sandbox.Released, Code: "COMPUTE_ABSENT", Converged: true}); err != nil {
					t.Fatal(err)
				}
			}
			restarted, _ := postgres.NewSessionSuspends(db, suspendAllow, func(context.Context, app.SuspendRequest, postgres.SuspendBoundary) error {
				t.Error("revalidated historical result")
				return workspace.ErrIntegrity
			}, v)
			got, err := restarted.Suspend(context.Background(), r)
			if err != nil || got.Version != r.ExpectedVersion+1 {
				t.Fatal(got, err)
			}
			changed := r
			changed.ExpectedVersion++
			if _, err = restarted.Suspend(context.Background(), changed); !errors.Is(err, workspace.ErrConflict) {
				t.Fatal("changed request", err)
			}
			denied, _ := postgres.NewSessionSuspends(db, func(context.Context, app.SuspendRequest) error { return errors.New("private denial") }, suspendVerify, v)
			if _, err = denied.Suspend(context.Background(), r); !errors.Is(err, workspace.ErrUnavailable) {
				t.Fatal("replay authorization", err)
			}
			// Replay after a later lifecycle change must not re-suspend or release it.
			if _, err = db.Exec(`UPDATE sessions SET state='CLOSING',state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND session_id=$2`, r.Caller.TenantID, r.SessionID); err != nil {
				t.Fatal(err)
			}
			if _, err = restarted.Suspend(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			var state string
			if err = db.QueryRow(`SELECT state FROM sessions WHERE tenant_id=$1 AND session_id=$2`, r.Caller.TenantID, r.SessionID).Scan(&state); err != nil || state != "CLOSING" {
				t.Fatal(state, err)
			}
		})
	}
}

func TestSessionSuspendGuards(t *testing.T) {
	for _, name := range []string{"version", "tenant", "checkpoint", "closing", "attached", "verify", "corrupt", "missing", "snapshotting", "mutable", "active"} {
		t.Run(name, func(t *testing.T) {
			db, r, v, _ := suspendFixture(t, false)
			verify := postgres.SuspendVerifier(suspendVerify)
			want := workspace.ErrConflict
			switch name {
			case "version":
				r.ExpectedVersion++
			case "tenant":
				r.Caller.TenantID = concurrencyIDs(t, time.Now(), 1)[0]
				want = workspace.ErrNotFound
			case "checkpoint":
				r.CheckpointID = concurrencyIDs(t, time.Now(), 1)[0]
			case "closing":
				if _, err := db.Exec(`UPDATE sessions SET state='CLOSING',state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1`, r.Caller.TenantID); err != nil {
					t.Fatal(err)
				}
			case "attached":
				if _, err := db.Exec(`UPDATE workspaces SET state='ATTACHED',current_attachment_id=$2 WHERE tenant_id=$1`, r.Caller.TenantID, concurrencyIDs(t, time.Now(), 1)[0]); err != nil {
					t.Fatal(err)
				}
			case "verify":
				verify = func(context.Context, app.SuspendRequest, postgres.SuspendBoundary) error {
					return errors.New("private unresolved side effect")
				}
				want = workspace.ErrIntegrity
			case "corrupt":
				if _, err := db.Exec(`UPDATE checkpoints SET state='DELETING',state_version=state_version+1,delete_operation_id=$2,cleanup_state='PENDING',updated_at=clock_timestamp() WHERE tenant_id=$1`, r.Caller.TenantID, concurrencyIDs(t, time.Now(), 1)[0]); err != nil {
					t.Fatal(err)
				}
				want = checkpoint.ErrInvalidRestore
			case "missing":
				if _, err := db.Exec(`UPDATE sessions SET current_checkpoint_id=NULL WHERE tenant_id=$1`, r.Caller.TenantID); err != nil {
					t.Fatal(err)
				}
			case "snapshotting":
				if _, err := db.Exec(`UPDATE workspaces SET state='SNAPSHOTTING' WHERE tenant_id=$1`, r.Caller.TenantID); err != nil {
					t.Fatal(err)
				}
			case "mutable", "active":
				// Guard the entire mutable set even if the Session projection is stale.
				if _, err := db.Exec(`INSERT INTO executions(tenant_id,execution_id,session_id,session_generation,authority_mode,authority_namespace,authority_reference,grant_digest,agent_id,agent_version_id,agent_evidence,agent_evidence_digest,deadline,state) VALUES($1,$2,$3,1,'LOCAL','fixture','queued',$4,'agent','v1','{}',$4,clock_timestamp()+interval '1 hour','QUEUED')`, r.Caller.TenantID, concurrencyIDs(t, time.Now(), 1)[0], r.SessionID, testDigest('a')); err != nil {
					t.Fatal(err)
				}
			}
			if name == "active" {
				if _, err := db.Exec(`UPDATE sessions SET state='ACTIVE',execution_generation=1,current_execution_id=(SELECT execution_id FROM executions WHERE tenant_id=$1 AND session_id=$2),state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND session_id=$2`, r.Caller.TenantID, r.SessionID); err != nil {
					t.Fatal(err)
				}
				r.ExpectedVersion++
			}
			s, _ := postgres.NewSessionSuspends(db, suspendAllow, verify, v)
			if _, err := s.Suspend(context.Background(), r); !errors.Is(err, want) {
				t.Fatal(err, "want", want)
			}
		})
	}
}

func TestSessionSuspendExcludesAdmission(t *testing.T) {
	db, r, v, _ := suspendFixture(t, false)
	entered, release := make(chan struct{}), make(chan struct{})
	s, _ := postgres.NewSessionSuspends(db, suspendAllow, func(ctx context.Context, _ app.SuspendRequest, _ postgres.SuspendBoundary) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, v)
	suspended := make(chan error, 1)
	go func() { _, err := s.Suspend(context.Background(), r); suspended <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("suspend did not reach verification")
	}
	admission := make(chan error, 1)
	store, _ := postgres.NewStore(db)
	go func() {
		admission <- store.WithinTransaction(context.Background(), r.Caller.TenantID, func(ctx context.Context, repos persistence.Repositories) error {
			sess, err := repos.Sessions().GetForUpdate(ctx, r.SessionID)
			if err != nil {
				return err
			}
			return sess.Transition(domain.Active, sess.StateVersion(), time.Now())
		})
	}()
	select {
	case err := <-admission:
		close(release)
		t.Fatal("admission bypassed suspend lock", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	if err := <-suspended; err != nil {
		t.Fatal(err)
	}
	if err := <-admission; !errors.Is(err, domain.ErrIllegalTransition) {
		t.Fatal("suspended Session accepted admission", err)
	}
}
