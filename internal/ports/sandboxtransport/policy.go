package sandboxtransport

import (
	"context"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
)

type Purpose string

const (
	IssueBootstrap Purpose = "ISSUE_BOOTSTRAP"
	RenewIdentity  Purpose = "RENEW_IDENTITY"
	AcceptStream   Purpose = "ACCEPT_STREAM"
	DeliverFrame   Purpose = "DELIVER_FRAME"
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
