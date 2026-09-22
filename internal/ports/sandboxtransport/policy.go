package sandboxtransport

import (
	"context"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type Purpose string

const (
	IssueBootstrap Purpose = "ISSUE_BOOTSTRAP"
	// RecoverBootstrap requires an independently verified safely recoverable Attempt.
	RecoverBootstrap Purpose = "RECOVER_BOOTSTRAP"
	RenewIdentity    Purpose = "RENEW_IDENTITY"
	AcceptStream     Purpose = "ACCEPT_STREAM"
	DeliverFrame     Purpose = "DELIVER_FRAME"
)

type Authorization struct {
	Deadline, BootstrapDeadline time.Time
	Expected                    Expectations
}

// AdmissionPolicy loads immutable materialization expectations and validates
// current grant, lease, revocation and rate limits using trusted compute identity.
// Deadlines may only narrow current authority. It must not derive expectations
// from Hello, Workspace files or sandbox observations. No permissive default.
type AdmissionPolicy interface {
	AuthorizeTransport(context.Context, sandbox.ComputeIntent, Purpose) (Authorization, error)
}

// FramePolicy enforces direction, registered payload semantics, sequence/replay
// and durable operation idempotency. A compatible authenticated frame is not an
// execution authorization. Implementations honor cancellation and are concurrent-safe.
type FramePolicy interface {
	AuthorizeFrame(context.Context, sandbox.ComputeIntent, Connection, *agentdv1.Envelope) error
}

// DispatchOutcome is retained transport bookkeeping, never Execution success.
// Pending is ambiguous after interruption; neither Pending nor Unknown permits
// retransmission. Only a correlated response can move Pending to a final value.
type DispatchOutcome string

const (
	DispatchPending      DispatchOutcome = "PENDING"
	DispatchAcknowledged DispatchOutcome = "ACKNOWLEDGED"
	DispatchUnknown      DispatchOutcome = "UNKNOWN"
)

// CommandOutcomes supports restart reconciliation without retransmitting a
// command. FramePolicy owns atomic claims and correlated outcome writes. Reads
// remain available after revocation/termination but cannot authorize new work.
type CommandOutcomes interface {
	CommandOutcome(context.Context, primitives.ID, primitives.ID, primitives.ID, string) (DispatchOutcome, error)
}
