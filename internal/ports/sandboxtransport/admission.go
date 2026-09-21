package sandboxtransport

import (
	"context"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// Identity is derived only from the verified client certificate URI SAN.
type Identity struct{ TenantID, SandboxID, AttemptID primitives.ID }
type Peer struct {
	Identity          Identity
	CertificateDigest string
	ExpiresAt         time.Time
}

// Expectations is immutable materialization evidence loaded by trusted AR code.
// It must never be assembled from a sandbox's claimed identity or capabilities.
type Expectations struct {
	Binding               *agentdv1.Binding
	Challenge             []byte
	BuildDigest           string
	AdapterKind           string
	AdapterDigest         string
	SupportedCapabilities []string
	RequiredCapabilities  []string
	Limits                *agentdv1.Limits
}

// Authorizer owns current binding/authority/provider checks, bootstrap consumption
// and durable epoch allocation. Implementations must honor context cancellation.
// Transport identity cannot select a different tenant or acquire Run authority.
type Authorizer interface {
	Admit(context.Context, Peer, []byte) (Lease, error)
}

// Lease gates an authenticated stream. Deadline cannot exceed current authority
// or certificate lifetime. Check revalidates durable fences, epoch, revocation,
// operation idempotency/sequence and message semantics before every delivery.
// Check receives a private frame copy. Close releases only this lease's epoch.
// Neither callback may log raw frames, proofs or credentials.
type Lease struct {
	Expected     Expectations
	ConnectionID primitives.ID
	Epoch        uint64
	Deadline     time.Time
	Check        func(context.Context, *agentdv1.Envelope) error
	Close        func()
}
