package agentd

import (
	"bytes"
	"context"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// Controls routes normalized signals without exposing arbitrary OS signal numbers.
// Construction does not grant authority; use only after authenticated admission.
type Controls struct {
	processes *Processes
	maxBytes  uint32
	signals   map[string]SignalHandler
	interrupt InterruptHandler
}

func NewControls(p *Processes, maxBytes uint32, signals map[string]SignalHandler, interrupt InterruptHandler) (*Controls, error) {
	if p == nil || maxBytes == 0 || maxBytes > protocol.HardLimits().CommandBytes || maxBytes > p.commandBytes || len(signals) > 32 {
		return nil, ErrControl
	}
	c := &Controls{processes: p, maxBytes: maxBytes, signals: make(map[string]SignalHandler), interrupt: interrupt}
	for name, handler := range signals {
		if !controlName(name) || handler == nil {
			return nil, ErrControl
		}
		c.signals[name] = handler
	}
	return c, nil
}
func controlName(name string) bool {
	if len(name) == 0 || len(name) > 64 || name == "cancel" || name == "interrupt" {
		return false
	}
	for i, b := range []byte(name) {
		if !(b >= 'a' && b <= 'z' || i > 0 && (b >= '0' && b <= '9' || b == '.' || b == '_' || b == '-')) {
			return false
		}
	}
	return true
}
func (c *Controls) Signal(ctx context.Context, id primitives.ID, name string, payload []byte) error {
	handler := c.signals[name]
	if handler == nil || len(payload) > int(c.maxBytes) {
		return ErrControl
	}
	if ctx.Err() != nil {
		return ErrProcessDeadline
	}
	owned := bytes.Clone(payload)
	err := c.deliver(ctx, id, false, func(ctx context.Context) error { defer clear(owned); return handler(ctx, id, owned) })
	if err == ErrProcessBusy || err == ErrProcessStale || err == ErrProcessClosed {
		clear(owned)
	}
	return err
}

// Interrupt attempts cooperative delivery, or stops the group if unsupported,
// failed or unacknowledged. ErrInterruptEscalated means fallback Stop completed;
// nil means acknowledgement only. Neither result terminalizes an Execution.
func (c *Controls) Interrupt(ctx context.Context, id primitives.ID, reason InterruptReason) error {
	if reason < InterruptCancel || reason > InterruptShutdown {
		return ErrControl
	}
	var call func(context.Context) error
	if c.interrupt != nil {
		call = func(ctx context.Context) error { return c.interrupt(ctx, id, reason) }
	}
	return c.deliver(ctx, id, true, call)
}
func controlInvoke(call func(context.Context) error, ctx context.Context) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrControl
		}
	}()
	return call(ctx)
}
func (c *Controls) deliver(ctx context.Context, id primitives.ID, interrupt bool, call func(context.Context) error) error {
	p := c.processes
	grace := time.Duration(p.config.StopGraceMS) * time.Millisecond
	_, err := p.operation(ctx, grace+p.stopBudget(), func(ctx context.Context) (primitives.ID, error) {
		child := p.current
		if child == nil || id == "" || child.id != id {
			return "", ErrProcessStale
		}
		select {
		case <-child.done:
			return "", ErrProcessStale
		default:
		}
		if ctx.Err() != nil {
			return "", ErrProcessDeadline
		}
		if call == nil {
			if err := p.stop(ctx, id, false); err != nil {
				return "", err
			}
			return "", ErrInterruptEscalated
		}
		attempt, cancel := context.WithTimeout(ctx, grace)
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- controlInvoke(call, attempt) }()
		finished := false
		var deliveryErr error
		select {
		case deliveryErr = <-result:
			finished = true
		case <-attempt.Done():
		}
		expired := attempt.Err() != nil
		cancel()
		if finished && !expired && deliveryErr == nil {
			// Retain the target ID so operation cleans up if this acknowledgement
			// loses the race with caller cancellation before it is delivered.
			return id, nil
		}
		if finished && !expired && !interrupt {
			return "", ErrControl
		}
		// A timed-out callback might still act on the old process. Stop it using an
		// independent finite cleanup budget, and retain the gate until the callback
		// returns. Go cannot forcibly cancel trusted code which ignores its context.
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), p.stopBudget())
		stopErr := p.stop(cleanup, id, false)
		cleanupCancel()
		if !finished {
			<-result
		}
		if stopErr != nil {
			return "", stopErr
		}
		if interrupt {
			return "", ErrInterruptEscalated
		}
		return "", ErrControl
	})
	return err
}
