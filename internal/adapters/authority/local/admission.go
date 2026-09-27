package local

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/idempotency"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/authority"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func validID(v string) bool { _, e := primitives.ParseID(v); return e == nil }

// Admit records the grant and replay result in the same transaction. It does
// not advance Session state or start compute. Execution admission must still
// atomically compare Session version/generation and persist the returned grant.
func (a *Authority) Admit(ctx context.Context, c authority.Caller, r authority.Request) (grant authority.Grant, resultErr error) {
	return a.admit(ctx, c, r, func(f func(context.Context, persistence.Repositories) error) error {
		return a.store.WithinTransaction(ctx, c.TenantID, f)
	})
}

// AdmitInTransaction joins local issuance to Execution admission. The caller
// owns the tenant-scoped transaction; errors must roll back the whole callback.
// This is a local-only seam, not a protocol for remote authority calls.
func (a *Authority) AdmitInTransaction(ctx context.Context, repos persistence.Repositories, c authority.Caller, r authority.Request) (authority.Grant, error) {
	return a.admit(ctx, c, r, func(f func(context.Context, persistence.Repositories) error) error { return f(ctx, repos) })
}

func (a *Authority) admit(ctx context.Context, c authority.Caller, r authority.Request, transact func(func(context.Context, persistence.Repositories) error) error) (grant authority.Grant, resultErr error) {
	ctx, finish := a.observe(ctx, "admit")
	defer func() { finish(resultErr, "") }()
	var g authority.Grant
	if !validID(string(c.TenantID)) || !digestPattern.MatchString(c.PrincipalDigest) || !validID(string(r.SessionID)) || !digestPattern.MatchString(r.KeyDigest) || !digestPattern.MatchString(r.RequestDigest) || r.Generation == 0 || r.Generation > math.MaxInt64 || r.SessionVersion > math.MaxInt64 || !subset(r.Capabilities, []string{"shell", "process", "fork"}) || len(r.Profile) > 128 || len(r.Network) > 128 || len(r.Architecture) > 16 || (len(a.config.Tenants) > 0 && !slices.Contains(a.config.Tenants, string(c.TenantID))) || (len(a.config.Principals) > 0 && !slices.Contains(a.config.Principals, c.PrincipalDigest)) {
		return g, authority.ErrDenied
	}
	c.Deadline = c.Deadline.UTC()
	now := a.clock.Now().UTC()
	if now.IsZero() {
		return g, authority.ErrUnavailable
	}
	// Bind all policy-relevant request fields ourselves, in addition to the
	// caller's canonical operation digest, so a lying digest cannot widen replay.
	raw, err := json.Marshal(struct {
		Caller  authority.Caller
		Request authority.Request
	}{c, r})
	if err != nil {
		return g, authority.ErrDenied
	}
	digest := sandbox.Digest(raw)
	scope := idempotency.Scope{PrincipalDigest: c.PrincipalDigest, Action: "authority.local.admit.v1", KeyDigest: r.KeyDigest}
	id, err := primitives.NewID(now)
	if err != nil {
		return g, authority.ErrUnavailable
	}
	candidate, err := idempotency.New(c.TenantID, id, scope, "local-admission-v1", digest, id, r.SessionID, id, id, now.Add(time.Minute), now.Add(a.config.MaximumDuration+24*time.Hour), now)
	if err != nil {
		return g, authority.ErrUnavailable
	}
	err = transact(func(ctx context.Context, repos persistence.Repositories) error {
		record, created, err := repos.Idempotency().Reserve(ctx, candidate)
		if err != nil {
			return err
		}
		if record.RequestDigest() != digest {
			return authority.ErrConflict
		}
		if !created {
			response, found := record.Response()
			if record.State() != idempotency.Succeeded || !found || json.Unmarshal(response.Payload, &g) != nil {
				return authority.ErrUnavailable
			}
			// Replay returns the original snapshot even after expiry/config changes.
			// It is never a validation or a renewal of that snapshot.
			return nil
		}
		s, err := repos.Sessions().Get(ctx, r.SessionID)
		if errors.Is(err, persistence.ErrNotFound) {
			return authority.ErrDenied
		}
		if err != nil {
			return err
		}
		g, err = a.evaluate(c, r, s, id, now)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(g)
		if err != nil || len(payload) > 65536 {
			return authority.ErrDenied
		}
		if err = repos.LocalGrants().Add(ctx, persistence.LocalGrantRecord{ID: g.ID, SessionID: g.SessionID, Snapshot: payload, Digest: sandbox.Digest(payload)}); err != nil {
			return err
		}
		if err = record.Succeed(idempotency.Response{HTTPStatus: 201, Payload: payload, Reference: string(g.ID)}, id, record.OwnerFence(), now); err != nil {
			return err
		}
		return repos.Idempotency().Update(ctx, record, record.OwnerFence())
	})
	if err != nil {
		if errors.Is(err, authority.ErrDenied) {
			return authority.Grant{}, authority.ErrDenied
		}
		if errors.Is(err, authority.ErrConflict) || errors.Is(err, persistence.ErrRequestDigestMismatch) || errors.Is(err, persistence.ErrConflict) {
			return authority.Grant{}, authority.ErrConflict
		}
		return authority.Grant{}, authority.ErrUnavailable
	}
	return g, nil
}

func (a *Authority) evaluate(c authority.Caller, r authority.Request, s *session.Session, id primitives.ID, now time.Time) (authority.Grant, error) {
	deny := authority.Grant{}
	if s == nil || s.TenantID() != c.TenantID || s.ID() != r.SessionID || s.StateVersion() != r.SessionVersion || s.ExecutionGeneration() >= math.MaxInt64 || r.Generation != s.ExecutionGeneration()+1 || (s.State() != session.Ready && s.State() != session.Idle) {
		return deny, authority.ErrDenied
	}
	b := s.Binding()
	var approved *ApprovedRuntime
	for _, v := range a.config.Runtimes {
		if sameBinding(v.Binding, b) {
			copy := v
			approved = &copy
			break
		}
	}
	if approved == nil {
		return deny, authority.ErrDenied
	}
	name := r.Profile
	if name == "" {
		name = a.config.DefaultProfile
	}
	entry, ok := a.profiles[name]
	if !ok || !slices.Contains(approved.Profiles, name) {
		return deny, authority.ErrDenied
	}
	// Clone before narrowing: no request may mutate policy or another grant.
	raw, _ := json.Marshal(entry.Profile)
	var p runtimeprofile.Profile
	_ = json.Unmarshal(raw, &p)
	if !narrowLimit(&p.Resources.CPU, r.CPU) || !narrowLimit(&p.Resources.Memory, r.Memory) || !narrowLimit(&p.Resources.EphemeralStorage, r.EphemeralStorage) || !narrowValue(&p.Storage.WorkspaceBytes, r.WorkspaceBytes) || !narrowValue(&p.Resources.MaxProcesses, r.MaxProcesses) || (r.Network != "" && r.Network != p.Network.Profile) || !subset(r.Capabilities, approved.Capabilities) {
		return deny, authority.ErrDenied
	}
	if r.Architecture != "" {
		if !slices.Contains(p.Platform.Architectures, r.Architecture) {
			return deny, authority.ErrDenied
		}
		p.Platform.Architectures = []string{r.Architecture}
	}
	duration := r.Duration
	if duration == 0 {
		duration = a.config.DefaultDuration
	}
	if duration <= 0 || duration > a.config.MaximumDuration {
		return deny, authority.ErrDenied
	}
	expires := now.Add(duration)
	if !c.Deadline.IsZero() && c.Deadline.Before(expires) {
		expires = c.Deadline.UTC()
	}
	if !expires.After(now) {
		return deny, authority.ErrDenied
	}
	// Preserve registry evidence separately; effective constraints are in Profile.
	return authority.Grant{ID: id, Mode: "local", Issuer: "thinkpixelar/local", PolicyDigest: a.revision, Reason: "LOCAL_POLICY_ADMITTED", TenantID: c.TenantID, PrincipalDigest: c.PrincipalDigest, SessionID: s.ID(), SessionVersion: s.StateVersion(), Generation: r.Generation, RequestDigest: r.RequestDigest, Runtime: b, Profile: p, ProfileDigest: entry.Digest, ImplementationDigest: entry.ImplementationDigest, Implementation: append([]byte(nil), entry.Implementation...), Capabilities: slices.Clone(r.Capabilities), IssuedAt: now, ExpiresAt: expires}, nil
}
func narrowLimit(base *runtimeprofile.RequestLimit, requested *runtimeprofile.RequestLimit) bool {
	if requested == nil {
		return true
	}
	if requested.Request <= 0 || requested.Limit <= 0 || requested.Request > requested.Limit || requested.Request > base.Request || requested.Limit > base.Limit {
		return false
	}
	*base = *requested
	return true
}
func narrowValue(base *int64, requested *int64) bool {
	if requested == nil {
		return true
	}
	if *requested <= 0 || *requested > *base {
		return false
	}
	*base = *requested
	return true
}
func sameBinding(a, b session.RuntimeBinding) bool {
	if !canonicalDigest(b.RuntimeSpec, b.RuntimeSpecDigest) || !canonicalDigest(b.RuntimeProfileSnapshot, b.RuntimeProfileDigest) {
		return false
	}
	a.RuntimeSpec = nil
	b.RuntimeSpec = nil
	a.RuntimeProfileSnapshot = nil
	b.RuntimeProfileSnapshot = nil
	return reflect.DeepEqual(a, b)
}
