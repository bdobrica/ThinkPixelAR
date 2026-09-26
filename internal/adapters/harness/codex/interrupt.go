package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
)

type turnTarget struct{ thread, turn string }

// InterruptTurn requests cooperative cancellation of this client's sole accepted
// turn. The trusted caller owns authority/fencing and bounded process escalation.
// Success is an empty protocol acknowledgement, never a terminal observation.
// One attempt is allowed per client; retries return its result without resending.
func (c *Client) InterruptTurn(ctx context.Context) (err error) {
	if !c.interruptGate.TryLock() {
		return harness.ErrConflict
	}
	defer c.interruptGate.Unlock()
	target := c.interruptTarget.Load()
	if target == nil {
		return harness.ErrInvalid
	}
	if c.interruptAttempted {
		return c.interruptErr
	}
	if ctx.Err() != nil {
		return harness.ErrOutcomeUnknown
	}
	c.interruptAttempted = true
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, c.Close)
	defer stop()
	defer func() {
		if ctx.Err() != nil {
			err = harness.ErrOutcomeUnknown
		}
		c.interruptErr = err
		if err != nil {
			c.Close()
		}
	}()
	request, _ := json.Marshal(map[string]any{"id": 4, "method": "turn/interrupt", "params": map[string]string{"threadId": target.thread, "turnId": target.turn}})
	request = append(request, '\n')
	defer clear(request)
	c.interruptSent.Store(true)
	if n, e := c.in.Write(request); e != nil || n != len(request) {
		return harness.ErrOutcomeUnknown
	}
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if c.interruptAcknowledged.Load() {
			return nil
		}
		if ctx.Err() != nil {
			return harness.ErrOutcomeUnknown
		}
		// Next owns the reader while streaming and recognizes the response itself.
		// Otherwise retain bounded intervening notifications for the next subscriber
		// read, including a terminal notification arriving before the response.
		if c.gate.TryLock() {
			err = c.readInterruptFrame()
			c.gate.Unlock()
			if err != nil {
				return err
			}
		} else {
			select {
			case <-ctx.Done():
				return harness.ErrOutcomeUnknown
			case <-tick.C:
			}
		}
	}
}

// Called only while holding the protocol reader gate.
func (c *Client) readInterruptFrame() error {
	// A streaming reader may have acknowledged just before releasing the gate.
	if c.interruptAcknowledged.Load() {
		return nil
	}
	raw, err := c.reader.ReadSlice('\n')
	if err != nil {
		clear(raw)
		return harness.ErrOutcomeUnknown
	}
	defer clear(raw)
	if validateVendorJSON(raw) != nil {
		return harness.ErrProtocol
	}
	f, err := object(raw)
	if err != nil {
		return err
	}
	if f["id"] != nil {
		return c.interruptResponse(f)
	}
	if !keys(f, "method params emittedAtMs") || field(f, "method") == "" {
		return harness.ErrProtocol
	}
	if _, err := object(f["params"]); err != nil {
		return err
	}
	if len(c.pending) >= 32 || c.pendingBytes+len(raw) > maxHandshakeBytes {
		return harness.ErrStreamIntegrity
	}
	c.pending = append(c.pending, bytes.Clone(raw))
	c.pendingBytes += len(raw)
	return nil
}

func (c *Client) interruptResponse(f map[string]json.RawMessage) error {
	if !c.interruptSent.Load() || c.interruptAcknowledged.Load() || len(f) != 2 || string(f["id"]) != "4" {
		return harness.ErrProtocol
	}
	result, err := object(f["result"])
	if err != nil || len(result) != 0 {
		return harness.ErrProtocol
	}
	c.interruptAcknowledged.Store(true)
	return nil
}

func (c *Client) readEventFrame() ([]byte, error) {
	if len(c.pending) == 0 {
		return c.reader.ReadSlice('\n')
	}
	raw := c.pending[0]
	c.pending[0] = nil
	c.pending = c.pending[1:]
	c.pendingBytes -= len(raw)
	return raw, nil
}
