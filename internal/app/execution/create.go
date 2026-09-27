package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"regexp"
	"time"
	"unicode/utf8"

	sessions "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/idempotency"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/outbox"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/authority"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	canonical "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

var (
	ErrInvalid     = errors.New("invalid Execution request")
	ErrNotFound    = errors.New("Session unavailable")
	ErrConflict    = errors.New("Execution admission conflict")
	ErrUnsupported = errors.New("unsupported authority reference")
	ErrDenied      = errors.New("Execution admission denied")
	ErrUnavailable = errors.New("Execution creation unavailable")
	keyPattern     = regexp.MustCompile(`^[A-Za-z0-9._~-]{16,128}$`)
	digestPattern  = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

const CreateAction = "executions.create.v1"

type Caller = sessions.Caller

// LocalAdmission is an in-process local transaction participant. Remote AG
// admission needs its own claim/reconciliation composition; it cannot use this seam.
type LocalAdmission interface {
	AdmitInTransaction(context.Context, persistence.Repositories, authority.Caller, authority.Request) (authority.Grant, error)
}
type CreateRequest struct {
	Input        string `json:"input"`
	RunReference string `json:"run_reference,omitempty"`
}

// Access must check current tenant and Session execution/disclosure policy on
// every request, including replay. Denial is deliberately enumeration-safe.
type Access func(context.Context, Caller, primitives.ID, CreateRequest) error
type View struct {
	ID              primitives.ID `json:"id"`
	SessionID       primitives.ID `json:"session_id"`
	State           domain.State  `json:"state"`
	StateVersion    uint64        `json:"state_version"`
	Generation      uint64        `json:"generation"`
	AuthorityMode   string        `json:"authority_mode"`
	AuthorityIssuer string        `json:"authority_issuer"`
	CreatedAt       time.Time     `json:"created_at"`
}
type Result struct {
	View     View
	Replayed bool
}

// QueueIntent contains metadata only. Consumers read Confidential input through
// the tenant-scoped Execution repository and verify InputDigest. They must
// revalidate authority and current fences before materializing or starting work.
type QueueIntent struct {
	ExecutionID primitives.ID `json:"execution_id"`
	SessionID   primitives.ID `json:"session_id"`
	Generation  uint64        `json:"generation"`
	InputDigest string        `json:"input_digest"`
	GrantID     primitives.ID `json:"grant_id"`
	GrantDigest string        `json:"grant_digest"`
}
type Creator struct {
	store     persistence.TransactionManager
	admission LocalAdmission
	clock     clock.Clock
	access    Access
}

func NewLocalCreator(store persistence.TransactionManager, admission LocalAdmission, clk clock.Clock, access Access) (*Creator, error) {
	if store == nil || admission == nil || clk == nil || access == nil {
		return nil, ErrUnavailable
	}
	return &Creator{store, admission, clk, access}, nil
}
func (c *Creator) Create(ctx context.Context, caller Caller, sid primitives.ID, version uint64, key string, r CreateRequest) (Result, error) {
	var result Result
	if _, err := primitives.ParseID(string(caller.TenantID)); err != nil || !digestPattern.MatchString(caller.PrincipalDigest) {
		return result, ErrDenied
	}
	if _, err := primitives.ParseID(string(sid)); err != nil {
		return result, ErrInvalid
	}
	if !keyPattern.MatchString(key) || version == 0 || version > math.MaxInt64 || len(r.Input) == 0 || len(r.Input) > 262144 || !utf8.ValidString(r.Input) {
		return result, ErrInvalid
	}
	if r.RunReference != "" {
		return result, ErrUnsupported
	}
	raw, _ := json.Marshal(struct {
		SessionID primitives.ID
		Version   uint64
		Request   CreateRequest
	}{sid, version, r})
	digest := sandbox.Digest(raw)
	scope := idempotency.Scope{PrincipalDigest: caller.PrincipalDigest, Action: CreateAction, KeyDigest: sandbox.Digest([]byte(string(sid) + "/" + key))}
	now := c.clock.Now().UTC()
	ids := make([]primitives.ID, 5)
	for i := range ids {
		var err error
		ids[i], err = primitives.NewID(now)
		if err != nil {
			return result, ErrUnavailable
		}
	}
	operation, eid, eventID, stateEventID, messageID := ids[0], ids[1], ids[2], ids[3], ids[4]
	record, err := idempotency.New(caller.TenantID, operation, scope, "execution-create-v1", digest, operation, eid, operation, operation, now.Add(time.Minute), now.Add(365*24*time.Hour), now)
	if err != nil {
		return result, ErrUnavailable
	}
	err = c.store.WithinTransaction(ctx, caller.TenantID, func(ctx context.Context, repos persistence.Repositories) error {
		if c.access(ctx, caller, sid, r) != nil {
			return ErrNotFound
		}
		record, created, err := repos.Idempotency().Reserve(ctx, record)
		if err != nil {
			return err
		}
		if record.RequestDigest() != digest {
			return ErrConflict
		}
		if !created {
			response, ok := record.Response()
			if !ok || record.State() != idempotency.Succeeded || record.NormalizationVersion() != "execution-create-v1" || json.Unmarshal(response.Payload, &result.View) != nil {
				return ErrUnavailable
			}
			if result.View.SessionID != sid {
				return ErrUnavailable
			}
			result.Replayed = true
			return nil
		}
		s, err := repos.Sessions().GetForUpdate(ctx, sid)
		if err != nil {
			return err
		}
		if s.StateVersion() != version || (s.State() != session.Ready && s.State() != session.Idle) || s.ExecutionGeneration() >= math.MaxInt64 {
			return ErrConflict
		}
		b := s.Binding()
		if b.AuthorityMode != "LOCAL" || b.AuthorityNamespace != authority.LocalIssuer {
			return ErrUnsupported
		}
		// Issuance and its replay record commit with the Execution, never separately.
		g, err := c.admission.AdmitInTransaction(ctx, repos, authority.Caller{TenantID: caller.TenantID, PrincipalDigest: caller.PrincipalDigest}, authority.Request{SessionID: sid, SessionVersion: version, Generation: s.ExecutionGeneration() + 1, KeyDigest: scope.KeyDigest, RequestDigest: digest})
		if err != nil {
			return err
		}
		stored, err := repos.LocalGrants().Get(ctx, g.ID)
		if err != nil {
			return err
		}
		grantRaw, err := json.Marshal(g)
		if err != nil {
			return err
		}
		now = c.clock.Now().UTC() // after issuance/locking; expiry is inclusive
		if g.Mode != authority.LocalMode || g.Issuer != authority.LocalIssuer || g.TenantID != caller.TenantID || g.PrincipalDigest != caller.PrincipalDigest || g.SessionID != sid || g.SessionVersion != version || g.Generation != s.ExecutionGeneration()+1 || g.RequestDigest != digest || !g.ExpiresAt.After(now) || g.IssuedAt.After(now) || stored.State != "ACTIVE" || stored.SessionID != sid || !bytes.Equal(stored.Snapshot, grantRaw) || stored.Digest != sandbox.Digest(grantRaw) || !sameRuntime(g.Runtime, b) {
			return ErrDenied
		}
		if err = s.Transition(session.Active, version, now); err != nil {
			return err
		}
		// CAS also checks READY/IDLE, the previous generation, and no current writer.
		if err = repos.Sessions().Activate(ctx, s, version, eid); err != nil {
			return err
		}
		evidence, err := json.Marshal(b)
		if err != nil {
			return err
		}
		e, err := domain.New(caller.TenantID, eid, domain.Binding{SessionID: sid, SessionGeneration: s.ExecutionGeneration(), AuthorityMode: b.AuthorityMode, AuthorityNamespace: g.Issuer, AuthorityReference: string(g.ID), GrantDigest: stored.Digest, AgentID: b.AgentID, AgentVersionID: b.AgentVersionID, AgentEvidence: evidence, AgentEvidenceDigest: sandbox.Digest(evidence)}, g.ExpiresAt, now)
		if err != nil {
			return err
		}
		if err = repos.Executions().Add(ctx, e); err != nil {
			return err
		}
		inputDigest := sandbox.Digest([]byte(r.Input))
		if err = repos.Executions().AddInput(ctx, eid, r.Input, inputDigest); err != nil {
			return err
		}
		result.View = View{eid, sid, e.State(), e.StateVersion(), s.ExecutionGeneration(), g.Mode, g.Issuer, now}
		sequence, err := repos.RuntimeEvents().NextSequence(ctx, sid)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(struct {
			State      session.State `json:"state"`
			Generation uint64        `json:"execution_generation"`
		}{s.State(), s.ExecutionGeneration()})
		event, err := runtimeevent.New(stateEventID, caller.TenantID, sid, "", "", sequence, s.StateVersion(), "session.state_changed", now, now, runtimeevent.SourceAgentRuntime, runtimeevent.Internal, payload, runtimeevent.Correlation{RequestID: operation}, "session-lifetime", nil)
		if err != nil {
			return err
		}
		if err = repos.RuntimeEvents().Append(ctx, event); err != nil {
			return err
		}
		payload, _ = json.Marshal(result.View)
		event, err = runtimeevent.New(eventID, caller.TenantID, sid, eid, "", sequence+1, 0, "execution.accepted", now, now, runtimeevent.SourceAgentRuntime, runtimeevent.Internal, payload, runtimeevent.Correlation{RequestID: operation}, "session-lifetime", nil)
		if err != nil {
			return err
		}
		if err = repos.RuntimeEvents().Append(ctx, event); err != nil {
			return err
		}
		payload, _ = json.Marshal(QueueIntent{eid, sid, s.ExecutionGeneration(), inputDigest, g.ID, stored.Digest})
		message, err := outbox.New(caller.TenantID, messageID, outbox.Envelope{Topic: "execution.materialize.v1", SchemaVersion: "1", EventID: eventID, AggregateType: "execution_creation", AggregateID: operation, AggregateVersion: 1, Payload: payload, PayloadDigest: sandbox.Digest(payload)}, now, now)
		if err != nil {
			return err
		}
		if err = repos.Outbox().Add(ctx, message); err != nil {
			return err
		}
		payload, _ = json.Marshal(result.View)
		if err = record.Succeed(idempotency.Response{HTTPStatus: 201, Payload: payload, Reference: "/v1/executions/" + string(eid)}, operation, record.OwnerFence(), now); err != nil {
			return err
		}
		return repos.Idempotency().Update(ctx, record, record.OwnerFence())
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound), errors.Is(err, persistence.ErrNotFound):
			return Result{}, ErrNotFound
		case errors.Is(err, ErrConflict), errors.Is(err, persistence.ErrConflict), errors.Is(err, persistence.ErrRequestDigestMismatch), errors.Is(err, authority.ErrConflict):
			return Result{}, ErrConflict
		case errors.Is(err, ErrUnsupported):
			return Result{}, ErrUnsupported
		case errors.Is(err, ErrDenied), errors.Is(err, authority.ErrDenied):
			return Result{}, ErrDenied
		default:
			return Result{}, ErrUnavailable
		}
	}
	return result, nil
}
func sameRuntime(a, b session.RuntimeBinding) bool {
	for _, v := range []session.RuntimeBinding{a, b} {
		raw, err := canonical.Transform(v.RuntimeSpec)
		if err != nil || sandbox.Digest(raw) != v.RuntimeSpecDigest {
			return false
		}
		raw, err = canonical.Transform(v.RuntimeProfileSnapshot)
		if err != nil || sandbox.Digest(raw) != v.RuntimeProfileDigest {
			return false
		}
	}
	a.RuntimeSpec = nil
	b.RuntimeSpec = nil
	a.RuntimeProfileSnapshot = nil
	b.RuntimeProfileSnapshot = nil
	return reflect.DeepEqual(a, b)
}
