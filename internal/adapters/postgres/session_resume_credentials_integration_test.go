package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	app "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
)

func TestSessionResumeCredentialRetirement(t *testing.T) {
	ctx := context.Background()
	db, r, v, old := resumeFixture(t, true)
	bindings, _ := postgres.NewSandboxBindings(db)
	intent, err := bindings.LoadCompute(ctx, r.Caller.TenantID, old)
	if err != nil {
		t.Fatal(err)
	}
	if err = bindings.RecordCompute(ctx, intent, sandbox.ComputeObservation{State: sandbox.Released, Code: "COMPUTE_ABSENT", Converged: true}); err != nil {
		t.Fatal(err)
	}
	s := resumeStore(t, db, v)
	if _, _, err = s.Prepare(ctx, r); !errors.Is(err, workspace.ErrConflict) {
		t.Fatal("queued Secret cleanup admitted", err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM session_resume_operations WHERE tenant_id=$1`, r.Caller.TenantID).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial allocation", count, err)
	}
	if _, err = db.Exec(`UPDATE agentd_bootstrap_delivery SET cleaned=true WHERE tenant_id=$1`, r.Caller.TenantID); err != nil {
		t.Fatal(err)
	}
	i, _, err := s.Prepare(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a stale projection discovered after allocation. Publication and
	// an in-progress retry must independently reject it, even with allow callbacks.
	connection := concurrencyIDs(t, time.Now(), 1)[0]
	if _, err = db.Exec(`UPDATE agentd_credential_state SET connection_id=$2,connection_digest=$3,connection_deadline=clock_timestamp()+interval '1 minute' WHERE tenant_id=$1`, r.Caller.TenantID, connection, testDigest('b')); err != nil {
		t.Fatal(err)
	}
	p := &resumeProvider{}
	o, err := p.Reconcile(ctx, i)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Complete(ctx, r, o); !errors.Is(err, workspace.ErrConflict) {
		t.Fatal("stale connection published", err)
	}
	if _, _, err = s.Prepare(ctx, r); !errors.Is(err, workspace.ErrConflict) {
		t.Fatal("stale connection replay admitted", err)
	}
	if _, err = db.Exec(`UPDATE agentd_credential_state SET connection_id=NULL,connection_digest=NULL,connection_deadline=NULL WHERE tenant_id=$1`, r.Caller.TenantID); err != nil {
		t.Fatal(err)
	}
	// An expired delivery is harmless authority, though cleanup remains pending.
	// A future delivery must block even if its certificate has already expired.
	for _, expired := range []bool{false, true} {
		credential := concurrencyIDs(t, time.Now(), 1)[0]
		digest := testDigest('d')
		if expired {
			digest = testDigest('e')
		}
		if _, err = db.Exec(`INSERT INTO agentd_credentials(tenant_id,sandbox_binding_id,credential_id,certificate_digest,issuer_digest,proof_digest,bootstrap,not_before,expires_at) VALUES($1,$2,$3,$4,$4,$4,true,clock_timestamp()-interval '2 minutes',clock_timestamp()-interval '1 minute')`, r.Caller.TenantID, old, credential, digest); err != nil {
			t.Fatal(err)
		}
		expires := time.Now().Add(time.Minute)
		if expired {
			expires = time.Now().Add(-time.Minute)
		}
		if _, err = db.Exec(`INSERT INTO agentd_bootstrap_delivery(tenant_id,credential_id,reference,provider_uid,expires_at,cleanup_requested) VALUES($1,$2,'{}','fixture-secret',$3,true)`, r.Caller.TenantID, credential, expires); err != nil {
			t.Fatal(err)
		}
		if !expired {
			if _, err = s.Complete(ctx, r, o); !errors.Is(err, workspace.ErrConflict) {
				t.Fatal("unexpired delivery published", err)
			}
			if _, err = db.Exec(`UPDATE agentd_bootstrap_delivery SET cleaned=true WHERE tenant_id=$1 AND credential_id=$2`, r.Caller.TenantID, credential); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got, err := s.Complete(ctx, r, o); err != nil || got.State != "IDLE" {
		t.Fatal(got, err)
	}
	// Recreate the registry as on another AR replica. The still-unexpired old
	// certificate cannot bootstrap, reconnect or obtain a renewal version.
	registry, _ := postgres.NewAgentdCredentials(db)
	peer := transport.Peer{Identity: transport.Identity{TenantID: r.Caller.TenantID, SandboxID: old, AttemptID: intent.Binding.Request.Scope.AttemptID}, CertificateDigest: testDigest('b')}
	if err = db.QueryRow(`SELECT expires_at FROM agentd_credentials WHERE tenant_id=$1 AND certificate_digest=$2`, r.Caller.TenantID, peer.CertificateDigest).Scan(&peer.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	if !time.Now().Before(peer.ExpiresAt) {
		t.Fatal("test must exercise an unexpired certificate")
	}
	if _, err = registry.Version(ctx, peer.Identity); err != transport.ErrCredentialState {
		t.Fatal("old identity eligible for issuance", err)
	}
	if err = registry.CheckConnection(ctx, peer, transport.Connection{ID: connection, Epoch: 1, Deadline: peer.ExpiresAt}); err != transport.ErrCredentialState {
		t.Fatal("old stream accepted", err)
	}
	if _, err = registry.ConsumeBootstrap(ctx, peer, make([]byte, 32), peer.ExpiresAt); err != transport.ErrCredentialState {
		t.Fatal("old bootstrap accepted", err)
	}
	// No new identity, credential or Execution was minted by resume.
	if err = db.QueryRow(`SELECT count(*) FROM agentd_credentials WHERE tenant_id=$1 AND sandbox_binding_id<>$2`, r.Caller.TenantID, old).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestSessionResumeRejectsValidOldBootstrap(t *testing.T) {
	ctx := context.Background()
	db, r, v, old := suspendFixture(t, true)
	// Make the seeded bootstrap internally consistent, with a known valid proof.
	// Credential history is immutable: insert a new record instead of rewriting it.
	proof := make([]byte, 32)
	hash := sha256.Sum256(proof)
	digest := "sha256:" + hex.EncodeToString(hash[:])
	credential := concurrencyIDs(t, time.Now(), 1)[0]
	expires := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Microsecond)
	if _, err := db.Exec(`INSERT INTO agentd_credentials(tenant_id,sandbox_binding_id,credential_id,certificate_digest,issuer_digest,proof_digest,bootstrap,not_before,expires_at) VALUES($1,$2,$3,$4,$4,$5,true,clock_timestamp(),$6)`, r.Caller.TenantID, old, credential, testDigest('f'), digest, expires); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE agentd_credential_state SET latest_digest=$2,issued_at=clock_timestamp() WHERE tenant_id=$1`, r.Caller.TenantID, testDigest('f')); err != nil {
		t.Fatal(err)
	}
	susp, _ := postgres.NewSessionSuspends(db, suspendAllow, suspendVerify, v)
	got, err := susp.Suspend(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	bindings, _ := postgres.NewSandboxBindings(db)
	intent, err := bindings.LoadCompute(ctx, r.Caller.TenantID, old)
	if err != nil {
		t.Fatal(err)
	}
	if err = bindings.RecordCompute(ctx, intent, sandbox.ComputeObservation{State: sandbox.Released, Code: "COMPUTE_ABSENT", Converged: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE agentd_bootstrap_delivery SET cleaned=true WHERE tenant_id=$1`, r.Caller.TenantID); err != nil {
		t.Fatal(err)
	}
	rr := app.ResumeRequest(r)
	rr.ExpectedVersion = got.Version
	rr.OperationID = concurrencyIDs(t, time.Now(), 1)[0]
	s := resumeStore(t, db, v)
	i, _, err := s.Prepare(ctx, rr)
	if err != nil {
		t.Fatal(err)
	}
	p := &resumeProvider{}
	o, err := p.Reconcile(ctx, i)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Complete(ctx, rr, o); err != nil {
		t.Fatal(err)
	}
	registry, _ := postgres.NewAgentdCredentials(db)
	peer := transport.Peer{Identity: transport.Identity{TenantID: r.Caller.TenantID, SandboxID: old, AttemptID: intent.Binding.Request.Scope.AttemptID}, CertificateDigest: testDigest('f'), ExpiresAt: expires}
	if _, err = registry.ConsumeBootstrap(ctx, peer, proof, expires); err != transport.ErrCredentialState {
		t.Fatal("valid retired bootstrap accepted", err)
	}
}
