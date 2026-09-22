package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	"github.com/bdobrica/ThinkPixelAR/internal/app/reconciliation"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func TestAgentdExpiredIdentityFencesAndRetainsAmbiguousWork(t *testing.T) {
	for _, renew := range []bool{false, true} {
		t.Run(map[bool]string{false: "expired", true: "renewed"}[renew], func(t *testing.T) {
			db, policy, m, intent, _ := policyFixture(t, true)
			ctx := context.Background()
			scope := intent.Binding.Request.Scope
			bindings, _ := postgres.NewSandboxBindings(db)
			registry, _ := postgres.NewAgentdCredentials(db)
			id := transport.Identity{TenantID: scope.TenantID, SandboxID: scope.SandboxID, AttemptID: scope.AttemptID}
			version, err := registry.Version(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			cid, _ := primitives.NewID(now)
			proof := make([]byte, 32)
			hash := sha256.Sum256(proof)
			expires := now.Add(time.Second)
			record := transport.CredentialRecord{Identity: id, CredentialID: cid, CertificateDigest: testDigest('a'), IssuerDigest: testDigest('b'), ProofDigest: "sha256:" + hex.EncodeToString(hash[:]), Bootstrap: true, NotBefore: now.Add(-time.Second), ExpiresAt: expires}
			grant := transport.CredentialGrant{Identity: id, Version: version, AuthorityDeadline: m.Deadline, AttemptDeadline: m.Deadline, BootstrapDeadline: m.BootstrapDeadline}
			if err = registry.Register(ctx, transport.CredentialRequest{Identity: id}, grant, record); err != nil {
				t.Fatal(err)
			}
			peer := transport.Peer{Identity: id, CertificateDigest: record.CertificateDigest, ExpiresAt: expires}
			conn, err := registry.ConsumeBootstrap(ctx, peer, proof, expires)
			if err != nil {
				t.Fatal(err)
			}
			command := policyCommand(t, m, conn)
			if err = policy.AuthorizeFrame(ctx, intent, conn, command); err != nil {
				t.Fatal(err)
			}
			// A final report is admitted but cannot resolve this PENDING command.
			mid, _ := primitives.NewID(time.Now())
			report := &agentdv1.Envelope{Major: 1, Binding: m.Config.Binding, ConnectionId: string(conn.ID), ConnectionEpoch: conn.Epoch, MessageId: string(mid), Sequence: 1, Body: &agentdv1.Envelope_Observation{Observation: &agentdv1.Observation{Kind: agentdv1.Observation_PROCESS_STATUS, PayloadSchema: control.ShutdownSchema, Payload: []byte(control.ShutdownStopped)}}}
			if err = policy.AuthorizeFrame(ctx, intent, conn, report); err != nil {
				t.Fatal(err)
			}
			if renew {
				grant.Version, err = registry.Version(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				next := record
				next.CredentialID, _ = primitives.NewID(time.Now())
				next.CertificateDigest = testDigest('c')
				next.Bootstrap = false
				next.ProofDigest = ""
				next.ExpiresAt = m.Deadline
				if err = registry.Register(ctx, transport.CredentialRequest{Identity: id, Peer: peer, ConnectionID: conn.ID, Epoch: conn.Epoch}, grant, next); err != nil {
					t.Fatal(err)
				}
			}
			if err = bindings.RecoverExpiredAgentd(ctx, scope.TenantID, scope.SandboxID); err != nil {
				t.Fatal(err)
			}
			before, err := bindings.LoadCompute(ctx, scope.TenantID, scope.SandboxID)
			if err != nil || !before.Current {
				t.Fatal("live identity fenced", err)
			}
			<-time.After(time.Until(expires.Add(20 * time.Millisecond)))
			for range 2 {
				if err = bindings.RecoverExpiredAgentd(ctx, scope.TenantID, scope.SandboxID); err != nil {
					t.Fatal(err)
				}
			}
			after, err := bindings.LoadCompute(ctx, scope.TenantID, scope.SandboxID)
			if err != nil {
				t.Fatal(err)
			}
			if renew {
				if !after.Current || after.Desired != sandbox.ComputeRunning {
					t.Fatal("old expiry fenced renewal")
				}
				return
			}
			if after.Current || after.Desired != sandbox.ComputeReleased || !after.ReleaseAuthorized || after.Binding.ProviderReference != intent.Binding.ProviderReference {
				t.Fatal("missing exact fenced cleanup")
			}
			if _, err = registry.Reconnect(ctx, peer, m.Deadline); err == nil {
				t.Fatal("stale reconnect accepted")
			}
			if err = policy.AuthorizeFrame(ctx, intent, conn, report); err == nil {
				t.Fatal("stale final report accepted")
			}
			outcome, err := policy.CommandOutcome(ctx, scope.TenantID, scope.SandboxID, primitives.ID(command.OperationId), command.RequestDigest)
			if err != nil || outcome != transport.DispatchPending {
				t.Fatal("ambiguous command outcome changed", outcome, err)
			}
			for _, query := range []string{`SELECT count(*) FROM reconciliation_work WHERE tenant_id=$1 AND work_kind='sandbox.recover' AND state='PENDING'`, `SELECT count(*) FROM cleanup_intents WHERE tenant_id=$1 AND state='PENDING'`, `SELECT count(*) FROM runtime_events WHERE tenant_id=$1 AND event_type='session.degraded'`} {
				var count int
				if err = db.QueryRow(query, scope.TenantID).Scan(&count); err != nil || count != 1 {
					t.Fatal("recovery missing or duplicated", count, err)
				}
			}
			restarted, _ := postgres.NewSandboxBindings(db)
			provider := &expiredCleanupFixture{intent: after}
			reconciler, err := reconciliation.NewCompute(restarted, provider, provider)
			if err != nil {
				t.Fatal(err)
			}
			observed, err := reconciler.Reconcile(ctx, scope.TenantID, scope.SandboxID)
			if err != nil || !observed.Converged || observed.State != sandbox.Released || !provider.released {
				t.Fatal("restart did not drain exact cleanup", err)
			}
			var pending int
			if err = db.QueryRow(`SELECT count(*) FROM reconciliation_work WHERE tenant_id=$1 AND work_kind='sandbox.recover' AND state='PENDING'`, scope.TenantID).Scan(&pending); err != nil || pending != 1 {
				t.Fatal("cleanup erased replacement decision", err)
			}

		})
	}
}

// Provider absence is a fixture here; live Kata deletion is separate acceptance.
type expiredCleanupFixture struct {
	intent   sandbox.ComputeIntent
	released bool
}

func (f *expiredCleanupFixture) Acquire(context.Context, sandbox.AcquireRequest) (sandbox.Handle, error) {
	return sandbox.Handle{}, sandbox.ErrPermission
}
func (f *expiredCleanupFixture) CheckCompute(context.Context, sandbox.Scope) error {
	return sandbox.ErrPermission
}
func (f *expiredCleanupFixture) Release(_ context.Context, tenant, id primitives.ID, op sandbox.Operation) error {
	s := f.intent.Binding.Request.Scope
	if tenant != s.TenantID || id != s.SandboxID || op != f.intent.Operation {
		return sandbox.ErrIntegrity
	}
	f.released = true
	return nil
}
func (f *expiredCleanupFixture) Get(context.Context, primitives.ID, primitives.ID) (sandbox.Status, error) {
	if !f.released {
		return sandbox.Status{}, sandbox.ErrIntegrity
	}
	return sandbox.Status{}, sandbox.ErrNotFound
}
