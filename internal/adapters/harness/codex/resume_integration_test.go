package codex

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
)

func TestPinnedThreadResume(t *testing.T) {
	const remembered = "resume-fixture-marker-7351"
	var calls atomic.Int32
	var historySeen atomic.Bool
	home, launch, ctx := pinnedThreadFactory(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if calls.Add(1) == 2 {
			historySeen.Store(strings.Contains(string(body), remembered))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		item := map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": remembered, "annotations": []any{}}}}
		for _, event := range []map[string]any{
			{"type": "response.created", "response": map[string]any{"id": "resp_fixture", "status": "in_progress", "output": []any{}}},
			{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}},
			{"type": "response.content_part.added", "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}},
			{"type": "response.output_text.delta", "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "delta": remembered},
			{"type": "response.output_text.done", "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "text": remembered},
			{"type": "response.output_item.done", "output_index": 0, "item": item},
			{"type": "response.completed", "response": map[string]any{"id": "resp_fixture", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 5, "output_tokens": 2, "total_tokens": 7}}},
		} {
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
		}
	})
	complete := func(c *Client, prompt string) {
		t.Helper()
		if _, err := c.StartTurn(ctx, turnOperation, turnInput, prompt); err != nil {
			t.Fatal(err)
		}
		h, op, caps, limits := eventOptions()
		h.VendorSessionReference = c.threadID
		stream, err := c.Events(h, op, caps, limits, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		completed := false
		for {
			e, err := stream.Next(ctx)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if e.Type == harness.ExecutionCompletionObserved {
				completed = true
			}
		}
		if !completed {
			t.Fatal("turn did not complete")
		}
	}
	first, stop := launch(home)
	id, err := first.StartThread(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	complete(first, "Return a fixture word. Do not run tools.")
	stop() // The original process is reaped before fresh compute uses its state.
	second, stopSecond := launch(home)
	resumed, err := second.ResumeThread(ctx, home, id)
	if err != nil || resumed != id {
		t.Fatal("resume identity", err)
	}
	complete(second, "Continue the previous conversation. Do not run tools.")
	stopSecond()
	if calls.Load() != 2 || !historySeen.Load() {
		t.Fatal("prior conversation absent from resumed model request")
	}
	missing, _ := launch(t.TempDir())
	if _, err := missing.ResumeThread(ctx, home, id); err == nil {
		t.Fatal("missing state accepted")
	}
	t.Log("fresh pinned process resumed the same thread and sent prior context to the loopback model fixture; absent state rejected")
}
