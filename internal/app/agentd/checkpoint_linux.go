package agentd

import (
	"context"
	"slices"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// Checkpoints holds immutable declarations supplied from the bound runtime spec.
// Construction performs lexical checks only; trusted storage must validate actual
// mounts, symlinks, special files, size, scan races and credential exclusions.
type Checkpoints struct {
	processes *Processes
	roots     []string
	format    string
	hook      CheckpointHook
}

func NewCheckpoints(p *Processes, roots []string, format string, hook CheckpointHook) (*Checkpoints, error) {
	if p == nil || !stateRoots(roots) || !stateText(format, 128) || hook == nil {
		return nil, ErrCheckpoint
	}
	return &Checkpoints{p, slices.Clone(roots), format, hook}, nil
}

// Prepare calls consume only inside the adapter's bounded preparation window.
// Call only after authenticated checkpoint admission. Success does not establish
// durability or prove that an untrusted harness stopped writing.
func (c *Checkpoints) Prepare(ctx context.Context, id primitives.ID, consume PreparedStateConsumer) error {
	if consume == nil {
		return ErrCheckpoint
	}
	controls := &Controls{processes: c.processes}
	err := controls.deliver(ctx, id, false, func(ctx context.Context) error {
		calls := 0
		var readyErr error
		err := c.hook(ctx, id, slices.Clone(c.roots), func(m StateManifest) error {
			calls++
			if calls != 1 || ctx.Err() != nil || !stateManifest(m, c.roots, c.format) {
				readyErr = ErrCheckpoint
				return readyErr
			}
			m.Paths = slices.Clone(m.Paths)
			readyErr = consume(ctx, m)
			return readyErr
		})
		if err != nil || calls != 1 || readyErr != nil || ctx.Err() != nil {
			return ErrCheckpoint
		}
		return nil
	})
	if err == ErrControl {
		return ErrCheckpoint
	}
	return err
}
