// Package agentdadmission composes current compute, provider, authority and
// durable credential checks. No identity or expectations come from Hello.
package agentdadmission

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"google.golang.org/protobuf/proto"
)

var ErrAdmission = errors.New("agentd admission rejected")

type Service struct {
	compute  sandbox.ComputeStore
	provider sandbox.ComputeProvider
	registry transport.CredentialRegistry
	policy   transport.AdmissionPolicy
	frames   transport.FramePolicy
}

var _ transport.Authorizer = (*Service)(nil)
var _ transport.CredentialAuthority = (*Service)(nil)

func New(compute sandbox.ComputeStore, provider sandbox.ComputeProvider, registry transport.CredentialRegistry, policy transport.AdmissionPolicy, frames transport.FramePolicy) (*Service, error) {
	if compute == nil || provider == nil || registry == nil || policy == nil || frames == nil {
		return nil, ErrAdmission
	}
	return &Service{compute, provider, registry, policy, frames}, nil
}

func (s *Service) current(ctx context.Context, id transport.Identity, purpose transport.Purpose) (sandbox.ComputeIntent, transport.Authorization, error) {
	fail := func() (sandbox.ComputeIntent, transport.Authorization, error) {
		return sandbox.ComputeIntent{}, transport.Authorization{}, ErrAdmission
	}
	if ctx.Err() != nil {
		return fail()
	}
	i, err := s.compute.LoadCompute(ctx, id.TenantID, id.SandboxID)
	r := i.Binding.Request
	if err != nil || !i.Current || i.Desired != sandbox.ComputeRunning || r.Scope.TenantID != id.TenantID || r.Scope.SandboxID != id.SandboxID || r.Scope.AttemptID != id.AttemptID || !r.Deadline.After(time.Now()) {
		return fail()
	}
	a, err := s.policy.AuthorizeTransport(ctx, i, purpose)
	if err != nil || !a.Deadline.After(time.Now()) || a.Deadline.After(r.Deadline) || ctx.Err() != nil {
		return fail()
	}
	b := a.Expected.Binding
	if b == nil || b.TenantId != string(id.TenantID) || b.SandboxBindingId != string(id.SandboxID) || b.AttemptId != string(id.AttemptID) || b.SessionId != string(r.Scope.SessionID) || b.ExecutionId != string(r.Scope.ExecutionID) || b.SessionGeneration != r.Scope.Generation {
		return fail()
	}
	// Validate the trusted expectation tuple before consuming any bootstrap proof.
	e := a.Expected
	h := &agentdv1.Hello{Versions: &agentdv1.VersionRange{Major: protocol.Major, MaximumMinor: protocol.Minor}, Binding: e.Binding, Challenge: e.Challenge, BuildDigest: e.BuildDigest, AdapterKind: e.AdapterKind, AdapterDigest: e.AdapterDigest, SupportedCapabilities: e.SupportedCapabilities, RequiredCapabilities: e.RequiredCapabilities, Limits: e.Limits}
	if _, err = protocol.Negotiate(h, e, string(id.SandboxID), 1); err != nil {
		return fail()
	}
	if purpose != transport.IssueBootstrap {
		status, err := s.provider.Get(ctx, id.TenantID, id.SandboxID)
		if err != nil || i.Binding.ProviderReference == "" || status.Handle.SandboxID != id.SandboxID || status.Handle.ProviderReference != i.Binding.ProviderReference || (status.State != sandbox.Ready && status.State != sandbox.Active) || !status.Effective.Verified || status.Effective.Image != r.Runtime.Image || status.Effective.Architecture != r.Runtime.Architecture || status.Effective.IsolationClass != r.Profile.IsolationClass || status.Effective.NetworkClass != r.Profile.Network.Profile || status.Effective.AttachmentReference != r.Workspace.Reference || ctx.Err() != nil {
			return fail()
		}
	}
	// Own the tuple so callers cannot mutate policy-owned expectations after return.
	if !a.Deadline.After(time.Now()) {
		return fail()
	}
	a.Expected = cloneExpected(e)
	return i, a, nil
}

func (s *Service) AuthorizeCredential(ctx context.Context, r transport.CredentialRequest) (transport.CredentialGrant, error) {
	purpose := transport.IssueBootstrap
	if r.Epoch != 0 {
		purpose = transport.RenewIdentity
		if r.Peer.Identity != r.Identity {
			return transport.CredentialGrant{}, ErrAdmission
		}
		if s.registry.CheckConnection(ctx, r.Peer, transport.Connection{ID: r.ConnectionID, Epoch: r.Epoch}) != nil {
			return transport.CredentialGrant{}, ErrAdmission
		}
	}
	if purpose == transport.IssueBootstrap && (r.ConnectionID != "" || r.Peer != (transport.Peer{})) {
		return transport.CredentialGrant{}, ErrAdmission
	}
	i, a, err := s.current(ctx, r.Identity, purpose)
	if err != nil {
		return transport.CredentialGrant{}, ErrAdmission
	}
	if purpose == transport.IssueBootstrap && (!a.BootstrapDeadline.After(time.Now()) || a.BootstrapDeadline.After(a.Deadline)) {
		return transport.CredentialGrant{}, ErrAdmission
	}
	v, err := s.registry.Version(ctx, r.Identity)
	if err != nil || v == 0 {
		return transport.CredentialGrant{}, ErrAdmission
	}
	return transport.CredentialGrant{Identity: r.Identity, Version: v, AuthorityDeadline: a.Deadline, AttemptDeadline: i.Binding.Request.Deadline, BootstrapDeadline: a.BootstrapDeadline}, nil
}
func (s *Service) CommitCredential(ctx context.Context, r transport.CredentialRequest, g transport.CredentialGrant, c transport.CredentialRecord) error {
	fresh, err := s.AuthorizeCredential(ctx, r)
	if err != nil || g.Identity != r.Identity || c.Identity != r.Identity || g.Version != fresh.Version || c.Bootstrap != (r.Epoch == 0) || c.ExpiresAt.After(g.AuthorityDeadline) || c.ExpiresAt.After(g.AttemptDeadline) || c.Bootstrap && c.ExpiresAt.After(g.BootstrapDeadline) {
		return ErrAdmission
	}
	if s.registry.Register(ctx, r, fresh, c) != nil {
		return ErrAdmission
	}
	return nil
}

func (s *Service) Admit(ctx context.Context, p transport.Peer, proof []byte) (transport.Lease, error) {
	fail := transport.Lease{}
	if len(proof) != 32 || !p.ExpiresAt.After(time.Now()) {
		return fail, ErrAdmission
	}
	_, a, err := s.current(ctx, p.Identity, transport.AcceptStream)
	if err != nil {
		return fail, ErrAdmission
	}
	deadline := a.Deadline
	if p.ExpiresAt.Before(deadline) {
		deadline = p.ExpiresAt
	}
	c, err := s.registry.ConsumeBootstrap(ctx, p, proof, deadline)
	if err != nil {
		return fail, ErrAdmission
	}
	var once sync.Once
	close := func() {
		once.Do(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.registry.CloseConnection(cleanupCtx, p.Identity, c)
		})
	}
	// Recheck after consumption; failure never makes a consumed proof reusable.
	_, idErr := primitives.ParseID(string(c.ID))
	_, after, err := s.current(ctx, p.Identity, transport.AcceptStream)
	if err != nil || idErr != nil || c.Epoch == 0 || !c.Deadline.After(time.Now()) || c.Deadline.After(deadline) || after.Deadline.Before(c.Deadline) || !sameExpected(a.Expected, after.Expected) || s.registry.CheckConnection(ctx, p, c) != nil {
		close()
		return fail, ErrAdmission
	}
	return transport.Lease{Expected: cloneExpected(a.Expected), ConnectionID: c.ID, Epoch: c.Epoch, Deadline: c.Deadline, Close: close, Check: func(ctx context.Context, f *agentdv1.Envelope) error {
		if f == nil || !proto.Equal(f.Binding, a.Expected.Binding) || f.ConnectionId != string(c.ID) || f.ConnectionEpoch != c.Epoch || !protocol.KnownFields(f) {
			return ErrAdmission
		}
		i, current, err := s.current(ctx, p.Identity, transport.DeliverFrame)
		if err != nil || !sameExpected(a.Expected, current.Expected) || !current.Deadline.After(time.Now()) || f.DeadlineUnixMs > current.Deadline.UnixMilli() || s.registry.CheckConnection(ctx, p, c) != nil {
			return ErrAdmission
		}
		if s.frames.AuthorizeFrame(ctx, i, c, proto.Clone(f).(*agentdv1.Envelope)) != nil || ctx.Err() != nil {
			return ErrAdmission
		}
		return nil
	}}, nil
}
func sameExpected(a, b transport.Expectations) bool {
	return proto.Equal(a.Binding, b.Binding) && proto.Equal(a.Limits, b.Limits) && slices.Equal(a.Challenge, b.Challenge) && a.BuildDigest == b.BuildDigest && a.AdapterKind == b.AdapterKind && a.AdapterDigest == b.AdapterDigest && slices.Equal(a.SupportedCapabilities, b.SupportedCapabilities) && slices.Equal(a.RequiredCapabilities, b.RequiredCapabilities)
}

func cloneExpected(e transport.Expectations) transport.Expectations {
	e.Binding = proto.Clone(e.Binding).(*agentdv1.Binding)
	e.Limits = proto.Clone(e.Limits).(*agentdv1.Limits)
	e.Challenge = slices.Clone(e.Challenge)
	e.SupportedCapabilities = slices.Clone(e.SupportedCapabilities)
	e.RequiredCapabilities = slices.Clone(e.RequiredCapabilities)
	return e
}
