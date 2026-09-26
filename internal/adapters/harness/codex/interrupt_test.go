package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
)

func interruptFixture(t *testing.T, wire string) *Client {
	t.Helper()
	in, input := io.Pipe()
	output, out := io.Pipe()
	c := NewClient(input, output)
	c.threadID, c.turnID, c.turnOperation = testThreadID, testTurnID, string(turnOperation)
	c.interruptTarget.Store(&turnTarget{testThreadID, testTurnID})
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer out.Close()
		raw, err := bufio.NewReader(in).ReadBytes('\n')
		if err != nil {
			return
		}
		var req struct {
			ID     int
			Method string
			Params map[string]string
		}
		if json.Unmarshal(raw, &req) != nil || req.ID != 4 || req.Method != "turn/interrupt" || len(req.Params) != 2 || req.Params["threadId"] != testThreadID || req.Params["turnId"] != testTurnID {
			t.Error("incorrect interrupt target")
		}
		_, _ = io.WriteString(out, wire)
	}()
	t.Cleanup(func() { c.Close(); _ = in.Close(); <-done })
	return c
}

func TestInterruptRetainsEventsAndReplays(t *testing.T) {
	for _, ackFirst := range []bool{false, true} {
		for _, streamFirst := range []bool{false, true} {
			t.Run(map[bool]string{false: "response reader", true: "stream reader"}[streamFirst], func(t *testing.T) {
				wire := turnNotification("turn/started", "inProgress") + turnNotification("turn/completed", "interrupted") + "{\"id\":4,\"result\":{}}\n"
				if ackFirst {
					wire = "{\"id\":4,\"result\":{}}\n" + turnNotification("turn/started", "inProgress") + turnNotification("turn/completed", "interrupted")
				}
				c := interruptFixture(t, wire)
				h, op, caps, limits := eventOptions()
				s, err := c.Events(h, op, caps, limits, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				done := make(chan error, 1)
				if streamFirst {
					go func() {
						for {
							_, e := s.Next(t.Context())
							if e == harness.ErrConflict {
								time.Sleep(time.Millisecond)
								continue
							}
							if e != nil {
								done <- e
								return
							}
						}
					}()

					deadline := time.Now().Add(time.Second)
					for !s.(*eventStream).reading.Load() {
						if time.Now().After(deadline) {
							t.Fatal("stream did not start reading")
						}
						time.Sleep(time.Millisecond)
					}
				}
				if err := c.InterruptTurn(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := c.InterruptTurn(t.Context()); err != nil {
					t.Fatal("replay", err)
				}
				if streamFirst {
					if err := <-done; err != io.EOF {
						t.Fatal(err)
					}
				} else {
					es := readEvents(t, s)
					if len(es) != 2 || es[1].Type != harness.ExecutionCompletionObserved || string(es[1].Content.Inline) != `{"reason_code":"interrupted"}` {
						t.Fatal("lost terminal notification")
					}
				}
			})
		}
	}

}

func TestInterruptInvalidResponses(t *testing.T) {
	for _, wire := range []string{
		"{\"id\":5,\"result\":{}}\n",
		"{\"id\":4,\"error\":{\"message\":\"secret-canary\"}}\n",
		"{\"id\":4,\"result\":{\"extra\":true}}\n",
		"{\"id\":4,\"id\":4,\"result\":{}}\n",
		"",
		strings.Repeat(notification("warning", map[string]any{}), 33),
	} {
		c := interruptFixture(t, wire)
		err := c.InterruptTurn(t.Context())
		if err == nil || strings.Contains(err.Error(), "secret-canary") {
			t.Fatal("invalid response accepted/leaked")
		}
		if again := c.InterruptTurn(t.Context()); again != err {
			t.Fatal("ambiguous request replayed")
		}
	}
}

func TestInterruptDeadlineAndMissingTurn(t *testing.T) {
	in, input := io.Pipe()
	output, out := io.Pipe()
	defer in.Close()
	defer out.Close()
	c := NewClient(input, output)
	defer c.Close()
	if err := c.InterruptTurn(t.Context()); err != harness.ErrInvalid {
		t.Fatal(err)
	}
	c.interruptTarget.Store(&turnTarget{testThreadID, testTurnID})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := c.InterruptTurn(ctx); err != harness.ErrOutcomeUnknown {
		t.Fatal(err)
	}
}
