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
