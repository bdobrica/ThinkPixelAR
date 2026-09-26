package codex

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
)

// Real App Server protocol, deterministic loopback Responses SSE. No provider
// credentials, outbound provider call, or claim of governed model qualification.
func TestPinnedTurnEvents(t *testing.T) {
	var calls atomic.Int32
	c, ctx := pinnedTurnClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		item := map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "fixture response", "annotations": []any{}}}}
		for _, event := range []map[string]any{
			{"type": "response.created", "response": map[string]any{"id": "resp_fixture", "status": "in_progress", "output": []any{}}},
			{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}},
			{"type": "response.content_part.added", "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}},
			{"type": "response.output_text.delta", "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "delta": "fixture response"},
			{"type": "response.output_text.done", "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "text": "fixture response"},
			{"type": "response.output_item.done", "output_index": 0, "item": item},
			{"type": "response.completed", "response": map[string]any{"id": "resp_fixture", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 5, "output_tokens": 2, "total_tokens": 7}}},
		} {
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
		}
	})
	if _, err := c.StartTurn(ctx, turnOperation, turnInput, "Return one word; do not run tools."); err != nil {
		t.Fatal(err)
	}
	h, op, caps, limits := eventOptions()
	h.VendorSessionReference = c.threadID
	stream, err := c.Events(h, op, caps, limits, fixtureEventPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var kinds []string
	var text string
	var usage harness.UsagePayload
	var completion harness.ObservationPayload
	for {
		e, err := stream.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("real stream after %v: %v", kinds, err)
		}
		kinds = append(kinds, e.Type)
		if e.Type == harness.UsageObserved {
			_ = json.Unmarshal(e.Content.Inline, &usage)
		}
		if e.Type == harness.ExecutionCompletionObserved {
			_ = json.Unmarshal(e.Content.Inline, &completion)
		}
		if e.Type == harness.MessageDelta {
			var p harness.MessageDeltaPayload
			_ = json.Unmarshal(e.Content.Inline, &p)
			text += p.Text
		}
	}
	if calls.Load() != 1 || text != "fixture response" || usage.InputTokens != 5 || usage.OutputTokens != 2 || completion.ReasonCode != "completed" || len(kinds) != 5 || kinds[0] != harness.ExecutionStarted || kinds[1] != harness.MessageDelta || kinds[2] != harness.MessageCompleted || kinds[3] != harness.UsageObserved || kinds[4] != harness.ExecutionCompletionObserved {
		t.Fatalf("unexpected mapped stream: calls=%d types=%v", calls.Load(), kinds)
	}
	t.Log("real pinned App Server emitted ordered message candidates from local Responses SSE")
}

func TestPinnedTurnFailure(t *testing.T) {
	c, ctx := pinnedTurnClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "secret-canary", http.StatusBadRequest)
	})
	if _, err := c.StartTurn(ctx, turnOperation, turnInput, "Return one word."); err != nil {
		t.Fatal(err)
	}
	h, op, caps, limits := eventOptions()
	h.VendorSessionReference = c.threadID
	stream, err := c.Events(h, op, caps, limits, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var kinds []string
	for {
		e, err := stream.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("real failure stream after %v: %v", kinds, err)
		}
		kinds = append(kinds, e.Type)
		if e.Type == harness.ExecutionFailureObserved && string(e.Content.Inline) != `{"reason_code":"failed"}` {
			t.Fatal("unexpected failure payload")
		}
	}
	if len(kinds) != 2 || kinds[0] != harness.ExecutionStarted || kinds[1] != harness.ExecutionFailureObserved {
		t.Fatal(kinds)
	}
}

func TestPinnedTurnInterrupt(t *testing.T) {
	requested := make(chan struct{})
	release := make(chan struct{})
	c, ctx := pinnedTurnClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_interrupt\",\"status\":\"in_progress\",\"output\":[]}}\n\n")
		w.(http.Flusher).Flush()
		close(requested)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	defer close(release)
	if _, err := c.StartTurn(ctx, turnOperation, turnInput, "Wait for the model response."); err != nil {
		t.Fatal(err)
	}
	select {
	case <-requested:
	case <-ctx.Done():
		t.Fatal("model request not observed")
	}
	h, op, caps, limits := eventOptions()
	h.VendorSessionReference = c.threadID
	s, err := c.Events(h, op, caps, limits, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, err := s.Next(ctx)
	if err != nil || first.Type != harness.ExecutionStarted {
		t.Fatal("missing start", err)
	}
	done := make(chan error, 1)
	go func() { done <- c.InterruptTurn(ctx) }()
	var terminal bool
	for {
		e, err := s.Next(ctx)
		if err == harness.ErrConflict {
			time.Sleep(time.Millisecond)
			continue
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if e.Type == harness.ExecutionCompletionObserved {
			terminal = string(e.Content.Inline) == `{"reason_code":"interrupted"}`
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !terminal {
		t.Fatal("no real interruption observation")
	}
	if err := c.InterruptTurn(ctx); err != nil {
		t.Fatal("interrupt replay", err)
	}
}
