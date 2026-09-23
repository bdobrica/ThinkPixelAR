package harness

import (
	"context"
	"errors"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimebinding"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// HarnessAdapter is the application port defined by docs/contracts/harness-adapter.md.
// Implementations validate current fences before transport and bound all payloads
// by negotiated limits. Results are observations, never authority or durable state.
// Cancellation can leave an unknown outcome: reconcile the same operation through
// Status, never blindly repeat a mutation. Unsupported operations fail before work.
type HarnessAdapter interface {
	Descriptor(context.Context) (AdapterDescriptor, error)
	Negotiate(context.Context, CompatibilityRequest) (CompatibilityResult, error)
	Start(context.Context, StartHarnessRequest) (HarnessHandle, error)
	Resume(context.Context, ResumeHarnessRequest) (HarnessHandle, error)
	Execute(context.Context, HarnessHandle, ExecuteRequest) (HarnessEventStream, error)
	Signal(context.Context, HarnessHandle, SignalRequest) error
	Interrupt(context.Context, HarnessHandle, InterruptRequest) error
	PrepareCheckpoint(context.Context, HarnessHandle, CheckpointRequest) (HarnessCheckpoint, error)
	Status(context.Context, HarnessHandle) (HarnessStatus, error)
	Close(context.Context, HarnessHandle, CloseHarnessRequest) error
}

var (
	ErrInvalid         = errors.New("harness INVALID_REQUEST")
	ErrUnsupported     = errors.New("harness CAPABILITY_UNAVAILABLE")
	ErrIncompatible    = errors.New("harness INCOMPATIBLE")
	ErrStale           = errors.New("harness STALE_OR_UNAUTHORIZED")
	ErrConflict        = errors.New("harness CONFLICT")
	ErrStartFailed     = errors.New("harness START_FAILED")
	ErrProtocol        = errors.New("harness PROTOCOL_VIOLATION")
	ErrOutcomeUnknown  = errors.New("harness OUTCOME_UNKNOWN")
	ErrStreamIntegrity = errors.New("harness STREAM_INTEGRITY")
	ErrCheckpoint      = errors.New("harness CHECKPOINT_FAILED")
)

// VersionRange is declarative input to negotiation, not an executable expression.
// HNS-002 supplies bounded parsing/selection; a range alone proves no compatibility.
type VersionRange struct{ Minimum, Maximum string }

type AdapterDescriptor struct {
	Kind, ImplementationVersion, ContractVersion string
	HarnessProtocols, AgentdProtocols            []VersionRange
	VendorStateFormats                           []string
	Capabilities                                 Capabilities
	Limits                                       AdapterLimits
}

type AdapterLimits struct {
	InputBytes, InputItems, EventBytes, EventsPerSecond, BufferedEvents int
	SignalBytes, DiagnosticBytes, VendorStatePaths                      int
	ShutdownTimeout, CheckpointTimeout                                  time.Duration
}

type CompatibilityRequest struct {
	ContractVersion, EventSchemaVersion            string
	RuntimeDigest, AdapterKind, AdapterBuildDigest string
	HarnessProtocol, AgentdProtocol                VersionRange
	VendorStateFormat, CheckpointFormat            string
	RequiredCapabilities, OptionalCapabilities     []HarnessCapability
	Limits                                         AdapterLimits
	Resume                                         *CheckpointReference
	AllowPrerelease                                bool
}

// CompatibilityResult is persisted by trusted composition and checked against
// the actual handshake. Capabilities/limits never grant execution permission.
type CompatibilityResult struct {
	AdapterKind, ImplementationVersion, AdapterBuildDigest               string
	ContractVersion, EventSchemaVersion, HarnessProtocol, AgentdProtocol string
	VendorStateFormat, CheckpointFormat                                  string
	RuntimeDigest, RequestDigest, ResultDigest, ReasonCode               string
	Capabilities                                                         Capabilities
	IgnoredOptionalCapabilities                                          []HarnessCapability
	Limits                                                               AdapterLimits
}

type Fence struct {
	TenantID, SessionID, ExecutionID, AttemptID, SandboxBindingID primitives.ID
	Generation, AttemptOrdinal                                    uint64
}

// Mutation contains trusted correlation, not a caller-supplied authorization.
type Mutation struct {
	Fence     Fence
	Operation runtimebinding.OperationIdentity
	Deadline  time.Time
}

// HarnessHandle is an AR identity, not a vendor thread ID or bearer credential.
// All fields are immutable for a process instance; Status also rechecks its fence.
type HarnessHandle struct {
	ID, ProcessInstanceID                              primitives.ID
	Fence                                              Fence
	AdapterKind, AdapterBuildDigest, NegotiationDigest string
	VendorSessionReference                             string
}

type Mount struct {
	Path, Reference string
	ReadOnly        bool
}

type StartHarnessRequest struct {
	Mutation
	HandleID                         primitives.ID
	RuntimeDigest, NegotiationDigest string
	Workspace                        Mount
	VendorMounts                     []Mount
	Configuration                    map[string]string // Bounded non-secret configuration only.
	CredentialReferences             []string          // Opaque trusted injection references, never values.
}

// CheckpointReference must be integrity-validated by trusted storage before use.
// It cannot name an arbitrary client-selected filesystem path.
type CheckpointReference struct {
	Reference, IntegrityDigest, VendorSessionReference                          string
	AdapterKind, AdapterBuildDigest, HarnessProtocol, VendorStateFormat, Format string
}

type ResumeHarnessRequest struct {
	StartHarnessRequest
	Checkpoint CheckpointReference
}

// Content carries either bounded inline content or a protected reference, never both.
type Content struct {
	Classification    runtimeevent.Classification
	Schema            string
	Inline            []byte
	ArtifactReference string
}

type ExecuteRequest struct {
	Mutation
	InputID primitives.ID
	Input   Content
	Options map[string]string // Only normalized options allowed by the ExecutionGrant.
}

type SignalRequest struct {
	Mutation
	Kind  string
	Input Content
}
type InterruptRequest struct {
	Mutation
	ReasonCode string
}
type CheckpointRequest struct {
	Mutation
	Format string
}
type CloseHarnessRequest struct {
	Mutation
	ReasonCode string
}

// HarnessCheckpoint is a candidate only. Trusted storage independently validates
// paths/content and publishes durable state. Paths are relative to declared roots.
type HarnessCheckpoint struct {
	VendorSessionReference, VendorStateFormat string
	Paths                                     []string
	Quiesced                                  bool
	ReasonCode                                string
}

type HarnessStatus struct {
	State                 runtimebinding.HarnessState
	Operation             runtimebinding.OperationIdentity
	ObservedAt            time.Time
	ExitClass, ReasonCode string
}

// HarnessEvent is an untrusted candidate envelope, not a persisted Runtime Event.
// HNS-004 owns its registered type/payload mapping and reasoning exclusion.
type HarnessEvent struct {
	StreamID, EventID                  primitives.ID
	Operation                          runtimebinding.OperationIdentity
	Handle                             HarnessHandle
	Sequence                           uint64
	Type, SchemaVersion, VendorEventID string
	OccurredAt, ObservedAt             time.Time
	Content                            Content
}

// HarnessEventStream is pull-based for explicit backpressure. Next honors context,
// returns io.EOF at stream end (not proof of Execution success), and never buffers
// beyond negotiated limits. Close releases the subscription, not the harness;
// stopping work requires Interrupt/Close through the adapter. One reader at a time.
type HarnessEventStream interface {
	Next(context.Context) (HarnessEvent, error)
	Close() error
}
