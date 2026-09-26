package postgres_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentd"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdadmission"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"google.golang.org/protobuf/proto"
)

func policyFixture(t *testing.T, unissued ...bool) (*sql.DB, *postgres.AgentdPolicy, postgres.AgentdMaterialization, sandbox.ComputeIntent, transport.Connection) {
	return policyFixtureConfigured(t, len(unissued) == 1 && unissued[0], nil)
}

func policyFixtureConfigured(t *testing.T, unissued bool, configure func(*agentd.Config), materialize ...func(*postgres.AgentdMaterialization)) (*sql.DB, *postgres.AgentdPolicy, postgres.AgentdMaterialization, sandbox.ComputeIntent, transport.Connection) {
	t.Helper()
	db, r := sandboxDatabaseFixture(t, "thinkpixelar/local")
	ctx := context.Background()
	bindings, _ := postgres.NewSandboxBindings(db)
	if _, err := bindings.Reserve(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := bindings.BindReference(ctx, r.Scope.TenantID, r.Scope.SandboxID, "admission-fixture"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../deploy/agentd/config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	config, err := agentd.DecodeConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	s := r.Scope
	config.Binding = &agentdv1.Binding{TenantId: string(s.TenantID), SessionId: string(s.SessionID), ExecutionId: string(s.ExecutionID), AttemptId: string(s.AttemptID), SandboxBindingId: string(s.SandboxID), SessionGeneration: s.Generation}
	config.Capabilities = append(config.Capabilities, control.Capability, "rotation.v1")
	config.RequiredCapabilities = append(config.RequiredCapabilities, control.Capability, "rotation.v1")
	handle, _ := primitives.NewID(time.Now())
	m := postgres.AgentdMaterialization{Config: config, Challenge: make([]byte, 32), Revision: testDigest('e'), RequestDigest: r.Operation.Digest, HarnessHandle: string(handle), Deadline: time.Now().Add(4 * time.Minute).UTC().Truncate(time.Microsecond), BootstrapDeadline: time.Now().Add(time.Minute).UTC().Truncate(time.Microsecond)}
	m.Config.ControlDeadlineUnixMS = m.Deadline.UnixMilli()
	m.Config.Harness.StopGraceMS = 1000
	if configure != nil {
		configure(&m.Config)
	}
	if slices.Contains(m.Config.RequiredCapabilities, control.ThreadCapability) {
		m.HarnessNegotiationDigest = testDigest('d')
	}
	var issuer string
	if err = db.QueryRow(`SELECT grant_digest,authority_namespace,authority_reference FROM executions WHERE tenant_id=$1 AND execution_id=$2`, s.TenantID, s.ExecutionID).Scan(&m.GrantDigest, &issuer, &m.AuthorityReference); err != nil {
		t.Fatal(err)
	}
	policy, err := postgres.NewAgentdLocalPolicy(db, "local", m.Revision, issuer)
	if err != nil {
		t.Fatal(err)
	}
	for _, configure := range materialize {
		configure(&m)
	}
	if err = policy.Register(ctx, m); err != nil {
		t.Fatal(err)
	}
	if unissued {
		intent, err := bindings.LoadCompute(ctx, s.TenantID, s.SandboxID)
		if err != nil {
			t.Fatal(err)
		}
		return db, policy, m, intent, transport.Connection{}
	}
	registry, _ := postgres.NewAgentdCredentials(db)
	id := transport.Identity{TenantID: s.TenantID, SandboxID: s.SandboxID, AttemptID: s.AttemptID}
	v, err := registry.Version(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	cid, _ := primitives.NewID(now)
	proof := make([]byte, 32)
	sum := sha256.Sum256(proof)
	record := transport.CredentialRecord{Identity: id, CredentialID: cid, CertificateDigest: testDigest('a'), IssuerDigest: testDigest('b'), ProofDigest: "sha256:" + hex.EncodeToString(sum[:]), Bootstrap: true, NotBefore: now, ExpiresAt: now.Add(5 * time.Minute)}
	if err = registry.Register(ctx, transport.CredentialRequest{Identity: id}, transport.CredentialGrant{Identity: id, Version: v, AuthorityDeadline: r.Deadline, AttemptDeadline: r.Deadline, BootstrapDeadline: r.Deadline}, record); err != nil {
		t.Fatal(err)
	}
	conn, err := registry.ConsumeBootstrap(ctx, transport.Peer{Identity: id, CertificateDigest: record.CertificateDigest, ExpiresAt: record.ExpiresAt}, proof, m.Deadline)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := bindings.LoadCompute(ctx, s.TenantID, s.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	return db, policy, m, intent, conn
}
func policyCommand(t *testing.T, m postgres.AgentdMaterialization, c transport.Connection) *agentdv1.Envelope {
	t.Helper()
	op, _ := primitives.NewID(time.Now())
	mid, _ := primitives.NewID(time.Now())
	raw, _ := json.Marshal(m.Config)
	digest := sandbox.Digest(raw)
	return &agentdv1.Envelope{Major: 1, Binding: m.Config.Binding, ConnectionId: string(c.ID), ConnectionEpoch: c.Epoch, MessageId: string(mid), Sequence: 1, HarnessHandle: m.HarnessHandle, OperationId: string(op), RequestDigest: control.Digest(agentdv1.Command_START, digest, m.HarnessHandle), SentUnixMs: time.Now().UnixMilli(), DeadlineUnixMs: time.Now().Add(30 * time.Second).UnixMilli(), Body: &agentdv1.Envelope_Command{Command: &agentdv1.Command{Kind: agentdv1.Command_START, ConfigurationDigest: digest, PayloadSchema: control.Capability}}}
}
func TestAgentdPolicyDurableReplay(t *testing.T) {
	db, p, m, i, c := policyFixture(t)
	ctx := context.Background()
	s := i.Binding.Request.Scope
	a, err := p.AuthorizeTransport(ctx, i, transport.AcceptStream)
	if err != nil || !proto.Equal(a.Expected.Binding, m.Config.Binding) {
		t.Fatal("admission", err)
	}
	a.Expected.Binding.AttemptId = "mutated"
	if _, err = p.AuthorizeTransport(ctx, i, transport.AcceptStream); err != nil {
		t.Fatal("returned pointer mutated storage")
	}
	f := policyCommand(t, m, c)
	if _, err := p.CommandOutcome(ctx, s.TenantID, s.SandboxID, primitives.ID(f.OperationId), f.RequestDigest); err != transport.ErrCommandNotFound {
		t.Fatal("new command not distinguished from failed lookup", err)
	}
	var wg sync.WaitGroup
	var wins atomic.Int32
	for range 8 {
		wg.Go(func() {
			if p.AuthorizeFrame(ctx, i, c, f) == nil {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("dispatch claims", wins.Load())
	}
	restarted, _ := postgres.NewAgentdLocalPolicy(db, "local", m.Revision, "thinkpixelar/local")
	outcome, err := restarted.CommandOutcome(ctx, s.TenantID, s.SandboxID, primitives.ID(f.OperationId), f.RequestDigest)
	if err != nil || outcome != "PENDING" {
		t.Fatal("lost durable reservation", outcome, err)
	}
	if _, err := restarted.CommandOutcome(ctx, s.TenantID, s.SandboxID, primitives.ID(f.OperationId), testDigest('f')); err != postgres.ErrAgentdPolicy {
		t.Fatal("conflicting command reported absent", err)
	}
	if restarted.AuthorizeFrame(ctx, i, c, f) == nil {
		t.Fatal("ambiguous dispatch retried")
	}
	mid, _ := primitives.NewID(time.Now())
	ack := &agentdv1.Envelope{Major: 1, Binding: m.Config.Binding, ConnectionId: string(c.ID), ConnectionEpoch: c.Epoch, MessageId: string(mid), Sequence: 1, HarnessHandle: m.HarnessHandle, OperationId: f.OperationId, RequestDigest: f.RequestDigest, Body: &agentdv1.Envelope_Acknowledgement{Acknowledgement: &agentdv1.Acknowledgement{MessageId: f.MessageId, RequestDigest: f.RequestDigest, AcceptedSequence: f.Sequence}}}
	bad := proto.Clone(ack).(*agentdv1.Envelope)
	bad.GetAcknowledgement().MessageId = string(mid)
	if p.AuthorizeFrame(ctx, i, c, bad) == nil {
		t.Fatal("uncorrelated ack accepted")
	}
	if err = p.AuthorizeFrame(ctx, i, c, ack); err != nil {
		t.Fatal("ack", err)
	}
	if err = p.AuthorizeFrame(ctx, i, c, ack); err != nil {
		t.Fatal("identical ack replay", err)
	}
	outcome, err = restarted.CommandOutcome(ctx, s.TenantID, s.SandboxID, primitives.ID(f.OperationId), f.RequestDigest)
	if err != nil || outcome != "ACKNOWLEDGED" {
		t.Fatal("outcome not durable", outcome, err)
	}
	if p.AuthorizeFrame(ctx, i, c, f) == nil {
		t.Fatal("acknowledged dispatch retried")
	}
	conflict := proto.Clone(ack).(*agentdv1.Envelope)
	conflict.Sequence = 2
	conflict.Body = &agentdv1.Envelope_Failure{Failure: &agentdv1.Failure{Code: agentdv1.Failure_OUTCOME_UNKNOWN}}
	if p.AuthorizeFrame(ctx, i, c, conflict) == nil {
		t.Fatal("terminal outcome overwritten")
	}
	checkAgentdRLS(t, db, transport.Identity{TenantID: s.TenantID, SandboxID: s.SandboxID, AttemptID: s.AttemptID}, "agentd_admission", "agentd_commands")
	if err = p.Revoke(ctx, s.TenantID, s.SandboxID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.AuthorizeTransport(ctx, i, transport.AcceptStream); err == nil {
		t.Fatal("revoked admission accepted")
	}
	if p.AuthorizeFrame(ctx, i, c, ack) == nil {
		t.Fatal("revoked frame accepted")
	}
	if _, err = p.CommandOutcome(ctx, s.TenantID, s.SandboxID, primitives.ID(f.OperationId), f.RequestDigest); err != nil {
		t.Fatal("revocation lost history")
	}
	if _, err = db.Exec(`UPDATE agentd_admission SET revoked=false WHERE tenant_id=$1 AND sandbox_binding_id=$2`, s.TenantID, s.SandboxID); err == nil {
		t.Fatal("revocation reversed")
	}
}
func TestAgentdPolicyRejectsChangedAuthorityAndFrames(t *testing.T) {
	db, p, m, i, c := policyFixture(t)
	ctx := context.Background()
	s := i.Binding.Request.Scope
	for _, mode := range []string{"", "thinkpixelag"} {
		if _, err := postgres.NewAgentdLocalPolicy(db, mode, m.Revision, "thinkpixelar/local"); err == nil {
			t.Fatal("mode fallback")
		}
	}
	for _, purpose := range []transport.Purpose{transport.RecoverBootstrap, "UNKNOWN"} {
		if _, err := p.AuthorizeTransport(ctx, i, purpose); err == nil {
			t.Fatal("unsupported authority")
		}
	}
	changed, _ := postgres.NewAgentdLocalPolicy(db, "local", testDigest('f'), "thinkpixelar/local")
	if _, err := changed.AuthorizeTransport(ctx, i, transport.AcceptStream); err == nil {
		t.Fatal("revision widened")
	}
	if _, err := postgres.NewAgentdLocalPolicy(db, "local", m.Revision, "other-issuer"); err == nil {
		t.Fatal("issuer widened")
	}
	f := policyCommand(t, m, c)
	for _, which := range []string{"binding", "epoch", "payload", "schema", "handle", "digest", "gap", "expired"} {
		t.Run(which, func(t *testing.T) {
			bad := proto.Clone(f).(*agentdv1.Envelope)
			switch which {
			case "binding":
				bad.Binding.SessionGeneration++
			case "epoch":
				bad.ConnectionEpoch++
			case "payload":
				bad.GetCommand().Payload = []byte("not allowed")
			case "schema":
				bad.GetCommand().PayloadSchema = "unknown"
			case "handle":
				bad.HarnessHandle = bad.OperationId
			case "digest":
				bad.RequestDigest = testDigest('f')
			case "gap":
				bad.Sequence = 2
			case "expired":
				bad.DeadlineUnixMs = 1
			}
			if p.AuthorizeFrame(ctx, i, c, bad) == nil {
				t.Fatal("unsafe frame admitted")
			}
		})
	}
	if _, err := db.Exec(`UPDATE agentd_credential_state SET connection_epoch=connection_epoch+1 WHERE tenant_id=$1 AND sandbox_binding_id=$2`, s.TenantID, s.SandboxID); err != nil {
		t.Fatal(err)
	}
	if p.AuthorizeFrame(ctx, i, c, f) == nil {
		t.Fatal("stale durable epoch accepted")
	}
	if _, err := db.Exec(`UPDATE executions SET state='CANCELLING',state_version=state_version+1 WHERE tenant_id=$1 AND execution_id=$2`, s.TenantID, s.ExecutionID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AuthorizeTransport(ctx, i, transport.AcceptStream); err == nil {
		t.Fatal("cancelled grant accepted")
	}
}

func TestAgentdPolicyUnknownBlocksNewDispatch(t *testing.T) {
	_, p, m, i, c := policyFixture(t)
	ctx := context.Background()
	f := policyCommand(t, m, c)
	if err := p.AuthorizeFrame(ctx, i, c, f); err != nil {
		t.Fatal(err)
	}
	failed := proto.Clone(f).(*agentdv1.Envelope)
	mid, _ := primitives.NewID(time.Now())
	failed.MessageId = string(mid)
	failed.Body = &agentdv1.Envelope_Failure{Failure: &agentdv1.Failure{Code: agentdv1.Failure_OUTCOME_UNKNOWN}}
	if err := p.AuthorizeFrame(ctx, i, c, failed); err != nil {
		t.Fatal(err)
	}
	s := i.Binding.Request.Scope
	outcome, err := p.CommandOutcome(ctx, s.TenantID, s.SandboxID, primitives.ID(f.OperationId), f.RequestDigest)
	if err != nil || outcome != "UNKNOWN" {
		t.Fatal(outcome, err)
	}
	another := policyCommand(t, m, c)
	another.Sequence = 2
	if p.AuthorizeFrame(ctx, i, c, another) == nil {
		t.Fatal("unknown outcome allowed another dispatch")
	}
	other, _ := primitives.NewID(time.Now())
	if _, err = p.CommandOutcome(ctx, other, s.SandboxID, primitives.ID(f.OperationId), f.RequestDigest); err == nil {
		t.Fatal("cross-tenant history leaked")
	}
}

func TestAgentdPolicyComposesCurrentAdmission(t *testing.T) {
	db, p, m, i, old := policyFixture(t)
	ctx := context.Background()
	s := i.Binding.Request.Scope
	bindings, _ := postgres.NewSandboxBindings(db)
	registry, _ := postgres.NewAgentdCredentials(db)
	provider := &admissionFixture{r: i.Binding.Request}
	admission, err := agentdadmission.New(bindings, provider, registry, p, p)
	if err != nil {
		t.Fatal(err)
	}
	peer := transport.Peer{Identity: transport.Identity{TenantID: s.TenantID, SandboxID: s.SandboxID, AttemptID: s.AttemptID}, CertificateDigest: testDigest('a')}
	if err = db.QueryRow(`SELECT expires_at FROM agentd_credentials WHERE tenant_id=$1 AND sandbox_binding_id=$2`, s.TenantID, s.SandboxID).Scan(&peer.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	lease, err := admission.Admit(ctx, peer, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if lease.Epoch <= old.Epoch {
		t.Fatal("reconnect did not replace epoch")
	}
	if p.AuthorizeFrame(ctx, i, old, policyCommand(t, m, old)) == nil {
		t.Fatal("previous connection retained authority")
	}
	c := transport.Connection{ID: lease.ConnectionID, Epoch: lease.Epoch, Deadline: lease.Deadline}
	f := policyCommand(t, m, c)
	if err = lease.Check(ctx, f); err != nil {
		t.Fatal("composed frame policy", err)
	}
	if err = lease.Check(ctx, f); err == nil {
		t.Fatal("composed duplicate dispatch")
	}
	hb := proto.Clone(f).(*agentdv1.Envelope)
	mid, _ := primitives.NewID(time.Now())
	hb.MessageId = string(mid)
	hb.OperationId = ""
	hb.RequestDigest = ""
	hb.HarnessHandle = ""
	hb.Body = &agentdv1.Envelope_Heartbeat{Heartbeat: &agentdv1.Heartbeat{ProcessState: agentdv1.Heartbeat_RUNNING, LastAcceptedSequence: 999}}
	if lease.Check(ctx, hb) == nil {
		t.Fatal("impossible acknowledgement progress accepted")
	}
	hb.GetHeartbeat().LastAcceptedSequence = 1
	if err = lease.Check(ctx, hb); err != nil {
		t.Fatal("candidate heartbeat rejected", err)
	}
	var state string
	if err = db.QueryRow(`SELECT state FROM executions WHERE tenant_id=$1 AND execution_id=$2`, s.TenantID, s.ExecutionID).Scan(&state); err != nil || state != "MATERIALIZING" {
		t.Fatal("sandbox report changed authoritative lifecycle")
	}
	if err = p.Revoke(ctx, s.TenantID, s.SandboxID); err != nil {
		t.Fatal(err)
	}
	if lease.Check(ctx, hb) == nil {
		t.Fatal("existing lease survived revocation")
	}
}
