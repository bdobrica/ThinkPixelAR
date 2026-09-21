package agentdadmission

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"google.golang.org/protobuf/proto"
)

type fixture struct {
	intent                                        sandbox.ComputeIntent
	auth                                          transport.Authorization
	status                                        sandbox.Status
	peer                                          transport.Peer
	connection                                    transport.Connection
	policyErr, frameErr, registryErr              error
	consumed, closed, checked, registered, frames int
	version                                       uint64
	onConsume                                     func()
}

func (f *fixture) LoadCompute(context.Context, primitives.ID, primitives.ID) (sandbox.ComputeIntent, error) {
	return f.intent, nil
}
func (f *fixture) RecordCompute(context.Context, sandbox.ComputeIntent, sandbox.ComputeObservation) error {
	return nil
}
func (f *fixture) Acquire(context.Context, sandbox.AcquireRequest) (sandbox.Handle, error) {
	panic("admission must not acquire")
}
func (f *fixture) Release(context.Context, primitives.ID, primitives.ID, sandbox.Operation) error {
	panic("admission must not release")
}
func (f *fixture) Get(context.Context, primitives.ID, primitives.ID) (sandbox.Status, error) {
	return f.status, nil
}
func (f *fixture) AuthorizeTransport(context.Context, sandbox.ComputeIntent, transport.Purpose) (transport.Authorization, error) {
	return f.auth, f.policyErr
}
func (f *fixture) AuthorizeFrame(_ context.Context, _ sandbox.ComputeIntent, _ transport.Connection, e *agentdv1.Envelope) error {
	f.frames++
	e.Sequence = 99
	return f.frameErr
}
func (f *fixture) Version(context.Context, transport.Identity) (uint64, error) {
	return f.version, f.registryErr
}
func (f *fixture) Register(_ context.Context, _ transport.CredentialRequest, g transport.CredentialGrant, c transport.CredentialRecord) error {
	f.registered++
	if c.ExpiresAt.After(g.AuthorityDeadline) {
		return transport.ErrCredentialState
	}
	return f.registryErr
}
func (f *fixture) ConsumeBootstrap(_ context.Context, p transport.Peer, _ []byte, d time.Time) (transport.Connection, error) {
	f.consumed++
	if f.onConsume != nil {
		f.onConsume()
	}
	if p != f.peer {
		return transport.Connection{}, transport.ErrCredentialState
	}
	f.connection.Deadline = d
	return f.connection, f.registryErr
}
func (f *fixture) CheckConnection(context.Context, transport.Peer, transport.Connection) error {
	f.checked++
	return f.registryErr
}
func (f *fixture) CloseConnection(context.Context, transport.Identity, transport.Connection) error {
	f.closed++
	return nil
}
func newFixture(t *testing.T) (*Service, *fixture) {
	t.Helper()
	id := func() primitives.ID {
		v, err := primitives.NewID(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	scope := sandbox.Scope{TenantID: id(), SessionID: id(), ExecutionID: id(), AttemptID: id(), SandboxID: id(), Generation: 3, AttemptOrdinal: 1}
	b := &agentdv1.Binding{TenantId: string(scope.TenantID), SessionId: string(scope.SessionID), ExecutionId: string(scope.ExecutionID), AttemptId: string(scope.AttemptID), SandboxBindingId: string(scope.SandboxID), SessionGeneration: scope.Generation}
	deadline := time.Now().Add(time.Hour)
	digest := "sha256:" + strings.Repeat("a", 64)
	f := &fixture{intent: sandbox.ComputeIntent{Current: true, Desired: sandbox.ComputeRunning, Binding: sandbox.Binding{Request: sandbox.AcquireRequest{Scope: scope, Deadline: deadline}, ProviderReference: "provider-one"}}, auth: transport.Authorization{Deadline: deadline, BootstrapDeadline: time.Now().Add(10 * time.Minute), Expected: transport.Expectations{Binding: b, Challenge: make([]byte, 32), BuildDigest: digest, AdapterKind: "test", AdapterDigest: digest, SupportedCapabilities: []string{"test.status"}, Limits: protocol.HardLimits()}}, version: 1}
	f.peer = transport.Peer{Identity: transport.Identity{TenantID: scope.TenantID, SandboxID: scope.SandboxID, AttemptID: scope.AttemptID}, CertificateDigest: digest, ExpiresAt: time.Now().Add(5 * time.Minute)}
	f.status = sandbox.Status{Handle: sandbox.Handle{SandboxID: scope.SandboxID, ProviderReference: "provider-one"}, State: sandbox.Ready, Effective: sandbox.EffectiveFacts{Verified: true}}
	f.connection = transport.Connection{ID: id(), Epoch: 2}
	s, err := New(f, f, f, f, f)
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}
func frame(f *fixture) *agentdv1.Envelope {
	return &agentdv1.Envelope{Binding: proto.Clone(f.auth.Expected.Binding).(*agentdv1.Binding), ConnectionId: string(f.connection.ID), ConnectionEpoch: f.connection.Epoch, Sequence: 1}
}

func TestAdmissionRevalidatesBeforeDelivery(t *testing.T) {
	s, f := newFixture(t)
	lease, err := s.Admit(context.Background(), f.peer, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if f.consumed != 1 || lease.Deadline.After(f.peer.ExpiresAt) {
		t.Fatal("admission bounds")
	}
	e := frame(f)
	if err := lease.Check(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if e.Sequence != 1 || f.frames != 1 {
		t.Fatal("frame copy/check")
	}
	lease.Expected.Binding.AttemptId = "mutated" // Public lease cannot mutate closure.
	if err := lease.Check(context.Background(), e); err != nil {
		t.Fatal("lease mutation altered check")
	}
	f.policyErr = errors.New("restricted detail")
	if err := lease.Check(context.Background(), e); err != ErrAdmission || f.frames != 2 {
		t.Fatal("revoked frame reached handler")
	}
	lease.Close()
	lease.Close()
	if f.closed != 1 {
		t.Fatal("lease not released")
	}
}
func TestAdmissionRejectsBeforeConsumption(t *testing.T) {
	for _, which := range []string{"tenant", "sandbox", "attempt", "session", "execution", "generation", "stale", "released", "expired", "authority", "widened", "provider", "not-ready", "unverified", "image", "network", "invalid-expectations"} {
		t.Run(which, func(t *testing.T) {
			s, f := newFixture(t)
			p := f.peer
			switch which {
			case "tenant":
				p.Identity.TenantID = f.intent.Binding.Request.Scope.SessionID
			case "sandbox":
				p.Identity.SandboxID = f.intent.Binding.Request.Scope.SessionID
			case "attempt":
				p.Identity.AttemptID = f.intent.Binding.Request.Scope.SessionID
			case "session":
				f.auth.Expected.Binding.SessionId = f.auth.Expected.Binding.AttemptId
			case "execution":
				f.auth.Expected.Binding.ExecutionId = f.auth.Expected.Binding.AttemptId
			case "generation":
				f.auth.Expected.Binding.SessionGeneration++
			case "stale":
				f.intent.Current = false
			case "released":
				f.intent.Desired = sandbox.ComputeReleased
			case "expired":
				f.auth.Deadline = time.Now()
			case "authority":
				f.policyErr = errors.New("restricted detail")
			case "widened":
				f.auth.Deadline = f.auth.Deadline.Add(time.Hour)
			case "provider":
				f.status.Handle.ProviderReference = "other"
			case "not-ready":
				f.status.State = sandbox.Provisioning
			case "unverified":
				f.status.Effective.Verified = false
			case "image":
				f.status.Effective.Image = "other"
			case "network":
				f.status.Effective.NetworkClass = "other"
			case "invalid-expectations":
				f.auth.Expected.Challenge = nil
			}
			if _, err := s.Admit(context.Background(), p, make([]byte, 32)); err != ErrAdmission || f.consumed != 0 {
				t.Fatal("rejected admission consumed bootstrap")
			}
		})
	}
}
func TestChangeDuringConsumptionClosesEpoch(t *testing.T) {
	for _, which := range []string{"authority", "fence", "expectations", "deadline"} {
		t.Run(which, func(t *testing.T) {
			s, f := newFixture(t)
			f.onConsume = func() {
				switch which {
				case "authority":
					f.policyErr = ErrAdmission
				case "fence":
					f.intent.Current = false
				case "expectations":
					f.auth.Expected.BuildDigest = "sha256:" + strings.Repeat("b", 64)
				case "deadline":
					f.auth.Deadline = time.Now().Add(time.Second)
				}
			}
			if _, err := s.Admit(context.Background(), f.peer, make([]byte, 32)); err != ErrAdmission || f.consumed != 1 || f.closed != 1 {
				t.Fatal("post-consumption failure left epoch admitted")
			}
		})
	}
}
func TestFramesRecheckBindingAndPolicy(t *testing.T) {
	for _, which := range []string{"binding", "connection", "epoch", "registry", "provider", "semantics", "fence", "deadline"} {
		t.Run(which, func(t *testing.T) {
			s, f := newFixture(t)
			lease, err := s.Admit(context.Background(), f.peer, make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			e := frame(f)
			switch which {
			case "binding":
				e.Binding.SessionGeneration++
			case "connection":
				e.ConnectionId = e.Binding.AttemptId
			case "epoch":
				e.ConnectionEpoch++
			case "registry":
				f.registryErr = transport.ErrCredentialState
			case "provider":
				f.status.Effective.Verified = false
			case "deadline":
				e.DeadlineUnixMs = f.auth.Deadline.Add(time.Second).UnixMilli()
			case "semantics":
				f.frameErr = ErrAdmission
			case "fence":
				f.intent.Current = false
			}
			if err := lease.Check(context.Background(), e); err != ErrAdmission {
				t.Fatal("invalid frame accepted")
			}
			if which != "semantics" && f.frames != 0 {
				t.Fatal("invalid frame reached semantic handler")
			}
		})
	}
}
func TestCredentialAuthorizationRechecksCommit(t *testing.T) {
	s, f := newFixture(t)
	ctx := context.Background()
	r := transport.CredentialRequest{Identity: f.peer.Identity}
	// Bootstrap precedes compute creation; effective provider checks are deferred
	// to stream admission, but persisted binding and authority remain mandatory.
	f.status.State = sandbox.Provisioning
	f.intent.Binding.ProviderReference = ""
	g, err := s.AuthorizeCredential(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	c := transport.CredentialRecord{Identity: r.Identity, Bootstrap: true, ExpiresAt: time.Now().Add(time.Minute)}
	f.policyErr = ErrAdmission
	if err := s.CommitCredential(ctx, r, g, c); err != ErrAdmission || f.registered != 0 {
		t.Fatal("revoked signing committed")
	}
	f.policyErr = nil
	f.version++
	if err := s.CommitCredential(ctx, r, g, c); err != ErrAdmission || f.registered != 0 {
		t.Fatal("stale grant committed")
	}
	f.version = g.Version
	f.auth.Deadline = time.Now().Add(30 * time.Second)
	f.auth.BootstrapDeadline = f.auth.Deadline
	if err := s.CommitCredential(ctx, r, g, c); err != ErrAdmission {
		t.Fatal("narrowed grant widened")
	}
}
func TestMandatoryDependencies(t *testing.T) {
	_, f := newFixture(t)
	for _, args := range [][5]bool{{true, false, false, false, false}, {false, true, false, false, false}, {false, false, true, false, false}, {false, false, false, true, false}, {false, false, false, false, true}} {
		var c sandbox.ComputeStore = f
		var p sandbox.ComputeProvider = f
		var r transport.CredentialRegistry = f
		var a transport.AdmissionPolicy = f
		var frames transport.FramePolicy = f
		if args[0] {
			c = nil
		}
		if args[1] {
			p = nil
		}
		if args[2] {
			r = nil
		}
		if args[3] {
			a = nil
		}
		if args[4] {
			frames = nil
		}
		if _, err := New(c, p, r, a, frames); err != ErrAdmission {
			t.Fatal("missing mandatory guard accepted")
		}
	}
}
