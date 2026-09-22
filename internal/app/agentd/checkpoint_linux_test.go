package agentd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func TestCheckpointPreparationWindow(t *testing.T) {
	p, id, _ := controlFixture(t, "ignore")
	roots := []string{"/state/test"}
	quiesced := false
	paths := []string{"test/session"}
	c, err := NewCheckpoints(p, roots, "test/v1", func(ctx context.Context, got primitives.ID, declared []string, ready func(StateManifest) error) error {
		if got != id || declared[0] != "/state/test" {
			t.Fatal("wrong binding")
		}
		declared[0] = "/state/changed"
		quiesced = true
		defer func() { quiesced = false }()
		return ready(StateManifest{"session-id", "test/v1", paths})
	})
	if err != nil {
		t.Fatal(err)
	}
	roots[0] = "/state/changed"
	for range 2 {
		err = c.Prepare(context.Background(), id, func(ctx context.Context, m StateManifest) error {
			if !quiesced || m.Paths[0] != "test/session" {
				t.Error("outside window")
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Error("unbounded window")
			}
			m.Paths[0] = "changed"
			if _, err := p.Restart(ctx, id); err != ErrProcessBusy {
				t.Error("unfenced preparation")
			}
			return nil
		})
		if err != nil || quiesced || paths[0] != "test/session" {
			t.Fatal("preparation failed", err)
		}
	}
	if err := c.Prepare(context.Background(), "stale", func(context.Context, StateManifest) error { t.Error("stale callback"); return nil }); err != ErrProcessStale {
		t.Fatal(err)
	}
}

func TestCheckpointRegistrationAndManifestBounds(t *testing.T) {
	p, _, _ := controlFixture(t, "ignore")
	hook := func(context.Context, primitives.ID, []string, func(StateManifest) error) error { return nil }
	for _, roots := range [][]string{nil, {"/state"}, {"/workspace"}, {"/state/../secrets"}, {"/state/a/"}, {"/state/a", "/state/a/b"}, {"/state/b", "/state/a"}, {"/state/a", "/state/a"}, {"/state/a\\b"}} {
		if _, err := NewCheckpoints(p, roots, "test/v1", hook); err != ErrCheckpoint {
			t.Fatal("accepted roots", roots)
		}
	}
	for _, paths := range [][]string{{"../secret"}, {"test/../secret"}, {"other/file"}, {"/state/test/file"}, {"test//file"}, {"test/a", "test/a/b"}, {"test/a", "test/a"}, {"test/b", "test/a"}, make([]string, 65)} {
		if stateManifest(StateManifest{"session", "test/v1", paths}, []string{"/state/test"}, "test/v1") {
			t.Fatal("accepted manifest", paths)
		}
	}
	if stateManifest(StateManifest{"session", "wrong", nil}, []string{"/state/test"}, "test/v1") {
		t.Fatal("accepted incompatible format")
	}
}

func TestCheckpointHookFailures(t *testing.T) {
	p, id, _ := controlFixture(t, "ignore")
	for _, mode := range []string{"missing", "twice", "invalid", "error", "panic", "consumer"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			c, err := NewCheckpoints(p, []string{"/state/test"}, "test/v1", func(_ context.Context, _ primitives.ID, _ []string, ready func(StateManifest) error) error {
				m := StateManifest{"session", "test/v1", []string{"test/session"}}
				switch mode {
				case "missing":
					return nil
				case "twice":
					_ = ready(m)
					return ready(m)
				case "invalid":
					m.Paths = []string{"../secret"}
					_ = ready(m)
					return nil
				case "error":
					return errors.New("restricted-canary")
				case "panic":
					panic("restricted-canary")
				default:
					return ready(m)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			err = c.Prepare(context.Background(), id, func(context.Context, StateManifest) error {
				calls++
				if mode == "consumer" {
					return errors.New("restricted-canary")
				}
				return nil
			})
			if err != ErrCheckpoint || calls > 1 {
				t.Fatal("unsafe result", err, calls)
			}
		})
	}
}

func TestCheckpointTimeoutFencesReplacement(t *testing.T) {
	p, id, _ := controlFixture(t, "ignore")
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	c, err := NewCheckpoints(p, []string{"/state/test"}, "test/v1", func(context.Context, primitives.ID, []string, func(StateManifest) error) error {
		close(entered)
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- c.Prepare(ctx, id, func(context.Context, StateManifest) error { t.Error("unexpected manifest"); return nil })
	}()
	<-entered
	if err := <-result; err != ErrProcessDeadline {
		t.Fatal(err)
	}
	if _, err := p.Start(context.Background()); err != ErrProcessBusy {
		t.Fatal("late hook not fenced", err)
	}
	select {
	case <-p.current.done:
	case <-time.After(3 * time.Second):
		t.Fatal("target left alive")
	}
}
