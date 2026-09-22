package agentd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var (
	ErrOutputLimit        = errors.New("harness output limit exceeded")
	ErrOutputBackpressure = errors.New("harness output consumer stalled")
	ErrOutputRedaction    = errors.New("harness output redaction failed")
	ErrOutputClosed       = errors.New("harness output closed")
	ErrOutputIO           = errors.New("harness output stream failed")
)

type OutputSource uint8

const (
	OutputStdout OutputSource = iota + 1
	OutputStderr
	OutputAdapterEvent
)

// Output is ephemeral content for an authorized consumer, never platform logs.
// Sequence is local capture order, not a durable Runtime Event sequence.
type Output struct {
	ProcessID primitives.ID
	Sequence  uint64
	Source    OutputSource
	Data      []byte
}

func (Output) String() string     { return "[restricted harness output]" }
func (o Output) GoString() string { return o.String() }

// OutputSanitizer is trusted, synchronous, bounded, thread-safe code, not sandbox code. It
// must use registered field allowlists, remove hidden reasoning, and redact
// credential keys, known exact secret values and token patterns before return.
// It must not retain input or log payloads. A nil sanitizer suppresses all content.
// Failure or panic terminates capture; error details never leave this boundary.
type OutputSanitizer func(OutputSource, []byte) ([]byte, error)

// SuppressOutput is the safe default for unclassified stdout/stderr and events.
func SuppressOutput(OutputSource, []byte) ([]byte, error) { return []byte("[REDACTED]"), nil }

type Capture struct {
	mu                                           sync.Mutex
	eventGate                                    sync.Mutex
	queue                                        []Output
	bytes                                        int
	sequence                                     uint64
	maxEvents, maxBytes, maxEvent, maxDiagnostic int
	wait                                         time.Duration
	sanitizer                                    OutputSanitizer
	id                                           primitives.ID
	changed                                      chan struct{}
	failed                                       chan struct{}
	closed                                       bool
	err                                          error
}

func newCapture(id primitives.ID, l *agentdv1.Limits, s OutputSanitizer) (*Capture, error) {
	hard := protocol.HardLimits()
	if l == nil || l.BufferedEvents == 0 || l.BufferedEvents > hard.BufferedEvents || l.BufferedBytes == 0 || l.BufferedBytes > hard.BufferedBytes || l.EventBytes == 0 || l.EventBytes > hard.EventBytes || l.DiagnosticBytes == 0 || l.DiagnosticBytes > hard.DiagnosticBytes || l.LivenessWindowMs == 0 || l.LivenessWindowMs > hard.LivenessWindowMs {
		return nil, ErrConfig
	}
	if s == nil {
		s = SuppressOutput
	}
	return &Capture{id: id, maxEvents: int(l.BufferedEvents), maxBytes: int(l.BufferedBytes), maxEvent: int(l.EventBytes), maxDiagnostic: int(l.DiagnosticBytes), wait: time.Duration(l.LivenessWindowMs) * time.Millisecond, sanitizer: s, changed: make(chan struct{}), failed: make(chan struct{})}, nil
}
func (c *Capture) notify() { close(c.changed); c.changed = make(chan struct{}) }
func (c *Capture) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return
	}
	c.err = err
	c.closed = true
	for i := range c.queue {
		clear(c.queue[i].Data)
		c.queue[i] = Output{}
	}
	c.queue = nil
	c.bytes = 0
	close(c.failed)
	c.notify()
}
func (c *Capture) finish() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.notify()
	return c.err
}
func (*Capture) String() string     { return "[restricted harness capture]" }
func (c *Capture) GoString() string { return c.String() }

// Close abandons consumption and invalidates queued data. It also stops a live
// captured child through its failure watcher. Receive is a single-consumer API.
func (c *Capture) Close() { c.fail(ErrOutputClosed) }
func (c *Capture) Receive(ctx context.Context) (Output, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Output{}, err
		}
		c.mu.Lock()
		if c.err != nil {
			err := c.err
			c.mu.Unlock()
			return Output{}, err
		}
		if len(c.queue) > 0 {
			out := c.queue[0]
			c.queue[0] = Output{}
			c.queue = c.queue[1:]
			c.bytes -= len(out.Data)
			c.notify()
			c.mu.Unlock()
			return out, nil
		}
		if c.closed {
			c.mu.Unlock()
			return Output{}, io.EOF
		}
		changed := c.changed
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return Output{}, ctx.Err()
		case <-changed:
		}
	}
}

// EmitEvent accepts a complete adapter record; it shares stdout/stderr budgets.
// The selected sanitizer must recognize its registered schema. No raw protocol
// frame becomes an authoritative Runtime Event merely by entering this queue.
func (c *Capture) EmitEvent(raw []byte) error {
	if !c.eventGate.TryLock() {
		c.fail(ErrOutputBackpressure)
		return ErrOutputBackpressure
	}
	defer c.eventGate.Unlock()
	return c.emit(OutputAdapterEvent, raw)
}
func sanitize(s OutputSanitizer, source OutputSource, raw []byte) (out []byte, err error) {
	defer func() {
		if recover() != nil {
			out = nil
			err = ErrOutputRedaction
		}
	}()
	return s(source, raw)
}
func (c *Capture) emit(source OutputSource, raw []byte) error {
	max := c.maxDiagnostic
	if source == OutputAdapterEvent {
		max = c.maxEvent
	}
	if len(raw) > max {
		c.fail(ErrOutputLimit)
		return ErrOutputLimit
	}
	c.mu.Lock()
	closed := c.closed
	err := c.err
	c.mu.Unlock()
	if closed {
		if err != nil {
			return err
		}
		return ErrOutputClosed
	}
	// Own and clear the callback input even if it returns an alias of that input.
	input := bytes.Clone(raw)
	safe, err := sanitize(c.sanitizer, source, input)
	if err != nil {
		clear(input)
		c.fail(ErrOutputRedaction)
		return ErrOutputRedaction
	}
	if len(safe) > max || len(safe) > c.maxBytes {
		clear(input)
		c.fail(ErrOutputLimit)
		return ErrOutputLimit
	}
	data := bytes.Clone(safe)
	clear(input)
	transferred := false
	defer func() {
		if !transferred {
			clear(data)
		}
	}()
	timer := time.NewTimer(c.wait)
	defer timer.Stop()
	for {
		c.mu.Lock()
		if c.closed {
			err := c.err
			c.mu.Unlock()
			if err != nil {
				return err
			}
			return ErrOutputClosed
		}
		if len(c.queue) < c.maxEvents && c.bytes+len(data) <= c.maxBytes {
			c.sequence++
			c.queue = append(c.queue, Output{ProcessID: c.id, Sequence: c.sequence, Source: source, Data: data})
			c.bytes += len(data)
			c.notify()
			c.mu.Unlock()
			transferred = true
			return nil
		}
		changed := c.changed
		c.mu.Unlock()
		select {
		case <-changed:
		case <-timer.C:
			c.fail(ErrOutputBackpressure)
			return ErrOutputBackpressure
		}
	}
}

type captureWriter struct {
	capture *Capture
	source  OutputSource
	pending []byte
}

func (w *captureWriter) Write(raw []byte) (int, error) {
	total := len(raw)
	for len(raw) > 0 {
		n := bytes.IndexByte(raw, '\n')
		end := n >= 0
		if !end {
			n = len(raw)
		}
		if len(w.pending)+n > w.capture.maxDiagnostic {
			clear(w.pending)
			w.pending = nil
			w.capture.fail(ErrOutputLimit)
			return 0, ErrOutputLimit
		}
		w.pending = append(w.pending, raw[:n]...)
		raw = raw[n:]
		if end {
			raw = raw[1:]
			err := w.capture.emit(w.source, w.pending)
			clear(w.pending)
			w.pending = w.pending[:0]
			if err != nil {
				return 0, err
			}
		}
	}
	return total, nil
}
func (w *captureWriter) finish() {
	if len(w.pending) > 0 {
		_ = w.capture.emit(w.source, w.pending)
	}
	clear(w.pending)
	w.pending = nil
}
