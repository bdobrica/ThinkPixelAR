package codex

import (
	"context"
	"encoding/json"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
)

// The pinned notification contains thread-cumulative total and last-model-call
// usage. Keep only the latest total, never sum snapshots or infer missing usage.
// This is an observation, not a bill, reservation release or AG settlement.
func (s *eventStream) captureUsage(p map[string]json.RawMessage) error {
	if s.capabilities.Require(harness.UsageObservation) != nil {
		return harness.ErrUnsupported
	}
	if !keys(p, "threadId turnId tokenUsage") {
		return harness.ErrProtocol
	}
	u, err := object(p["tokenUsage"])
	if err != nil || !keys(u, "total last modelContextWindow") {
		return harness.ErrProtocol
	}
	for _, name := range []string{"total", "last"} {
		b, err := object(u[name])
		if err != nil || !keys(b, "inputTokens outputTokens totalTokens cachedInputTokens cacheWriteInputTokens reasoningOutputTokens") {
			return harness.ErrProtocol
		}
		for _, key := range []string{"inputTokens", "outputTokens", "totalTokens", "cachedInputTokens", "reasoningOutputTokens", "cacheWriteInputTokens"} {
			if key == "cacheWriteInputTokens" && b[key] == nil {
				continue
			}
			if _, err := nonnegative(b[key]); err != nil {
				return err
			}
		}
	}
	if raw := u["modelContextWindow"]; raw != nil && string(raw) != "null" {
		if _, err := nonnegative(raw); err != nil {
			return err
		}
	}
	total, _ := object(u["total"])
	input, _ := nonnegative(total["inputTokens"])
	output, _ := nonnegative(total["outputTokens"])
	if s.usage != nil && (input < s.usage.InputTokens || output < s.usage.OutputTokens) {
		return harness.ErrStreamIntegrity
	}
	s.usage = &harness.UsagePayload{InputTokens: input, OutputTokens: output}
	return nil
}

func nonnegative(raw json.RawMessage) (uint64, error) {
	var n int64 // Pinned schema uses int64, not arbitrary JSON numbers.
	if string(raw) == "null" || json.Unmarshal(raw, &n) != nil || n < 0 {
		return 0, harness.ErrProtocol
	}
	return uint64(n), nil
}

func (s *eventStream) complete(ctx context.Context, turn map[string]json.RawMessage) (harness.HarnessEvent, error) {
	var none harness.HarnessEvent
	if !keys(turn, "id items itemsView status error startedAt completedAt durationMs") {
		return none, harness.ErrProtocol
	}
	reason := field(turn, "status")
	switch reason {
	case "completed", "interrupted", "failed":
	default:
		return none, harness.ErrProtocol
	}
	// Completion cannot repair missing content. Failed/interrupted turns may leave
	// items unfinished; report that outcome without inventing item completions.
	if reason == "completed" {
		for _, item := range s.items {
			if !item.done {
				return none, harness.ErrStreamIntegrity
			}
		}
	}
	payload := harness.ObservationPayload{ReasonCode: reason}
	if policy, ok := s.policy.(ResultReferencePolicy); ok {
		ref, err := policy.ResultReference(ctx, s.operation.ID, reason)
		if err != nil || (ref != "" && runtimeevent.ValidateReference(ref) != nil) {
			return none, harness.ErrProtocol
		}
		payload.ResultReference = ref
	}
	if s.usage != nil {
		// One bounded pending terminal payload. Sequence is allocated only when read.
		s.completion = &payload
		return s.emit(harness.UsageObserved, *s.usage)
	}
	return s.finish(payload)
}

func (s *eventStream) finish(payload harness.ObservationPayload) (harness.HarnessEvent, error) {
	kind := harness.ExecutionCompletionObserved
	if payload.ReasonCode == "failed" {
		kind = harness.ExecutionFailureObserved
	}
	event, err := s.emit(kind, payload)
	if err == nil {
		s.end = true
		s.completion = nil
	}
	return event, err
}
