package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimebinding"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func eventOptions() (harness.HarnessHandle, runtimebinding.OperationIdentity, harness.Capabilities, harness.AdapterLimits) {
	id := turnInput
	digest := "sha256:" + strings.Repeat("a", 64)
	return harness.HarnessHandle{ID: id, ProcessInstanceID: id, Fence: harness.Fence{TenantID: id, SessionID: id, ExecutionID: id, AttemptID: id, SandboxBindingID: id, Generation: 1, AttemptOrdinal: 1}, AdapterKind: Kind, AdapterBuildDigest: digest, NegotiationDigest: digest, VendorSessionReference: testThreadID}, runtimebinding.OperationIdentity{ID: turnOperation, RequestDigest: digest}, harness.Capabilities{harness.StructuredEvents: harness.Supported, harness.Streaming: harness.Supported, harness.UsageObservation: harness.Supported, harness.StructuredToolEvents: harness.Supported, harness.StructuredProcessEvents: harness.Supported}, harness.AdapterLimits{EventBytes: 4096, EventsPerSecond: 1000, BufferedEvents: 1}
}

// Fixture policy is deliberately limited to fixture content; not production
// secret detection. Production policy must account for fragment boundaries.
type fixtureEventPolicy struct{}

func (fixtureEventPolicy) MessageText(_ context.Context, _ primitives.ID, text string) (string, error) {
	if strings.Contains(text, "secret-canary") {
		return "[redacted]", nil
	}
	return text, nil
}
func (fixtureEventPolicy) ProcessOutput(_ context.Context, _ primitives.ID, _ string) (string, error) {
	return "artifact:protected-fixture", nil
}

func notification(method string, p map[string]any) string {
	b, _ := json.Marshal(map[string]any{"method": method, "params": p})
	return string(b) + "\n"
}
func turnNotification(method, status string) string {
	return notification(method, map[string]any{"threadId": testThreadID, "turn": map[string]any{"id": testTurnID, "status": status, "items": []any{}, "error": nil}})
}
func itemNotification(kind, id string, completed bool, extra map[string]any) string {
	v := map[string]any{"type": kind, "id": id}
	for k, x := range extra {
		v[k] = x
	}
	method, stamp := "item/started", "startedAtMs"
	if completed {
		method, stamp = "item/completed", "completedAtMs"
	}
	return notification(method, map[string]any{"threadId": testThreadID, "turnId": testTurnID, "item": v, stamp: 1})
}
func deltaNotification(method, id, text string) string {
	return notification(method, map[string]any{"threadId": testThreadID, "turnId": testTurnID, "itemId": id, "delta": text})
}
func fixtureEvents(t *testing.T, wire string, policy EventPolicy) (*eventStream, func()) {
	t.Helper()
	in, input := io.Pipe()
	output, out := io.Pipe()
	c := NewClient(input, output)
	c.threadID, c.turnID, c.turnOperation = testThreadID, testTurnID, string(turnOperation)
	h, op, caps, limits := eventOptions()
	stream, e := c.Events(h, op, caps, limits, policy)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { defer close(done); defer out.Close(); _, _ = io.WriteString(out, wire) }()
	cleanup := func() { _ = stream.Close(); c.Close(); _ = in.Close(); <-done }
	t.Cleanup(cleanup)
	return stream.(*eventStream), cleanup
}

func readEvents(t *testing.T, s harness.HarnessEventStream) []harness.HarnessEvent {
	t.Helper()
	var events []harness.HarnessEvent
	for {
		e, err := s.Next(t.Context())
		if err == io.EOF {
			return events
		}
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
}

func TestEventMapping(t *testing.T) {
	processStart := itemNotification("commandExecution", "cmd", false, map[string]any{"status": "inProgress", "command": "secret-canary", "cwd": "/private/secret-canary", "commandActions": []any{}})
	processEnd := itemNotification("commandExecution", "cmd", true, map[string]any{"status": "completed", "exitCode": 0, "aggregatedOutput": "secret-canary"})
	wire := turnNotification("turn/started", "inProgress") +
		itemNotification("reasoning", "reason", false, map[string]any{"summary": []string{"reasoning-canary"}}) +
		deltaNotification("item/reasoning/textDelta", "reason", "reasoning-canary") +
		itemNotification("agentMessage", "msg", false, map[string]any{"text": ""}) +
		deltaNotification("item/agentMessage/delta", "msg", "hello ") +
		deltaNotification("item/agentMessage/delta", "msg", "secret-canary") +
		itemNotification("agentMessage", "msg", true, map[string]any{"text": "hello secret-canary", "phase": "final_answer"}) +
		processStart + processStart + // exact lifecycle replay does not allocate again
		deltaNotification("item/commandExecution/outputDelta", "cmd", "secret-canary") +
		processEnd + processEnd +
		itemNotification("mcpToolCall", "tool", false, map[string]any{"status": "inProgress", "arguments": map[string]string{"token": "secret-canary"}}) +
		itemNotification("mcpToolCall", "tool", true, map[string]any{"status": "failed", "error": map[string]string{"message": "secret-canary"}}) +
		itemNotification("fileChange", "patch", false, map[string]any{"status": "inProgress", "changes": []any{}}) +
		itemNotification("fileChange", "patch", true, map[string]any{"status": "completed", "changes": []any{}}) +
		itemNotification("webSearch", "web", false, map[string]any{"query": "secret-canary"}) +
		itemNotification("webSearch", "web", true, map[string]any{"query": "secret-canary", "results": []any{}}) +
		turnNotification("turn/completed", "completed")
	s, _ := fixtureEvents(t, wire, fixtureEventPolicy{})
	events := readEvents(t, s)
	want := []string{harness.ExecutionStarted, harness.MessageDelta, harness.MessageDelta, harness.MessageCompleted, harness.ProcessStarted, harness.ProcessOutput, harness.ProcessCompleted, harness.ToolStarted, harness.ToolFailed, harness.ToolStarted, harness.ToolCompleted, harness.ToolStarted, harness.ToolCompleted, harness.ExecutionCompletionObserved}
	if len(events) != len(want) {
		t.Fatalf("events: %d", len(events))
	}
	ids := map[primitives.ID]bool{}
	for i, e := range events {
		if e.Type != want[i] || e.Sequence != uint64(i+1) || e.StreamID != s.id || e.Handle != s.handle || e.Operation != s.operation || e.Content.Classification != runtimeevent.Confidential || ids[e.EventID] {
			t.Fatal("correlation/order/classification")
		}
		ids[e.EventID] = true
		if strings.Contains(string(e.Content.Inline), "canary") || e.VendorEventID != "" {
			t.Fatal("private content escaped")
		}
		if _, err := harness.CheckEventDeclaration(e, s.capabilities); err != nil {
			t.Fatal(err)
		}
	}
	var delta harness.MessageDeltaPayload
	var completed harness.MessageCompletedPayload
	_ = json.Unmarshal(events[1].Content.Inline, &delta)
	_ = json.Unmarshal(events[3].Content.Inline, &completed)
	if delta.MessageID != completed.MessageID || completed.FirstSequence != 2 || completed.LastSequence != 3 {
		t.Fatal("message identity/range")
	}
	var start, end harness.ProcessPayload
	_ = json.Unmarshal(events[4].Content.Inline, &start)
	_ = json.Unmarshal(events[6].Content.Inline, &end)
	if start.ProcessID != end.ProcessID || end.ExitCode == nil || *end.ExitCode != 0 {
		t.Fatal("process identity/exit")
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", s, s), "canary") {
		t.Fatal("stream formatting leak")
	}
}

func TestEventsSuppressWithoutPolicy(t *testing.T) {
	wire := turnNotification("turn/started", "inProgress") + itemNotification("agentMessage", "m", false, map[string]any{"text": ""}) + deltaNotification("item/agentMessage/delta", "m", "secret-canary") + itemNotification("agentMessage", "m", true, map[string]any{"text": "secret-canary"}) + turnNotification("turn/completed", "completed")
	s, _ := fixtureEvents(t, wire, nil)
	if got := readEvents(t, s); len(got) != 2 || got[1].Type != harness.ExecutionCompletionObserved || got[0].Type != harness.ExecutionStarted {
		t.Fatal("default content suppression")
	}
}

func TestEventsRejectBrokenStreams(t *testing.T) {
	start := turnNotification("turn/started", "inProgress")
	msg := itemNotification("agentMessage", "m", false, map[string]any{"text": ""})
	for name, wire := range map[string]string{
		"missing start":     deltaNotification("item/agentMessage/delta", "m", "text"),
		"missing item":      start + deltaNotification("item/agentMessage/delta", "m", "text"),
		"wrong turn":        start + strings.Replace(msg, testTurnID, testThreadID, 1),
		"wrong thread":      strings.Replace(start, testThreadID, testTurnID, 1),
		"unknown method":    start + notification("item/new", map[string]any{"threadId": testThreadID, "turnId": testTurnID}),
		"approval":          `{"id":8,"method":"item/commandExecution/requestApproval","params":{}}` + "\n",
		"duplicate key":     strings.Replace(start, `"method":`, `"method":"secret-canary","method":`, 1),
		"nested duplicate":  start + strings.Replace(msg, `"text":""`, `"text":"","text":"secret-canary"`, 1),
		"unknown field":     start + strings.Replace(msg, `"text":""`, `"text":"","secret":"canary"`, 1),
		"missing delta":     start + msg + itemNotification("agentMessage", "m", true, map[string]any{"text": "unobserved"}),
		"unfinished item":   start + msg + turnNotification("turn/completed", "completed"),
		"truncated":         start + `{"method":`,
		"oversized":         strings.Repeat("x", maxHandshakeBytes+1),
		"invalid utf8":      start + msg + deltaNotification("item/agentMessage/delta", "m", "x")[:10] + "\xff\n",
		"control":           start + msg + deltaNotification("item/agentMessage/delta", "m", "\x00"),
		"deep":              start + `{"method":"warning","params":{"x":` + strings.Repeat("[", 18) + "0" + strings.Repeat("]", 18) + "}}\n",
		"conflicting start": start + msg + itemNotification("agentMessage", "m", false, map[string]any{"text": "different"}),
		"item type switch":  start + msg + itemNotification("commandExecution", "m", true, map[string]any{"status": "completed"}),
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := fixtureEvents(t, wire, fixtureEventPolicy{})
			for i := 0; i < 10; i++ {
				_, err := s.Next(t.Context())
				if err != nil {
					if err == io.EOF || strings.Contains(err.Error(), "canary") {
						t.Fatal("unsafe outcome", err)
					}
					if _, again := s.Next(t.Context()); again != err {
						t.Fatal("failure not sticky")
					}
					return
				}
			}
			t.Fatal("stream did not fail")
		})
	}
}

func TestEventsBoundsAndCancellation(t *testing.T) {
	s, _ := fixtureEvents(t, turnNotification("turn/started", "inProgress")+itemNotification("commandExecution", "c", false, map[string]any{"status": "inProgress"}), nil)
	delete(s.capabilities, harness.StructuredProcessEvents)
	if _, e := s.Next(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Next(t.Context()); e != harness.ErrUnsupported {
		t.Fatal(e)
	}
	s2, _ := fixtureEvents(t, turnNotification("turn/started", "inProgress"), nil)
	s2.sequence = ^uint64(0)
	if _, e := s2.Next(t.Context()); e != harness.ErrStreamIntegrity {
		t.Fatal(e)
	}
	s3, _ := fixtureEvents(t, turnNotification("turn/started", "inProgress")+itemNotification("agentMessage", "m", false, map[string]any{"text": ""})+deltaNotification("item/agentMessage/delta", "m", strings.Repeat("x", 5000)), fixtureEventPolicy{})
	if _, e := s3.Next(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e := s3.Next(t.Context()); e != harness.ErrStreamIntegrity {
		t.Fatal(e)
	}
	// A blocked pipe read is interrupted by cancellation/Close, not a polling
	// goroutine or an unbounded read-ahead queue.
	for _, closeStream := range []bool{false, true} {
		input, in := io.Pipe()
		out, output := io.Pipe()
		c := NewClient(in, out)
		c.threadID, c.turnID, c.turnOperation = testThreadID, testTurnID, string(turnOperation)
		h, op, caps, limits := eventOptions()
		stream, e := c.Events(h, op, caps, limits, nil)
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { _, err := stream.Next(ctx); done <- err }()
		if closeStream {
			_ = stream.Close()
		} else {
			cancel()
		}
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("blocked read succeeded")
			}
		case <-time.After(time.Second):
			t.Fatal("blocked read leaked")
		}
		cancel()
		c.Close()
		_ = input.Close()
		_ = output.Close()
	}
}

func TestEventsBindingAndSubscription(t *testing.T) {
	in, input := io.Pipe()
	output, out := io.Pipe()
	defer in.Close()
	defer out.Close()
	c := NewClient(input, output)
	defer c.Close()
	c.threadID, c.turnID, c.turnOperation = testThreadID, testTurnID, string(turnOperation)
	h, op, caps, limits := eventOptions()
	wrong := h
	wrong.VendorSessionReference = testTurnID
	if _, err := c.Events(wrong, op, caps, limits, nil); err != harness.ErrInvalid {
		t.Fatal("wrong thread admitted", err)
	}
	wrong = h
	wrong.Fence.AttemptID = "invalid"
	if _, err := c.Events(wrong, op, caps, limits, nil); err != harness.ErrInvalid {
		t.Fatal("invalid fence admitted", err)
	}
	if _, err := c.Events(h, op, harness.Capabilities{}, limits, nil); err != harness.ErrUnsupported {
		t.Fatal("missing capability admitted", err)
	}
	stream, err := c.Events(h, op, caps, limits, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Callers cannot mutate an open stream's negotiated capability snapshot.
	delete(caps, harness.StructuredEvents)
	if !stream.(*eventStream).capabilities.Supports(harness.StructuredEvents) {
		t.Fatal("capabilities aliased")
	}
	if _, err := c.Events(h, op, caps, limits, nil); err != harness.ErrConflict {
		t.Fatal("second subscription admitted", err)
	}
	_ = stream.Close()
	if _, err := stream.Next(t.Context()); err != io.EOF {
		t.Fatal(err)
	}
}
