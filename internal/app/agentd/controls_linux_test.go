package agentd

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"golang.org/x/sys/unix"
)

func controlFixture(t *testing.T, mode string) (*Processes, primitives.ID, string) {
	t.Helper()
	p, root := processFixture(t, mode)
	p.commandBytes = 1024
	id, err := p.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(root, mode+"-ready"))
	return p, id, root
}
func TestControlsRegisteredSignalAndCooperativeInterrupt(t *testing.T) {
	p, id, root := controlFixture(t, "control")
	signals := map[string]SignalHandler{"test.notify": func(_ context.Context, got primitives.ID, payload []byte) error {
		if got != id || string(payload) != "ping" {
			t.Error("incorrect delivery")
		}
		payload[0] = 'x'
		return p.current.signal(unix.SIGUSR1)
	}}
	c, err := NewControls(p, 32, signals, func(ctx context.Context, got primitives.ID, reason InterruptReason) error {
		if _, ok := ctx.Deadline(); !ok || got != id || reason != InterruptCancel {
			t.Error("unbounded/wrong interrupt")
		}
		return p.current.signal(unix.SIGINT)
	})
	if err != nil {
		t.Fatal(err)
	}
	signals["test.notify"] = nil // Registration is copied, not caller-mutable.
	payload := []byte("ping")
	if err := c.Signal(context.Background(), id, "test.notify", payload); err != nil {
		t.Fatal(err)
	}
	if string(payload) != "ping" {
		t.Fatal("input aliased")
	}
	waitFile(t, filepath.Join(root, "notified"))
	if err := c.Interrupt(context.Background(), id, InterruptCancel); err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(root, "interrupted"))
	if p.Status().State != agentdv1.Heartbeat_RUNNING {
		t.Fatal("ack incorrectly treated as process exit")
	}
	next, err := p.Restart(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Interrupt(context.Background(), id, InterruptCancel); err != ErrProcessStale {
		t.Fatal("stale handler invoked")
	}
	if p.Status().ProcessID != next {
		t.Fatal("replacement affected")
	}
}
func TestControlsRejectBeforeDelivery(t *testing.T) {
	p, id, _ := controlFixture(t, "ignore")
	var calls atomic.Int32
	h := func(context.Context, primitives.ID, []byte) error { calls.Add(1); return nil }
	for _, name := range []string{"", "cancel", "interrupt", "BAD", "bad/name"} {
		if _, err := NewControls(p, 16, map[string]SignalHandler{name: h}, nil); err != ErrControl {
			t.Fatal("invalid registration")
		}
	}
	if _, err := NewControls(p, 1025, nil, nil); err != ErrControl {
		t.Fatal("bootstrap limit widened")
	}
	if _, err := NewControls(p, 16, map[string]SignalHandler{"ok": nil}, nil); err != ErrControl {
		t.Fatal("nil handler")
	}
	c, err := NewControls(p, 4, map[string]SignalHandler{"ok": h}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id   primitives.ID
		name string
		data []byte
		want error
	}{{id, "unknown", nil, ErrControl}, {id, "ok", make([]byte, 5), ErrControl}, {"stale", "ok", nil, ErrProcessStale}} {
		if err := c.Signal(context.Background(), test.id, test.name, test.data); err != test.want {
			t.Fatal("invalid request accepted", err)
		}
	}
	if err := c.Interrupt(context.Background(), id, 0); err != ErrControl {
		t.Fatal("unknown reason")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Signal(ctx, id, "ok", nil); err != ErrProcessDeadline {
		t.Fatal("canceled signal")
	}
	if calls.Load() != 0 {
		t.Fatal("rejected signal reached adapter")
	}
}
func TestInterruptFallbackStopsProcess(t *testing.T) {
	for _, mode := range []string{"unsupported", "error", "panic", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			p, id, _ := controlFixture(t, "ignore")
			var hook InterruptHandler
			switch mode {
			case "error":
				hook = func(context.Context, primitives.ID, InterruptReason) error { return errors.New("restricted-canary") }
			case "panic":
				hook = func(context.Context, primitives.ID, InterruptReason) error { panic("restricted-canary") }
			case "timeout":
				hook = func(ctx context.Context, _ primitives.ID, _ InterruptReason) error { <-ctx.Done(); return ctx.Err() }
			}
			c, err := NewControls(p, 16, nil, hook)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Interrupt(context.Background(), id, InterruptDeadline); err != ErrInterruptEscalated {
				t.Fatal("fallback not reported", err)
			}
			if s := p.Status(); s.State != agentdv1.Heartbeat_EXITED || s.Signal != 9 {
				t.Fatal("ignored TERM not escalated")
			}
		})
	}
}
func TestTimedOutSignalFencesReplacementUntilHandlerReturns(t *testing.T) {
	p, id, _ := controlFixture(t, "ignore")
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	c, err := NewControls(p, 16, map[string]SignalHandler{"test.wait": func(context.Context, primitives.ID, []byte) error { close(entered); <-release; return nil }}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- c.Signal(ctx, id, "test.wait", nil) }()
	<-entered
	if err := <-result; err != ErrProcessDeadline {
		t.Fatal("unbounded signal", err)
	}
	if _, err := p.Start(context.Background()); err != ErrProcessBusy {
		t.Fatal("pending callback did not fence replacement")
	}
	select {
	case <-p.current.done:
	case <-time.After(3 * time.Second):
		t.Fatal("late signal left target alive")
	}
}

func TestSignalHandlerFailuresAreSanitized(t *testing.T) {
	p, id, _ := controlFixture(t, "ignore")
	for _, panics := range []bool{false, true} {
		c, err := NewControls(p, 16, map[string]SignalHandler{"test.reject": func(context.Context, primitives.ID, []byte) error {
			if panics {
				panic("restricted-canary")
			}
			return errors.New("restricted-canary")
		}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Signal(context.Background(), id, "test.reject", nil); err != ErrControl {
			t.Fatal("unsafe signal failure", err)
		}
	}
	if p.Status().State != agentdv1.Heartbeat_RUNNING {
		t.Fatal("completed rejection stopped unrelated work")
	}
}
