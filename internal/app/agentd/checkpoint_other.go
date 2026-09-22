//go:build !linux

package agentd

import (
	"context"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type Checkpoints struct{}

func NewCheckpoints(*Processes, []string, string, CheckpointHook) (*Checkpoints, error) {
	return nil, ErrCheckpoint
}
func (*Checkpoints) Prepare(context.Context, primitives.ID, PreparedStateConsumer) error {
	return ErrCheckpoint
}
