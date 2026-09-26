package codex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"hash"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type eventItem struct {
	id                primitives.ID
	kind              string
	start, completion [32]byte
	done, suppressed  bool
	text              hash.Hash
	first, last       uint64
}

// validateVendorJSON checks the entire bounded frame, including discarded fields,
// for duplicate keys, excessive nesting, invalid UTF-8 and trailing values. It
// never includes a decoder error (which can contain source bytes) in an error.
func validateVendorJSON(raw []byte) error {
	if len(raw) == 0 || len(raw) > maxHandshakeBytes || !utf8.Valid(raw) {
		return harness.ErrProtocol
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	members := 0
	var value func(int) error
	value = func(depth int) error {
		if depth > 16 || members > 4096 {
			return harness.ErrProtocol
		}
		t, err := d.Token()
		if err != nil {
			return harness.ErrProtocol
		}
		switch t {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				name, ok := k.(string)
				if e != nil || !ok || seen[name] {
					return harness.ErrProtocol
				}
				seen[name] = true
				members++
				if e = value(depth + 1); e != nil {
					return e
				}
			}
			if end, e := d.Token(); e != nil || end != json.Delim('}') {
				return harness.ErrProtocol
			}
		case json.Delim('['):
			for d.More() {
				members++
				if e := value(depth + 1); e != nil {
					return e
				}
			}
			if end, e := d.Token(); e != nil || end != json.Delim(']') {
				return harness.ErrProtocol
			}
		default:
			if _, delim := t.(json.Delim); delim {
				return harness.ErrProtocol
			}
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return harness.ErrProtocol
	}
	return nil
}

func field(m map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(m[key], &s)
	return s
}
func keys(m map[string]json.RawMessage, allowed string) bool {
	for key := range m {
		if !slices.Contains(strings.Fields(allowed), key) {
			return false
		}
	}
	return true
}
func itemKey(id string) bool {
	// Vendor IDs are private map keys, never published AR identities or paths.
	if len(id) == 0 || len(id) > 256 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func (s *eventStream) normalize(ctx context.Context, raw []byte) (harness.HarnessEvent, error) {
	var none harness.HarnessEvent
	if validateVendorJSON(raw) != nil {
		return none, harness.ErrProtocol
	}
	f, err := object(raw)
	if err != nil || !keys(f, "method params emittedAtMs") {
		return none, harness.ErrProtocol
	} // Includes all server requests: never approve.
	if raw, ok := f["emittedAtMs"]; ok {
		var stamp int64
		if string(raw) == "null" || json.Unmarshal(raw, &stamp) != nil || stamp < 0 {
			return none, harness.ErrProtocol
		}
	}
	p, err := object(f["params"])
	if err != nil {
		return none, harness.ErrProtocol
	}
	method := field(f, "method")
	if id, exists := p["threadId"]; exists && (string(id) == "null" || field(p, "threadId") != s.client.threadID) {
		return none, harness.ErrStreamIntegrity
	}
	if _, exists := p["turnId"]; exists && field(p, "turnId") != s.client.turnID {
		return none, harness.ErrStreamIntegrity
	}
	switch method {
	case "account/rateLimits/updated", "remoteControl/status/changed", "configWarning", "warning", "skills/changed", "mcpServer/startupStatus/updated", "thread/status/changed":
		return none, nil // No diagnostics or vendor status becomes authority.
	case "turn/started", "turn/completed":
		if !keys(p, "threadId turn") || field(p, "threadId") != s.client.threadID {
			return none, harness.ErrProtocol
		}
		turn, e := object(p["turn"])
		if e != nil || field(turn, "id") != s.client.turnID {
			return none, harness.ErrStreamIntegrity
		}
		if method == "turn/started" {
			if s.started || field(turn, "status") != "inProgress" {
				return none, harness.ErrStreamIntegrity
			}
			s.started = true
			return s.emit(harness.ExecutionStarted, harness.ObservationPayload{})
		}
		if !s.started {
			return none, harness.ErrStreamIntegrity
		}
		switch field(turn, "status") {
		case "completed", "interrupted", "failed":
		default:
			return none, harness.ErrProtocol
		}
		for _, item := range s.items {
			if !item.done {
				return none, harness.ErrStreamIntegrity
			}
		}
		// Stream boundary only. Completion/usage/reference mapping is CDX-008;
		// EOF cannot establish Execution success, including after a failed turn.
		s.end = true
		return none, nil
	}
	if !s.started || field(p, "threadId") != s.client.threadID {
		return none, harness.ErrStreamIntegrity
	}
	// Usage is a thread-scoped observation, and remains CDX-008.
	if method == "thread/tokenUsage/updated" {
		return none, nil
	}
	if field(p, "turnId") != s.client.turnID {
		return none, harness.ErrStreamIntegrity
	}
	switch method {
	case "item/reasoning/textDelta", "item/reasoning/summaryTextDelta", "item/reasoning/summaryPartAdded",
		"item/plan/delta", "turn/plan/updated", "turn/diff/updated", "item/fileChange/outputDelta":
		return none, nil // No content policy, ID allocation or output for private data.
	case "item/started", "item/completed":
		return s.item(ctx, p, method == "item/completed")
	case "item/agentMessage/delta", "item/commandExecution/outputDelta":
		if !keys(p, "threadId turnId itemId delta") {
			return none, harness.ErrProtocol
		}
		item := s.items[field(p, "itemId")]
		if item == nil || item.done {
			return none, harness.ErrStreamIntegrity
		}
		var text string
		if string(p["delta"]) == "null" || json.Unmarshal(p["delta"], &text) != nil || !eventText(text) {
			return none, harness.ErrProtocol
		}
		if method == "item/agentMessage/delta" {
			if item.kind != "agentMessage" {
				return none, harness.ErrStreamIntegrity
			}
			_, _ = item.text.Write([]byte(text))
			if s.policy == nil {
				item.suppressed = true
				return none, nil
			}
			safe, e := s.policy.MessageText(ctx, item.id, text)
			if e != nil || !eventText(safe) {
				return none, harness.ErrProtocol
			}
			if safe == "" {
				item.suppressed = true
				return none, nil
			}
			event, e := s.emit(harness.MessageDelta, harness.MessageDeltaPayload{MessageID: item.id, Text: safe})
			if e == nil {
				if item.first == 0 {
					item.first = event.Sequence
				}
				item.last = event.Sequence
			}
			return event, e
		}
		if item.kind != "commandExecution" {
			return none, harness.ErrStreamIntegrity
		}
		if s.policy == nil {
			return none, nil
		}
		ref, e := s.policy.ProcessOutput(ctx, item.id, text)
		if e != nil {
			return none, harness.ErrProtocol
		}
		if ref == "" {
			return none, nil
		}
		if runtimeevent.ValidateReference(ref) != nil {
			return none, harness.ErrProtocol
		}
		return s.emit(harness.ProcessOutput, harness.ProcessPayload{ProcessID: item.id, OutputReference: ref})
	case "item/mcpToolCall/progress":
		item := s.items[field(p, "itemId")]
		if item == nil || item.done || item.kind != "mcpToolCall" {
			return none, harness.ErrStreamIntegrity
		}
		return none, nil // Progress text is not tool status, nor safe diagnostics.
	default:
		return none, harness.ErrProtocol
	}
}

func (s *eventStream) item(_ context.Context, p map[string]json.RawMessage, completed bool) (harness.HarnessEvent, error) {
	var none harness.HarnessEvent
	stamp := "startedAtMs"
	if completed {
		stamp = "completedAtMs"
	}
	var at int64
	if !keys(p, "threadId turnId item "+stamp) || string(p[stamp]) == "null" || json.Unmarshal(p[stamp], &at) != nil || at < 0 {
		return none, harness.ErrProtocol
	}
	v, e := object(p["item"])
	if e != nil || !itemKey(field(v, "id")) {
		return none, harness.ErrProtocol
	}
	kind := field(v, "type")
	var capability harness.HarnessCapability
	switch kind {
	case "reasoning", "userMessage", "hookPrompt", "plan", "contextCompaction":
		return none, nil // Discard before allocating any normalized identity.
	case "agentMessage":
		if !keys(v, "id type text phase delivery memoryCitation questions") {
			return none, harness.ErrProtocol
		}
		phase := field(v, "phase")
		if phase != "" && phase != "commentary" && phase != "final_answer" {
			return none, harness.ErrProtocol
		}
		capability = harness.StructuredEvents
	case "commandExecution":
		if !keys(v, "id type command cwd commandActions aggregatedOutput exitCode durationMs processId status source pluginId scriptPath") {
			return none, harness.ErrProtocol
		}
		capability = harness.StructuredProcessEvents
	case "mcpToolCall":
		if !keys(v, "id type server tool arguments status result error durationMs appContext mcpAppResourceUri pluginId readOnlyHint") {
			return none, harness.ErrProtocol
		}
		capability = harness.StructuredToolEvents
	case "fileChange":
		if !keys(v, "id type changes status") {
			return none, harness.ErrProtocol
		}
		capability = harness.StructuredToolEvents
	case "webSearch":
		if !keys(v, "id type query action results") {
			return none, harness.ErrProtocol
		}
		capability = harness.StructuredToolEvents
	default:
		return none, harness.ErrUnsupported
	}
	if s.capabilities.Require(capability) != nil {
		return none, harness.ErrUnsupported
	}
	id := field(v, "id")
	sum := sha256.Sum256(p["item"])
	item := s.items[id]
	if !completed {
		if item != nil {
			if !item.done && item.start == sum {
				return none, nil
			}
			return none, harness.ErrStreamIntegrity
		}
		if len(s.items) >= 1024 {
			return none, harness.ErrStreamIntegrity
		}
		arid, e := primitives.NewID(time.Now())
		if e != nil {
			return none, harness.ErrOutcomeUnknown
		}
		item = &eventItem{id: arid, kind: kind, start: sum}
		s.items[id] = item
		if kind == "agentMessage" {
			if string(v["text"]) != `""` {
				return none, harness.ErrStreamIntegrity
			}
			item.text = sha256.New()
			return none, nil
		}
		if kind != "webSearch" && field(v, "status") != "inProgress" {
			return none, harness.ErrStreamIntegrity
		}
		if kind == "commandExecution" {
			return s.emit(harness.ProcessStarted, harness.ProcessPayload{ProcessID: arid})
		}
		return s.emit(harness.ToolStarted, harness.ToolPayload{ToolCallID: arid})
	}
	if item == nil || item.kind != kind {
		return none, harness.ErrStreamIntegrity
	}
	if item.done {
		if item.completion == sum {
			return none, nil
		}
		return none, harness.ErrStreamIntegrity
	}
	item.done = true
	item.completion = sum
	if kind == "agentMessage" {
		var text string
		if string(v["text"]) == "null" || json.Unmarshal(v["text"], &text) != nil || !eventText(text) {
			return none, harness.ErrProtocol
		}
		digest := sha256.Sum256([]byte(text))
		if !bytes.Equal(digest[:], item.text.Sum(nil)) {
			return none, harness.ErrStreamIntegrity
		}
		if item.suppressed || item.first == 0 {
			return none, nil
		}
		return s.emit(harness.MessageCompleted, harness.MessageCompletedPayload{MessageID: item.id, FirstSequence: item.first, LastSequence: item.last})
	}
	status := field(v, "status")
	if kind == "commandExecution" {
		if status != "completed" && status != "failed" && status != "declined" {
			return none, harness.ErrProtocol
		}
		var exit *int32
		if v["exitCode"] != nil && json.Unmarshal(v["exitCode"], &exit) != nil {
			return none, harness.ErrProtocol
		}
		return s.emit(harness.ProcessCompleted, harness.ProcessPayload{ProcessID: item.id, ExitCode: exit})
	}
	name := harness.ToolCompleted
	if kind != "webSearch" {
		switch status {
		case "completed":
		case "failed", "declined":
			if status == "declined" && kind == "mcpToolCall" {
				return none, harness.ErrProtocol
			}
			name = harness.ToolFailed
		default:
			return none, harness.ErrProtocol
		}
	}
	return s.emit(name, harness.ToolPayload{ToolCallID: item.id})
}
