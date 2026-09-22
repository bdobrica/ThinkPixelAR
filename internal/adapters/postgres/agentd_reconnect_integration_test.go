package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdadmission"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func TestAgentdReconnectAndRecovery(t *testing.T) {
	db, r := sandboxDatabaseFixture(t)
	ctx := context.Background()
	bindings, _ := postgres.NewSandboxBindings(db)
	if _, err := bindings.Reserve(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := bindings.BindReference(ctx, r.Scope.TenantID, r.Scope.SandboxID, "admission-fixture"); err != nil {
		t.Fatal(err)
	}
	store, _ := postgres.NewAgentdCredentials(db)
	id := transport.Identity{TenantID: r.Scope.TenantID, SandboxID: r.Scope.SandboxID, AttemptID: r.Scope.AttemptID}
	proof := make([]byte, 32)
	proof[0] = 3
	hash := sha256.Sum256(proof)
	now := time.Now().UTC().Truncate(time.Second)
	v, err := store.Version(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	grant := transport.CredentialGrant{Identity: id, Version: v, AuthorityDeadline: r.Deadline, AttemptDeadline: r.Deadline, BootstrapDeadline: r.Deadline}
	cid, _ := primitives.NewID(now)
	record := transport.CredentialRecord{Identity: id, CredentialID: cid, CertificateDigest: testDigest('a'), IssuerDigest: testDigest('b'), ProofDigest: "sha256:" + hex.EncodeToString(hash[:]), Bootstrap: true, NotBefore: now, ExpiresAt: now.Add(4 * time.Second)}
	if err := store.Register(ctx, transport.CredentialRequest{Identity: id}, grant, record); err != nil {
		t.Fatal(err)
	}
	peer := transport.Peer{Identity: id, CertificateDigest: record.CertificateDigest, ExpiresAt: record.ExpiresAt}
	if _, err := store.Reconnect(ctx, peer, peer.ExpiresAt); err != transport.ErrCredentialState {
		t.Fatal("unconsumed bootstrap reconnected")
	}
	first, err := store.ConsumeBootstrap(ctx, peer, proof, peer.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	// A new registry instance represents loss of all replica-local state.
	restarted, _ := postgres.NewAgentdCredentials(db)
	f := &admissionFixture{r: r}
	admission, err := agentdadmission.New(bindings, f, restarted, f, f)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := admission.Admit(ctx, peer, nil)
	second := transport.Connection{ID: accepted.ConnectionID, Epoch: accepted.Epoch, Deadline: accepted.Deadline}
	if err != nil {
		t.Fatal(err)
	}
	if second.Epoch <= first.Epoch || second.ID == first.ID {
		t.Fatal("epoch not replaced")
	}
	if err := store.CheckConnection(ctx, peer, first); err != transport.ErrCredentialState {
		t.Fatal("old epoch live")
	}
	if err := store.CloseConnection(ctx, id, first); err != nil {
		t.Fatal(err)
	}
	if err := restarted.CheckConnection(ctx, peer, second); err != nil {
		t.Fatal("old close erased replacement")
	}
	other := peer
	other.Identity.AttemptID = r.Scope.SessionID
	if _, err := store.Reconnect(ctx, other, other.ExpiresAt); err != transport.ErrCredentialState {
		t.Fatal("cross-attempt reconnect")
	}

	// Trusted recovery is disallowed while the latest credential is still live.
	recovery := record
	recoveryProof := make([]byte, 32)
	recoveryProof[0] = 4
	recoveryHash := sha256.Sum256(recoveryProof)
	recovery.ProofDigest = "sha256:" + hex.EncodeToString(recoveryHash[:])
	recovery.CredentialID, _ = primitives.NewID(time.Now())
	recovery.CertificateDigest = testDigest('c')
	recovery.ExpiresAt = now.Add(time.Minute)
	grant.Version, err = store.Version(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	request := transport.CredentialRequest{Identity: id, Recovery: true}
	if err := store.Register(ctx, request, grant, recovery); err != transport.ErrCredentialState {
		t.Fatal("early recovery accepted")
	}
	timer := time.NewTimer(time.Until(peer.ExpiresAt) + 20*time.Millisecond)
	defer timer.Stop()
	<-timer.C
	if _, err := store.Reconnect(ctx, peer, peer.ExpiresAt); err != transport.ErrCredentialState {
		t.Fatal("expired reconnect accepted")
	}
	if err := store.Register(ctx, request, grant, recovery); err != nil {
		t.Fatal("fenced recovery failed", err)
	}
	if err := store.Register(ctx, request, grant, recovery); err != transport.ErrCredentialState {
		t.Fatal("recovery replay accepted")
	}
	newPeer := transport.Peer{Identity: id, CertificateDigest: recovery.CertificateDigest, ExpiresAt: recovery.ExpiresAt}
	third, err := store.ConsumeBootstrap(ctx, newPeer, recoveryProof, newPeer.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if third.Epoch <= second.Epoch {
		t.Fatal("recovery reused epoch")
	}
	if err := store.CheckBootstrap(ctx, recovery); err != transport.ErrCredentialState {
		t.Fatal("recovery proof reusable")
	}

	renewal := recovery
	renewal.Bootstrap = false
	renewal.ProofDigest = ""
	renewal.CertificateDigest = testDigest('d')
	renewal.CredentialID, _ = primitives.NewID(time.Now())
	renewal.NotBefore = time.Now().UTC().Truncate(time.Second)
	renewal.ExpiresAt = renewal.NotBefore.Add(time.Minute)
	grant.Version, err = store.Version(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Register(ctx, transport.CredentialRequest{Identity: id, Peer: newPeer, ConnectionID: third.ID, Epoch: third.Epoch}, grant, renewal); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Reconnect(ctx, newPeer, newPeer.ExpiresAt); err != transport.ErrCredentialState {
		t.Fatal("predecessor reconnected after renewal")
	}
	latest := transport.Peer{Identity: id, CertificateDigest: renewal.CertificateDigest, ExpiresAt: renewal.ExpiresAt}
	results := make(chan transport.Connection, 2)
	for range 2 {
		go func() {
			c, e := store.Reconnect(ctx, latest, latest.ExpiresAt)
			if e != nil {
				results <- transport.Connection{}
				return
			}
			results <- c
		}()
	}
	a, b := <-results, <-results
	if a.Epoch == 0 || b.Epoch == 0 || a.Epoch == b.Epoch {
		t.Fatal("concurrent epochs not serialized")
	}
	older, newer := a, b
	if a.Epoch > b.Epoch {
		older, newer = b, a
	}
	if err := store.CheckConnection(ctx, latest, older); err != transport.ErrCredentialState {
		t.Fatal("concurrent old epoch accepted")
	}
	if err := store.CloseConnection(ctx, id, older); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckConnection(ctx, latest, newer); err != nil {
		t.Fatal("stale close erased newest epoch")
	}
}
