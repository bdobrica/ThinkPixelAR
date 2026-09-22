package agentd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
)

func captureFixture(t *testing.T, s OutputSanitizer) *Capture {
	t.Helper()
	l := protocol.HardLimits()
	l.BufferedEvents = 2
	l.BufferedBytes = 32
	l.DiagnosticBytes = 32
	l.EventBytes = 32
	l.LivenessWindowMs = 50
	c, err := newCapture("test-process", l, s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}
func TestCaptureFramesBeforeRedaction(t *testing.T) {
	c := captureFixture(t, func(_ OutputSource, b []byte) ([]byte, error) {
		return bytes.ReplaceAll(b, []byte("secret-canary"), []byte("[REDACTED]")), nil
	})
	w := &captureWriter{capture: c, source: OutputStdout}
	for _, part := range []string{"prefix secret-", "canary\n", "tail"} {
		if _, err := w.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	w.finish()
	if err := c.finish(); err != nil {
		t.Fatal(err)
	}
	a, err := c.Receive(context.Background())
	if err != nil || string(a.Data) != "prefix [REDACTED]" || a.Sequence != 1 || a.Source != OutputStdout {
		t.Fatal("frame/redaction failed")
	}
	b, err := c.Receive(context.Background())
	if err != nil || string(b.Data) != "tail" || b.Sequence != 2 {
		t.Fatal("EOF tail lost")
	}
	if _, err := c.Receive(context.Background()); err != io.EOF {
		t.Fatal("missing EOF")
	}
	if strings.Contains(fmt.Sprintf("%+v %#v %+v", a, a, c), "prefix") {
		t.Fatal("formatting exposed content")
	}
}
func TestCaptureBackpressureAndByteBound(t *testing.T) {
	c := captureFixture(t, nil)
	c.maxBytes = 10 // One redacted record; byte bound precedes count bound.
	if err := c.EmitEvent([]byte("a")); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- c.EmitEvent([]byte("b")) }()
	select {
	case <-result:
		t.Fatal("producer did not backpressure")
	case <-time.After(5 * time.Millisecond):
	}
	first, err := c.Receive(context.Background())
	if err != nil || string(first.Data) != "[REDACTED]" {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := c.EmitEvent([]byte("c")); err != ErrOutputBackpressure {
		t.Fatal("stall not terminal", err)
	}
	if _, err := c.Receive(context.Background()); err != ErrOutputBackpressure {
		t.Fatal("overflow silently lost")
	}
	if c.bytes != 0 || len(c.queue) != 0 {
		t.Fatal("failed queue retained data")
	}
}
func TestCaptureLimitsAndRedactionFailure(t *testing.T) {
	for _, mode := range []string{"oversize-line", "oversize-event", "expansion", "failure", "panic", "event-count"} {
		t.Run(mode, func(t *testing.T) {
			var s OutputSanitizer
			switch mode {
			case "expansion":
				s = func(OutputSource, []byte) ([]byte, error) { return make([]byte, 33), nil }
			case "failure":
				s = func(OutputSource, []byte) ([]byte, error) { return nil, errors.New("restricted-canary") }
			case "panic":
				s = func(OutputSource, []byte) ([]byte, error) { panic("restricted-canary") }
			}
			c := captureFixture(t, s)
			var err error
			switch mode {
			case "oversize-line":
				w := captureWriter{capture: c, source: OutputStderr}
				_, err = w.Write(bytes.Repeat([]byte{'x'}, 33))
			case "oversize-event":
				err = c.EmitEvent(make([]byte, 33))
			case "event-count":
				c.maxEvents = 1
				if e := c.EmitEvent(nil); e != nil {
					t.Fatal(e)
				}
				err = c.EmitEvent(nil)
			default:
				err = c.EmitEvent([]byte("safe"))
			}
			if err == nil || strings.Contains(err.Error(), "canary") {
				t.Fatal("unsafe failure", err)
			}
			if _, e := c.Receive(context.Background()); e != err {
				t.Fatal("failure not visible")
			}
		})
	}
}
func TestCaptureCloseUnblocksProducerAndReceiver(t *testing.T) {
	c := captureFixture(t, nil)
	c.maxEvents = 1
	c.wait = time.Second
	if err := c.EmitEvent(nil); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- c.EmitEvent(nil) }()
	c.Close()
	select {
	case err := <-result:
		if err != ErrOutputClosed {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer leaked")
	}
	empty := captureFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := empty.Receive(ctx); err != context.Canceled {
		t.Fatal("cancel ignored")
	}
}
