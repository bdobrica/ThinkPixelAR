package harness

import (
	"context"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// BindingReader retrieves immutable persisted identity for continuation and
// checkpoint composition. Reading a handle never validates current authority or
// proves that vendor state files have been durably checkpointed.
type BindingReader interface {
	Load(context.Context, primitives.ID, primitives.ID) (HarnessHandle, error)
}
