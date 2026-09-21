package postgres_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func TestAgentdCredentialRegistryIsolationAndReplay(t *testing.T) {
	db, r := sandboxDatabaseFixture(t)
	ctx := context.Background()
	bindings, _ := postgres.NewSandboxBindings(db)
	if _, err := bindings.Reserve(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := bindings.BindReference(ctx, r.Scope.TenantID, r.Scope.SandboxID, "fixture-provider-reference"); err != nil {
		t.Fatal(err)
	}
	store, _ := postgres.NewAgentdCredentials(db)
	id := transport.Identity{TenantID: r.Scope.TenantID, SandboxID: r.Scope.SandboxID, AttemptID: r.Scope.AttemptID}
	v, err := store.Version(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	proof := make([]byte, 32)
	proof[0] = 7
	hash := sha256.Sum256(proof)
	cid, _ := primitives.NewID(now)
	record := transport.CredentialRecord{Identity: id, CredentialID: cid, CertificateDigest: testDigest('a'), IssuerDigest: testDigest('b'), ProofDigest: "sha256:" + hex.EncodeToString(hash[:]), Bootstrap: true, NotBefore: now, ExpiresAt: now.Add(5 * time.Minute)}
	request := transport.CredentialRequest{Identity: id}
	grant := transport.CredentialGrant{Identity: id, Version: v, AuthorityDeadline: r.Deadline, AttemptDeadline: r.Deadline, BootstrapDeadline: r.Deadline}
	if err := store.Register(ctx, request, grant, record); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckBootstrap(ctx, record); err != nil {
		t.Fatal("registered bootstrap rejected for projection")
	}
	wrongRecord := record
	wrongRecord.ProofDigest = testDigest('f')
	if err := store.CheckBootstrap(ctx, wrongRecord); err != transport.ErrCredentialState {
		t.Fatal("mismatched projection record accepted")
	}
	checkAgentdRLS(t, db, id)
	if err := store.Register(ctx, request, grant, record); err != transport.ErrCredentialState {
		t.Fatal("registration replay accepted")
	}
	peer := transport.Peer{Identity: id, CertificateDigest: record.CertificateDigest, ExpiresAt: record.ExpiresAt}
	deadline := now.Add(4 * time.Minute)
	for _, which := range []string{"tenant", "sandbox", "attempt", "certificate", "expiry", "proof"} {
		t.Run(which, func(t *testing.T) {
			p := peer
			bad := append([]byte(nil), proof...)
			other, _ := primitives.NewID(now)
			switch which {
			case "tenant":
				p.Identity.TenantID = other
			case "sandbox":
				p.Identity.SandboxID = other
			case "attempt":
				p.Identity.AttemptID = other
			case "certificate":
				p.CertificateDigest = testDigest('c')
			case "expiry":
				p.ExpiresAt = p.ExpiresAt.Add(time.Second)
			case "proof":
				bad[0]++
			}
			if _, err := store.ConsumeBootstrap(ctx, p, bad, deadline); err != transport.ErrCredentialState {
				t.Fatal("cross-binding or incorrect proof accepted")
			}
		})
	}
	var wg sync.WaitGroup
	results := make(chan transport.Connection, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := store.ConsumeBootstrap(ctx, peer, proof, deadline)
			results <- c
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else if err != transport.ErrCredentialState {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("bootstrap must have exactly one winner")
	}
	var conn transport.Connection
	for c := range results {
		if c.Epoch > 0 {
			conn = c
		}
	}
	// New adapter instance proves consumption/epoch state is not process-local.
	restarted, _ := postgres.NewAgentdCredentials(db)
	if err := restarted.CheckBootstrap(ctx, record); err != transport.ErrCredentialState {
		t.Fatal("consumed bootstrap remained projectable")
	}
	if _, err := restarted.ConsumeBootstrap(ctx, peer, proof, deadline); err != transport.ErrCredentialState {
		t.Fatal("bootstrap replay after restart")
	}
	if err := restarted.CheckConnection(ctx, peer, conn); err != nil {
		t.Fatal(err)
	}
	// Two registrations from one version cannot both publish renewal credentials.
	version, err := restarted.Version(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	renewGrant := grant
	renewGrant.Version = version
	renewRequest := transport.CredentialRequest{Identity: id, Peer: peer, ConnectionID: conn.ID, Epoch: conn.Epoch}
	renewResults := make(chan error, 2)
	for _, digest := range []string{testDigest('c'), testDigest('d')} {
		renewID, _ := primitives.NewID(time.Now())
		renew := transport.CredentialRecord{Identity: id, CredentialID: renewID, CertificateDigest: digest, IssuerDigest: record.IssuerDigest, NotBefore: now, ExpiresAt: now.Add(10 * time.Minute)}
		go func() { renewResults <- restarted.Register(ctx, renewRequest, renewGrant, renew) }()
	}
	winners := 0
	for range 2 {
		if err := <-renewResults; err == nil {
			winners++
		} else if err != transport.ErrCredentialState {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatal("competing renewals both committed")
	}
	if _, err := db.ExecContext(ctx, `UPDATE agentd_credentials SET issuer_digest=$3 WHERE tenant_id=$1 AND credential_id=$2`, id.TenantID, record.CredentialID, testDigest('f')); err == nil {
		t.Fatal("credential history mutated")
	}
	stale := conn
	stale.Epoch++
	if err := restarted.CheckConnection(ctx, peer, stale); err != transport.ErrCredentialState {
		t.Fatal("wrong epoch accepted")
	}
	if err := restarted.CloseConnection(ctx, id, stale); err != nil {
		t.Fatal(err)
	}
	if err := restarted.CheckConnection(ctx, peer, conn); err != nil {
		t.Fatal("stale close invalidated live epoch")
	}
	if err := restarted.CloseConnection(ctx, id, conn); err != nil {
		t.Fatal(err)
	}
	if err := restarted.CheckConnection(ctx, peer, conn); err != transport.ErrCredentialState {
		t.Fatal("closed connection accepted")
	}
	if _, err := restarted.ConsumeBootstrap(ctx, peer, proof, deadline); err != transport.ErrCredentialState {
		t.Fatal("close resurrected bootstrap")
	}
}

func checkAgentdRLS(t *testing.T, db *sql.DB, id transport.Identity) {
	t.Helper()
	role := "agd_rls_" + strings.ReplaceAll(string(id.SandboxID), "-", "")
	if _, err := db.Exec(`CREATE ROLE ` + role + ` NOLOGIN NOSUPERUSER NOBYPASSRLS`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Exec(`REVOKE ALL ON agentd_credential_state,agentd_credentials FROM ` + role)
		_, _ = db.Exec(`REVOKE USAGE ON SCHEMA public FROM ` + role)
		_, _ = db.Exec(`DROP ROLE ` + role)
	}()
	if _, err := db.Exec(`GRANT USAGE ON SCHEMA public TO ` + role); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`GRANT SELECT ON agentd_credential_state,agentd_credentials TO ` + role); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SET LOCAL ROLE ` + role); err != nil {
		t.Fatal(err)
	}
	other, _ := primitives.NewID(time.Now())
	for _, tenant := range []primitives.ID{id.TenantID, other} {
		if _, err = tx.Exec(`SELECT set_config('thinkpixelar.tenant_id',$1,true)`, tenant); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"agentd_credential_state", "agentd_credentials"} {
			var count int
			if err = tx.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if tenant == id.TenantID {
				want = 1
			}
			if count != want {
				t.Fatal("credential RLS leaked or hid rows")
			}
		}
	}
}

func TestAgentdCredentialRegistrationFencesCancellation(t *testing.T) {
	db, r := sandboxDatabaseFixture(t)
	ctx := context.Background()
	bindings, _ := postgres.NewSandboxBindings(db)
	if _, err := bindings.Reserve(ctx, r); err != nil {
		t.Fatal(err)
	}
	store, _ := postgres.NewAgentdCredentials(db)
	id := transport.Identity{TenantID: r.Scope.TenantID, SandboxID: r.Scope.SandboxID, AttemptID: r.Scope.AttemptID}
	v, err := store.Version(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// This transition uses the same authoritative rows locked by registration.
	if _, err := db.ExecContext(ctx, `UPDATE executions SET state='CANCELLING',state_version=state_version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND execution_id=$2`, id.TenantID, r.Scope.ExecutionID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	cid, _ := primitives.NewID(now)
	c := transport.CredentialRecord{Identity: id, CredentialID: cid, CertificateDigest: testDigest('d'), IssuerDigest: testDigest('b'), ProofDigest: testDigest('e'), Bootstrap: true, NotBefore: now, ExpiresAt: now.Add(time.Minute)}
	g := transport.CredentialGrant{Identity: id, Version: v, AuthorityDeadline: r.Deadline, AttemptDeadline: r.Deadline, BootstrapDeadline: r.Deadline}
	if err := store.Register(ctx, transport.CredentialRequest{Identity: id}, g, c); err != transport.ErrCredentialState {
		t.Fatal("cancelled execution registered credential")
	}
}
