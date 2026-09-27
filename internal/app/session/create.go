package session

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/config/sessionruntimes"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/idempotency"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/outbox"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var (
	ErrInvalid     = errors.New("invalid Session creation request")
	ErrForbidden   = errors.New("Session access denied")
	ErrUnsupported = errors.New("unsupported Session source or runtime")
	ErrConflict    = errors.New("idempotency key conflict")
	ErrUnavailable = errors.New("Session creation unavailable")
	keyPattern     = regexp.MustCompile(`^[A-Za-z0-9._~-]{16,128}$`)
	idPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	digestPattern  = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

const CreateAction = "sessions.create.v1"

// Caller comes only from trusted authentication. PrincipalDigest binds issuer,
// subject and delegation identity; credentials are never passed to this service.
type Caller struct {
	TenantID        primitives.ID
	PrincipalDigest string
}
type Source struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference,omitempty"`
	Digest    string `json:"digest,omitempty"`
}
type CreateRequest struct {
	RuntimeID string `json:"agent_runtime_spec_id"`
	ProfileID string `json:"runtime_profile_id"`
	Source    Source `json:"source"`
}

// Access checks current tenant status and policy for create (empty Session ID)
// or disclosure of an existing Session (nonempty ID), including every replay.
type Access func(context.Context, Caller, CreateRequest, primitives.ID) error

type View struct {
	ID              primitives.ID `json:"id"`
	State           domain.State  `json:"state"`
	StateVersion    uint64        `json:"state_version"`
	Generation      uint64        `json:"execution_generation"`
	RuntimeID       string        `json:"agent_runtime_spec_id"`
	ProfileID       string        `json:"runtime_profile_id"`
	WorkspaceID     primitives.ID `json:"workspace_id"`
	AuthorityMode   string        `json:"authority_mode"`
	AuthorityIssuer string        `json:"authority_issuer"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}
type Result struct {
	View     View
	Replayed bool
}

// ProvisioningIntent is a versioned, credential-free outbox payload. Workspace
// and source identities are reserved once; consumers must reconcile these exact
// IDs and persist their bindings before acknowledging this message.
type ProvisioningIntent struct {
	SessionID            primitives.ID   `json:"session_id"`
	WorkspaceID          primitives.ID   `json:"workspace_id"`
	WorkspaceOperationID primitives.ID   `json:"workspace_operation_id"`
	SourceOperationID    primitives.ID   `json:"source_operation_id"`
	Request              CreateRequest   `json:"request"`
	PrincipalDigest      string          `json:"principal_digest"`
	RuntimeDigest        string          `json:"runtime_digest"`
	ProfileDigest        string          `json:"profile_digest"`
	Implementation       json.RawMessage `json:"implementation"`
	ImplementationDigest string          `json:"implementation_digest"`
	Qualification        json.RawMessage `json:"qualification"`
	PolicyDigest         string          `json:"policy_digest"`
	ResolvedAt           time.Time       `json:"resolved_at"`
}

type Creator struct {
	store   persistence.TransactionManager
	catalog *sessionruntimes.Catalog
	clock   clock.Clock
	access  Access
}

func NewCreator(store persistence.TransactionManager, catalog *sessionruntimes.Catalog, clk clock.Clock, access Access) (*Creator, error) {
	if store == nil || catalog == nil || clk == nil || access == nil {
		return nil, ErrUnavailable
	}
	return &Creator{store, catalog, clk, access}, nil
}
func (c *Creator) Create(ctx context.Context, caller Caller, key string, r CreateRequest) (Result, error) {
	var result Result
	if _, err := primitives.ParseID(string(caller.TenantID)); err != nil || !digestPattern.MatchString(caller.PrincipalDigest) {
		return result, ErrForbidden
	}
	if !keyPattern.MatchString(key) || !idPattern.MatchString(r.RuntimeID) || !idPattern.MatchString(r.ProfileID) {
		return result, ErrInvalid
	}
	// No external source resolver yet: never persist URLs, mutable references or credentials.
	if r.Source.Kind != "empty" {
		return result, ErrUnsupported
	}
	if r.Source.Reference != "" || r.Source.Digest != "" {
		return result, ErrInvalid
	}
	raw, _ := json.Marshal(r)
	requestDigest := sandbox.Digest(raw)
	now := c.clock.Now().UTC()
	if now.IsZero() {
		return result, ErrUnavailable
	}
	ids := make([]primitives.ID, 7)
	for i := range ids {
		var err error
		ids[i], err = primitives.NewID(now)
		if err != nil {
			return result, ErrUnavailable
		}
	}
	operation, sessionID, workspaceID, workspaceOp, sourceOp, eventID, messageID := ids[0], ids[1], ids[2], ids[3], ids[4], ids[5], ids[6]
	scope := idempotency.Scope{PrincipalDigest: caller.PrincipalDigest, Action: CreateAction, KeyDigest: sandbox.Digest([]byte(key))}
	// Session-creating records are excluded from generic expiry deletion until a
	// resource/tombstone-aware retention workflow exists (see persistence adapter).
	candidate, err := idempotency.New(caller.TenantID, operation, scope, "session-create-v1", requestDigest, operation, sessionID, operation, operation, now.Add(time.Minute), now.Add(365*24*time.Hour), now)
	if err != nil {
		return result, ErrUnavailable
	}
	err = c.store.WithinTransaction(ctx, caller.TenantID, func(ctx context.Context, repos persistence.Repositories) error {
		// Recheck current policy before revealing even a key conflict.
		if err := c.access(ctx, caller, r, ""); err != nil {
			return ErrForbidden
		}
		record, created, err := repos.Idempotency().Reserve(ctx, candidate)
		if err != nil {
			return err
		}
		if record.RequestDigest() != requestDigest {
			return ErrConflict
		}
		if !created {
			if c.access(ctx, caller, r, record.ResourceID()) != nil {
				return ErrForbidden
			}
			response, ok := record.Response()
			if !ok || record.State() != idempotency.Succeeded || record.NormalizationVersion() != "session-create-v1" || json.Unmarshal(response.Payload, &result.View) != nil {
				return ErrUnavailable
			}
			result.Replayed = true
			return nil
		}
		resolution, err := c.catalog.Resolve(r.RuntimeID, r.ProfileID)
		if err != nil {
			return ErrUnsupported
		}
		s, err := domain.New(caller.TenantID, sessionID, resolution.Binding, now)
		if err != nil {
			return err
		}
		if err = repos.Sessions().Add(ctx, s); err != nil {
			return err
		}
		result.View = View{ID: sessionID, State: s.State(), StateVersion: s.StateVersion(), Generation: s.ExecutionGeneration(), RuntimeID: r.RuntimeID, ProfileID: r.ProfileID, WorkspaceID: workspaceID, AuthorityMode: "local", AuthorityIssuer: resolution.Binding.AuthorityNamespace, CreatedAt: now, UpdatedAt: now}
		payload, _ := json.Marshal(struct {
			State           domain.State `json:"state"`
			AuthorityMode   string       `json:"authority_mode"`
			AuthorityIssuer string       `json:"authority_issuer"`
		}{s.State(), result.View.AuthorityMode, result.View.AuthorityIssuer})
		event, err := runtimeevent.New(eventID, caller.TenantID, sessionID, "", "", 1, 0, "session.created", now, now, runtimeevent.SourceAgentRuntime, runtimeevent.Internal, payload, runtimeevent.Correlation{RequestID: operation}, "session-lifetime", nil)
		if err != nil {
			return err
		}
		if err = repos.RuntimeEvents().Append(ctx, event); err != nil {
			return err
		}
		intent := ProvisioningIntent{SessionID: sessionID, WorkspaceID: workspaceID, WorkspaceOperationID: workspaceOp, SourceOperationID: sourceOp, Request: r, PrincipalDigest: caller.PrincipalDigest, RuntimeDigest: resolution.Binding.RuntimeSpecDigest, ProfileDigest: resolution.Binding.RuntimeProfileDigest, Implementation: resolution.Implementation, ImplementationDigest: resolution.ImplementationDigest, Qualification: resolution.Qualification, PolicyDigest: resolution.PolicyDigest, ResolvedAt: now}
		payload, err = json.Marshal(intent)
		if err != nil {
			return err
		}
		// The outbox aggregate is this creation operation at version 1, not the
		// Session at version 0. Its event ID correlates the durable creation event.
		message, err := outbox.New(caller.TenantID, messageID, outbox.Envelope{Topic: "session.provision.v1", SchemaVersion: "1", EventID: eventID, AggregateType: "session_creation", AggregateID: operation, AggregateVersion: 1, Payload: payload, PayloadDigest: sandbox.Digest(payload)}, now, now)
		if err != nil {
			return err
		}
		if err = repos.Outbox().Add(ctx, message); err != nil {
			return err
		}
		payload, _ = json.Marshal(result.View)
		if err = record.Succeed(idempotency.Response{HTTPStatus: 201, Payload: payload, Reference: "/v1/sessions/" + string(sessionID)}, operation, record.OwnerFence(), now); err != nil {
			return err
		}
		return repos.Idempotency().Update(ctx, record, record.OwnerFence())
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrForbidden), errors.Is(err, ErrUnsupported), errors.Is(err, ErrConflict):
			return Result{}, err
		case errors.Is(err, persistence.ErrRequestDigestMismatch):
			return Result{}, ErrConflict
		default:
			return Result{}, ErrUnavailable
		}
	}
	return result, nil
}
