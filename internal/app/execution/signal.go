package execution

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/idempotency"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/outbox"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	canonical "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

const SignalAction = "executions.signal.v1"
const MaxSignalBytes = 65536

type SignalRequest struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// SignalAccess checks current signal-mutation and disclosure permission for the
// persisted Session/Execution on every request, including replay. Denial is 404.
type SignalAccess func(context.Context, Caller, primitives.ID, primitives.ID) error

// SignalPolicy is a trusted, transaction-local validator of negotiated capability
// and payload schema. Permission responses must match an outstanding request and
// remain within existing authority. It must not dispatch work or call a remote
// service. Return ErrInvalid, ErrUnsupported or ErrDenied for safe client errors.
// It runs only on first acceptance; SignalAccess runs on every request/replay.
type SignalPolicy func(context.Context, persistence.Repositories, Caller, *domain.Execution, SignalRequest) error

type SignalOperation struct {
	ID               primitives.ID `json:"id"`
	Kind             string        `json:"kind"`
	State            string        `json:"state"`
	ResourceLocation string        `json:"resource_location"`
	CreatedAt        time.Time     `json:"created_at"`
}
type SignalResult struct {
	Operation SignalOperation
	Replayed  bool
}

// SignalIntent is metadata only. Delivery must load and verify the private body,
// revalidate authority/current Session and Attempt fences, and use OperationID as
// its deduplication identity. Ambiguous non-deduplicating delivery must not retry.
type SignalIntent struct {
	OperationID      primitives.ID `json:"operation_id"`
	ExecutionID      primitives.ID `json:"execution_id"`
	SessionID        primitives.ID `json:"session_id"`
	Generation       uint64        `json:"generation"`
	ExecutionVersion uint64        `json:"execution_version"`
	Type             string        `json:"type"`
	PayloadDigest    string        `json:"payload_digest"`
}
type Signaler struct {
	store  persistence.TransactionManager
	clock  clock.Clock
	access SignalAccess
	policy SignalPolicy
}

func NewLocalSignaler(store persistence.TransactionManager, clk clock.Clock, access SignalAccess, policy SignalPolicy) (*Signaler, error) {
	if store == nil || clk == nil || access == nil || policy == nil {
		return nil, ErrUnavailable
	}
	return &Signaler{store, clk, access, policy}, nil
}

// ParseSignal enforces the public envelope before reserving an operation. The
// negotiated policy validates type-specific contents; JSON cannot grant authority.
func ParseSignal(raw []byte) (SignalRequest, error) {
	var r SignalRequest
	if len(raw) > MaxSignalBytes || !utf8.Valid(raw) {
		return r, ErrInvalid
	}
	normalized, err := canonical.Transform(raw)
	if err != nil {
		return r, ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(normalized, &fields) != nil || len(fields) != 2 || json.Unmarshal(fields["type"], &r.Type) != nil {
		return r, ErrInvalid
	}
	if r.Type != "user_input" && r.Type != "permission_response" && r.Type != "interrupt" {
		return r, ErrInvalid
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(fields["payload"], &payload) != nil || payload == nil || len(payload) > 32 {
		return r, ErrInvalid
	}
	r.Payload = append([]byte(nil), fields["payload"]...)
	return r, nil
}
func (s *Signaler) Signal(ctx context.Context, caller Caller, eid primitives.ID, key string, request SignalRequest) (SignalResult, error) {
	var result SignalResult
	if _, err := primitives.ParseID(string(caller.TenantID)); err != nil || !digestPattern.MatchString(caller.PrincipalDigest) {
		return result, ErrDenied
	}
	if _, err := primitives.ParseID(string(eid)); err != nil || !keyPattern.MatchString(key) {
		return result, ErrInvalid
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return result, ErrInvalid
	}
	request, err = ParseSignal(raw)
	if err != nil {
		return result, err
	}
	raw, _ = json.Marshal(request)
	raw, err = canonical.Transform(raw)
	if err != nil {
		return result, ErrInvalid
	}
	bodyDigest := sandbox.Digest(raw)
	digest := sandbox.Digest(append([]byte(string(eid)+"/"), raw...))
	scope := idempotency.Scope{PrincipalDigest: caller.PrincipalDigest, Action: SignalAction, KeyDigest: sandbox.Digest([]byte(string(eid) + "/" + key))}
	now := s.clock.Now().UTC()
	op, err := primitives.NewID(now)
	if err != nil {
		return result, ErrUnavailable
	}
	eventID, err := primitives.NewID(now)
	if err != nil {
		return result, ErrUnavailable
	}
	messageID, err := primitives.NewID(now)
	if err != nil {
		return result, ErrUnavailable
	}
	record, err := idempotency.New(caller.TenantID, op, scope, "execution-signal-v1", digest, op, eid, op, op, now.Add(time.Minute), now.Add(365*24*time.Hour), now)
	if err != nil {
		return result, ErrUnavailable
	}
	err = s.store.WithinTransaction(ctx, caller.TenantID, func(ctx context.Context, repos persistence.Repositories) error {
		e, err := repos.Executions().Get(ctx, eid)
		if err != nil {
			return err
		}
		if e == nil || e.TenantID() != caller.TenantID || e.ID() != eid {
			return ErrNotFound
		}
		b := e.Binding()
		if s.access(ctx, caller, b.SessionID, eid) != nil {
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
			if !ok || record.State() != idempotency.Succeeded || record.NormalizationVersion() != "execution-signal-v1" || response.HTTPStatus != 202 || json.Unmarshal(response.Payload, &result.Operation) != nil {
				return ErrUnavailable
			}
			result.Replayed = true
			return nil
		}
		// Lock in Session -> Execution -> grant order; serialize against lifecycle
		// writers and grant revocation. Replay above never injects new work.
		sess, err := repos.Sessions().GetForUpdate(ctx, b.SessionID)
		if err != nil {
			return err
		}
		e, err = repos.Executions().GetForUpdate(ctx, eid)
		if err != nil {
			return err
		}
		if sess.State() != session.Active || sess.ExecutionGeneration() != b.SessionGeneration || e.State() != domain.Running {
			return ErrConflict
		}
		if b.AuthorityMode != "LOCAL" {
			return ErrUnsupported
		}
		grantID, err := primitives.ParseID(b.AuthorityReference)
		if err != nil {
			return ErrDenied
		}
		stored, err := repos.LocalGrants().Get(ctx, grantID)
		if err != nil {
			return err
		}
		g, err := verifyLocalBinding(e, sess, stored)
		if err != nil {
			return ErrDenied
		}
		now = s.clock.Now().UTC()
		if stored.State != "ACTIVE" || !g.ExpiresAt.After(now) || g.IssuedAt.After(now) || !e.Deadline().After(now) {
			return ErrDenied
		}
		if err = s.policy(ctx, repos, caller, e, SignalRequest{Type: request.Type, Payload: append([]byte(nil), request.Payload...)}); err != nil {
			return err
		}
		// Policy evaluation may take time; expiry is inclusive at acceptance.
		now = s.clock.Now().UTC()
		if !g.ExpiresAt.After(now) || !e.Deadline().After(now) {
			return ErrDenied
		}
		if err = repos.Executions().AddSignal(ctx, persistence.SignalRecord{ID: op, ExecutionID: eid, Payload: raw, Digest: bodyDigest}); err != nil {
			return err
		}
		intent := SignalIntent{op, eid, b.SessionID, b.SessionGeneration, e.StateVersion(), request.Type, bodyDigest}
		metadata, _ := json.Marshal(intent)
		sequence, err := repos.RuntimeEvents().NextSequence(ctx, b.SessionID)
		if err != nil {
			return err
		}
		event, err := runtimeevent.New(eventID, caller.TenantID, b.SessionID, eid, "", sequence, e.StateVersion(), "signal.accepted", now, now, runtimeevent.SourceAgentRuntime, runtimeevent.Internal, metadata, runtimeevent.Correlation{RequestID: op}, "session-lifetime", nil)
		if err != nil {
			return err
		}
		if err = repos.RuntimeEvents().Append(ctx, event); err != nil {
			return err
		}
		message, err := outbox.New(caller.TenantID, messageID, outbox.Envelope{Topic: "execution.signal.v1", SchemaVersion: "1", EventID: eventID, AggregateType: "execution_signal", AggregateID: op, AggregateVersion: 1, Payload: metadata, PayloadDigest: sandbox.Digest(metadata)}, now, now)
		if err != nil {
			return err
		}
		if err = repos.Outbox().Add(ctx, message); err != nil {
			return err
		}
		result.Operation = SignalOperation{op, "execution.signal", "PENDING", "/v1/executions/" + string(eid), now}
		response, _ := json.Marshal(result.Operation)
		if err = record.Succeed(idempotency.Response{HTTPStatus: 202, Payload: response, Reference: result.Operation.ResourceLocation}, op, record.OwnerFence(), now); err != nil {
			return err
		}
		return repos.Idempotency().Update(ctx, record, record.OwnerFence())
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound), errors.Is(err, persistence.ErrNotFound):
			return SignalResult{}, ErrNotFound
		case errors.Is(err, ErrConflict), errors.Is(err, persistence.ErrConflict), errors.Is(err, persistence.ErrRequestDigestMismatch):
			return SignalResult{}, ErrConflict
		case errors.Is(err, ErrInvalid):
			return SignalResult{}, ErrInvalid
		case errors.Is(err, ErrUnsupported):
			return SignalResult{}, ErrUnsupported
		case errors.Is(err, ErrDenied):
			return SignalResult{}, ErrDenied
		default:
			return SignalResult{}, ErrUnavailable
		}
	}
	return result, nil
}
