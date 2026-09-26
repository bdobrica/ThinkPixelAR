package agentd

import (
	"context"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func (p *Processes) startTurn(ctx context.Context, operation primitives.ID, input control.TurnInput) error {
	if !p.executeTurn || input.Validate() != nil {
		return ErrControl
	}
	_, err := p.operation(ctx, 10*time.Second, func(ctx context.Context) (primitives.ID, error) {
		c := p.current
		if c == nil || c.codex == nil || c.threadID == "" {
			return "", ErrControl
		}
		select {
		case <-c.done:
			return "", ErrControl
		default:
		}
		_, err := c.codex.StartTurn(ctx, operation, input.InputID, input.Text)
		if err != nil {
			_ = p.stop(context.Background(), c.id, true)
		}
		return c.id, err
	})
	return err
}

// interruptTurn preserves process-control.v1's bounded-stop contract. Give the
// pinned turn driver a cooperative opportunity, then stop/reap even after ack.
func (p *Processes) interruptTurn(ctx context.Context, id primitives.ID) error {
	controls, err := NewControls(p, p.commandBytes, nil, func(ctx context.Context, target primitives.ID, _ InterruptReason) error {
		c := p.current // Controls holds the process gate and verified target identity.
		if !p.executeTurn || c == nil || c.id != target || c.codex == nil {
			return ErrControl
		}
		return c.codex.InterruptTurn(ctx)
	})
	if err != nil {
		return err
	}
	err = controls.Interrupt(ctx, id, InterruptCancel)
	if err == ErrInterruptEscalated {
		return nil
	}
	if err != nil {
		return err
	}
	return p.Stop(ctx, id)
}
