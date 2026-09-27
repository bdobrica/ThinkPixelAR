package sandbox

import (
	"context"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// ReplacementResources are prepared by trusted materialization code. References
// must be fresh; the Workspace identity, generation and mount remain unchanged.
type ReplacementResources struct {
	// Trusted materialization allocates these identities before preparing scoped
	// resources. Retry the same tuple; competing tuples cannot replace the winner.
	AttemptID           primitives.ID
	SandboxID           primitives.ID
	AcquireOperationID  primitives.ID
	AttachmentReference string
	BootstrapReference  string
}

// ReplacementPolicy must verify current authority/lease, immutable runtime
// eligibility, retry budget, old credential retirement, and independently prove
// that the new attachment/bootstrap belong to this candidate and contain no old
// execution secrets. It must not infer these from names or sandbox reports.
// Called with the aggregate locked; implementations must be bounded and must not
// re-enter AR persistence or mutate infrastructure in this callback.
type ReplacementPolicy interface {
	CheckReplacement(context.Context, Binding, AcquireRequest) error
}

type ReplacementStore interface {
	ReplaceDeleted(context.Context, primitives.ID, primitives.ID, ReplacementResources) (Binding, error)
}
