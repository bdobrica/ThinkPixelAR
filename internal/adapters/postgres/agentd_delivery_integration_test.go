package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/bootstrap"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"sync"
	"testing"
	"time"
)

func TestAgentdDeliveryJournal(t *testing.T) {
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

	journal, err := postgres.NewAgentdDelivery(db)
	if err != nil {
		t.Fatal(err)
	}
	ref := bootstrap.Reference{Namespace: "sandboxes", Name: "agentd-" + string(cid), BundleDigest: testDigest('e'), Record: record}
	for _, mode := range []string{"sandbox", "attempt", "issuer", "validity"} {
		bad := ref
		switch mode {
		case "sandbox":
			bad.Record.Identity.SandboxID = r.Scope.SessionID
		case "attempt":
			bad.Record.Identity.AttemptID = r.Scope.SessionID
		case "issuer":
			bad.Record.IssuerDigest = testDigest('f')
		case "validity":
			bad.Record.ExpiresAt = bad.Record.ExpiresAt.Add(time.Second)
		}
		if journal.SavePlan(ctx, bad) == nil {
			t.Fatal("mismatched credential journaled", mode)
		}
	}
	var contenders sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		contenders.Go(func() { results <- journal.SavePlan(ctx, ref) })
	}
	contenders.Wait()
	close(results)
	winners := 0
	for result := range results {
		if result == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatal("publication claim winners", winners)
	}
	checkAgentdRLS(t, db, id, "agentd_bootstrap_delivery")
	if err = journal.SavePlan(ctx, ref); err == nil {
		t.Fatal("publication claim replay accepted")
	}
	restarted, _ := postgres.NewAgentdDelivery(db)
	entry, err := restarted.LoadDelivery(ctx, id.TenantID, cid)
	if err != nil || entry.Reference != ref {
		t.Fatal("plan did not survive restart", err)
	}
	if _, err = restarted.LoadDelivery(ctx, r.Scope.SessionID, cid); err == nil {
		t.Fatal("cross-tenant delivery visible")
	}
	if err = journal.CompleteCleanup(ctx, ref); err == nil {
		t.Fatal("unknown UID cleanup completed")
	}
	ref.UID = "provider-uid"
	if err = journal.BindUID(ctx, ref); err != nil {
		t.Fatal("uid", err)
	}
	other := ref
	other.UID = "replacement"
	if err = journal.BindUID(ctx, other); err == nil {
		t.Fatal("replacement UID adopted")
	}
	if err = journal.CompleteCleanup(ctx, ref); err == nil {
		t.Fatal("unscheduled cleanup completed")
	}
	if err = journal.RequestCleanup(ctx, id.TenantID, cid); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE agentd_bootstrap_delivery SET retry_after=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND credential_id=$2`, id.TenantID, cid); err != nil {
		t.Fatal(err)
	}
	pending, err := journal.DueCleanup(ctx, id.TenantID, 1)
	if err != nil || len(pending) != 1 {
		t.Fatal("cleanup not queued", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE agentd_bootstrap_delivery SET cleanup_requested=false WHERE tenant_id=$1 AND credential_id=$2`, id.TenantID, cid); err == nil {
		t.Fatal("cleanup intent reversed")
	}
	if err = journal.CompleteCleanup(ctx, other); err == nil {
		t.Fatal("replacement cleanup accepted")
	}
	if err = journal.CompleteCleanup(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err = journal.CompleteCleanup(ctx, ref); err != nil {
		t.Fatal("cleanup replay", err)
	}
	if err = journal.BindUID(ctx, ref); err == nil {
		t.Fatal("cleaned plan revived")
	}
	entries, err := journal.DueCleanup(ctx, id.TenantID, 128)
	if err != nil || len(entries) != 0 {
		t.Fatal("completed cleanup scheduled", err)
	}
	entry, err = restarted.LoadDelivery(ctx, id.TenantID, cid)
	if err != nil || !entry.Cleaned || !entry.CleanupRequested {
		t.Fatal("cleanup did not survive restart", err)
	}
}
