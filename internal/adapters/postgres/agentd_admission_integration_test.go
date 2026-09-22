package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentdadmission"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"google.golang.org/protobuf/proto"
)

// This fixture substitutes external authority/provider observations only; the
// binding, aggregate fence, registration, consumption and epoch use real SQL.
type admissionFixture struct {
	r      sandbox.AcquireRequest
	denied bool
	frames int
}

func (f *admissionFixture) AuthorizeTransport(_ context.Context, i sandbox.ComputeIntent, _ transport.Purpose) (transport.Authorization, error) {
	if f.denied || i.Binding.Request.Scope != f.r.Scope {
		return transport.Authorization{}, transport.ErrCredentialState
	}
	s := f.r.Scope
	return transport.Authorization{Deadline: f.r.Deadline, BootstrapDeadline: f.r.Deadline, Expected: transport.Expectations{Binding: &agentdv1.Binding{TenantId: string(s.TenantID), SessionId: string(s.SessionID), ExecutionId: string(s.ExecutionID), AttemptId: string(s.AttemptID), SandboxBindingId: string(s.SandboxID), SessionGeneration: s.Generation}, Challenge: make([]byte, 32), BuildDigest: testDigest('a'), AdapterKind: "test", AdapterDigest: testDigest('b'), SupportedCapabilities: []string{"test.status"}, Limits: protocol.HardLimits()}}, nil
}
func (f *admissionFixture) AuthorizeFrame(context.Context, sandbox.ComputeIntent, transport.Connection, *agentdv1.Envelope) error {
	f.frames++
	return nil
}
func (f *admissionFixture) Acquire(context.Context, sandbox.AcquireRequest) (sandbox.Handle, error) {
	panic("unexpected acquisition")
}
func (f *admissionFixture) Release(context.Context, primitives.ID, primitives.ID, sandbox.Operation) error {
	panic("unexpected release")
}
func (f *admissionFixture) Get(context.Context, primitives.ID, primitives.ID) (sandbox.Status, error) {
	r := f.r
	return sandbox.Status{Handle: sandbox.Handle{SandboxID: r.Scope.SandboxID, ProviderReference: "admission-fixture"}, State: sandbox.Ready, Effective: sandbox.EffectiveFacts{Verified: true, Image: r.Runtime.Image, Architecture: r.Runtime.Architecture, IsolationClass: r.Profile.IsolationClass, NetworkClass: r.Profile.Network.Profile, AttachmentReference: r.Workspace.Reference}}, nil
}

func TestAgentdAdmissionWithDurableRegistry(t *testing.T) {
	db, r := sandboxDatabaseFixture(t)
	ctx := context.Background()
	bindings, _ := postgres.NewSandboxBindings(db)
	if _, err := bindings.Reserve(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := bindings.BindReference(ctx, r.Scope.TenantID, r.Scope.SandboxID, "admission-fixture"); err != nil {
		t.Fatal(err)
	}
	registry, _ := postgres.NewAgentdCredentials(db)
	f := &admissionFixture{r: r}
	s, err := agentdadmission.New(bindings, f, registry, f, f)
	if err != nil {
		t.Fatal(err)
	}
	id := transport.Identity{TenantID: r.Scope.TenantID, SandboxID: r.Scope.SandboxID, AttemptID: r.Scope.AttemptID}
	request := transport.CredentialRequest{Identity: id}
	grant, err := s.AuthorizeCredential(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	proof := make([]byte, 32)
	proof[0] = 1
	hash := sha256.Sum256(proof)
	now := time.Now().UTC().Truncate(time.Second)
	cid, _ := primitives.NewID(now)
	record := transport.CredentialRecord{Identity: id, CredentialID: cid, CertificateDigest: testDigest('c'), IssuerDigest: testDigest('d'), ProofDigest: "sha256:" + hex.EncodeToString(hash[:]), Bootstrap: true, NotBefore: now, ExpiresAt: now.Add(5 * time.Minute)}
	if err := s.CommitCredential(ctx, request, grant, record); err != nil {
		t.Fatal(err)
	}
	peer := transport.Peer{Identity: id, CertificateDigest: record.CertificateDigest, ExpiresAt: record.ExpiresAt}
	for _, which := range []string{"tenant", "sandbox", "attempt", "certificate", "proof", "expired"} {
		t.Run(which, func(t *testing.T) {
			other := peer
			badProof := append([]byte(nil), proof...)
			switch which {
			case "tenant":
				other.Identity.TenantID = r.Scope.SessionID
			case "sandbox":
				other.Identity.SandboxID = r.Scope.SessionID
			case "attempt":
				other.Identity.AttemptID = r.Scope.SessionID
			case "certificate":
				other.CertificateDigest = testDigest('e')
			case "proof":
				badProof[0]++
			case "expired":
				other.ExpiresAt = time.Now().Add(-time.Second)
			}
			if _, err := s.Admit(ctx, other, badProof); err != agentdadmission.ErrAdmission {
				t.Fatal("invalid bootstrap admitted")
			}
		})
	}
	// Rejection must not consume the valid sandbox's proof.
	lease, err := s.Admit(ctx, peer, proof)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	frame := &agentdv1.Envelope{Binding: lease.Expected.Binding, ConnectionId: string(lease.ConnectionID), ConnectionEpoch: lease.Epoch, Sequence: 1}
	if err := lease.Check(ctx, frame); err != nil || f.frames != 1 {
		t.Fatal("current frame rejected")
	}
	for _, which := range []string{"sandbox", "attempt", "epoch"} {
		t.Run("frame-"+which, func(t *testing.T) {
			bad := proto.Clone(frame).(*agentdv1.Envelope)
			switch which {
			case "sandbox":
				bad.Binding.SandboxBindingId = string(r.Scope.SessionID)
			case "attempt":
				bad.Binding.AttemptId = string(r.Scope.SessionID)
			case "epoch":
				bad.ConnectionEpoch++
			}
			if err := lease.Check(ctx, bad); err != agentdadmission.ErrAdmission || f.frames != 1 {
				t.Fatal("cross-binding or stale frame reached delivery")
			}
		})
	}
	f.denied = true
	if err := lease.Check(ctx, frame); err != agentdadmission.ErrAdmission || f.frames != 1 {
		t.Fatal("revoked frame reached delivery")
	}
	f.denied = false
	if _, err := s.Admit(ctx, peer, proof); err != agentdadmission.ErrAdmission {
		t.Fatal("consumed bootstrap admitted again")
	}
}
