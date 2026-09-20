package sandbox

import (
	"context"
	"errors"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type State string

const (
	Requested    State = "REQUESTED"
	Provisioning State = "PROVISIONING"
	Ready        State = "READY"
	Active       State = "ACTIVE"
	Suspending   State = "SUSPENDING"
	Suspended    State = "SUSPENDED"
	Resuming     State = "RESUMING"
	Releasing    State = "RELEASING"
	Released     State = "RELEASED"
	Failed       State = "FAILED"
	Unknown      State = "UNKNOWN"
)

var (
	ErrInvalid     = errors.New("sandbox INVALID_REQUEST")
	ErrUnsupported = errors.New("sandbox UNSUPPORTED")
	ErrConflict    = errors.New("sandbox CONFLICT")
	ErrNotFound    = errors.New("sandbox NOT_FOUND")
	ErrUnavailable = errors.New("sandbox UNAVAILABLE")
	ErrTimeout     = errors.New("sandbox TIMEOUT")
	ErrPermission  = errors.New("sandbox PERMISSION")
	ErrIntegrity   = errors.New("sandbox INTEGRITY")
)

type Operation struct {
	ID     string
	Digest string
}
type Scope struct {
	TenantID       primitives.ID
	SessionID      primitives.ID
	ExecutionID    primitives.ID
	AttemptID      primitives.ID
	SandboxID      primitives.ID
	Generation     uint64
	AttemptOrdinal uint64
}
type Runtime struct {
	Image        string
	Architecture string
	Entrypoint   []string
}
type Attachment struct {
	Reference   string
	WorkspaceID primitives.ID
	Generation  uint64
	MountPath   string
	ReadOnly    bool
}
type AcquireRequest struct {
	Scope                Scope
	Operation            Operation
	Runtime              Runtime
	Profile              runtimeprofile.Profile
	ProfileDigest        string
	ImplementationDigest string
	Workspace            Attachment
	BootstrapReference   string
	Deadline             time.Time
}

// Binding is authoritative AR state, reserved before any external mutation.
// ProviderReference remains opaque outside infrastructure adapters.
type Binding struct {
	Request           AcquireRequest
	ProviderReference string
}
type Handle struct {
	SandboxID         primitives.ID
	ProviderKind      string
	ProviderReference string
	State             State
	ObservedAt        time.Time
}

// BindingStore must enforce tenant scope, current Attempt/generation, unique
// operation+digest and SandboxID, and atomic immutable provider-reference binding.
// Reserve must durably commit before returning. A failed/ambiguous provider call
// never frees a reservation. Production implementations may not use memory.
type BindingStore interface {
	Reserve(context.Context, AcquireRequest) (Binding, error)
	Get(context.Context, primitives.ID, primitives.ID) (Binding, error)
	BindReference(context.Context, primitives.ID, primitives.ID, string) error
}

type EffectiveFacts struct {
	IsolationClass      string
	Image               string
	Architecture        string
	ResourceDigest      string
	NetworkClass        string
	AttachmentReference string
	Verified            bool
}
type Status struct {
	Handle             Handle
	State              State
	Reason             string
	ProviderGeneration string
	Effective          EffectiveFacts
}

// LifecycleStore records immutable operation identities and elects the latest
// desired mutation with a monotonically increasing revision. Replays of an
// operation superseded by a newer mutation fail with ErrConflict. Admission and
// cleanup ownership checks are part of this durable transaction.
type LifecycleStore interface {
	BeginOperation(context.Context, primitives.ID, primitives.ID, string, Operation) (uint64, error)
}
