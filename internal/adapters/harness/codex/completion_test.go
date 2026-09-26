package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func usageNotification(input, output int64) string {
	b := map[string]any{"inputTokens": input, "outputTokens": output, "totalTokens": input + output, "cachedInputTokens": 0, "reasoningOutputTokens": 0}
	return notification("thread/tokenUsage/updated", map[string]any{"threadId": testThreadID, "turnId": testTurnID, "tokenUsage": map[string]any{"total": b, "last": b, "modelContextWindow": nil}})
}

type resultPolicy struct {
	fixtureEventPolicy
	ref       string
	err       error
	calls     int
	operation primitives.ID
	reason    string
}

func (p *resultPolicy) ResultReference(_ context.Context, op primitives.ID, reason string) (string, error) {
	p.calls++
	p.operation = op
	p.reason = reason
	return p.ref, p.err
}

func TestTurnCompletion(t *testing.T) {
	for _, reason := range []string{"completed", "failed", "interrupted"} {
		t.Run(reason, func(t *testing.T) {
			policy := &resultPolicy{ref: "artifact:protected-result"}
			wire := turnNotification("turn/started", "inProgress") + usageNotification(5, 2) + usageNotification(5, 2) + usageNotification(11, 4)
			if reason != "completed" { // No fake message completion for unfinished work.
				wire += itemNotification("agentMessage", "partial", false, map[string]any{"text": ""})
			}
			terminal := turnNotification("turn/completed", reason)
			wire += terminal + terminal // terminal repeats cannot allocate a second outcome
			s, _ := fixtureEvents(t, wire, policy)
			events := readEvents(t, s)
			kind := harness.ExecutionCompletionObserved
			if reason == "failed" {
				kind = harness.ExecutionFailureObserved
			}
			if len(events) != 3 || events[1].Type != harness.UsageObserved || events[2].Type != kind {
				t.Fatal("unexpected events", len(events))
			}
			var usage harness.UsagePayload
			var result harness.ObservationPayload
			_ = json.Unmarshal(events[1].Content.Inline, &usage)
			_ = json.Unmarshal(events[2].Content.Inline, &result)
			if usage.InputTokens != 11 || usage.OutputTokens != 4 || result.ReasonCode != reason || result.ResultReference != policy.ref || policy.calls != 1 || policy.operation != turnOperation || policy.reason != reason {
				t.Fatal("capture mismatch")
			}
			for i, e := range events {
				rule, err := harness.CheckEventDeclaration(e, s.capabilities)
				if err != nil || rule.RuntimeType != "" || e.Sequence != uint64(i+1) || e.Operation != s.operation || e.Handle != s.handle {
					t.Fatal("authority/correlation")
				}
			}
			if _, err := s.Next(t.Context()); err != io.EOF {
				t.Fatal(err)
			}
		})
	}
}

func TestCompletionWithoutUsageOrReference(t *testing.T) {
	diagnostic := notification("error", map[string]any{"threadId": testThreadID, "turnId": testTurnID, "willRetry": false, "error": map[string]any{"message": "secret-canary"}})
	s, _ := fixtureEvents(t, turnNotification("turn/started", "inProgress")+diagnostic+strings.Replace(turnNotification("turn/completed", "failed"), `"error":null`, `"error":{"message":"secret-canary"}`, 1), nil)
	events := readEvents(t, s)
	if len(events) != 2 || events[1].Type != harness.ExecutionFailureObserved || string(events[1].Content.Inline) != `{"reason_code":"failed"}` {
		t.Fatal("missing usage fabricated or diagnostics exposed")
	}
}

func TestCompletionRejectsInvalidCapture(t *testing.T) {
	usage := usageNotification(5, 2)
	for name, wire := range map[string]string{
		"wrong turn":   strings.Replace(usage, testTurnID, testThreadID, 1),
		"missing turn": strings.Replace(usage, `,"turnId":"`+testTurnID+`"`, "", 1),
		"negative":     usageNotification(-1, 2),
		"null":         strings.ReplaceAll(usage, `"inputTokens":5`, `"inputTokens":null`),
		"overflow":     strings.ReplaceAll(usage, `"inputTokens":5`, `"inputTokens":9223372036854775808`),
		"fraction":     strings.ReplaceAll(usage, `"inputTokens":5`, `"inputTokens":1.5`),
		"unknown":      strings.Replace(usage, `"tokenUsage":{`, `"tokenUsage":{"secret":"canary",`, 1),
		"regression":   usage + usageNotification(4, 2),
		"truncated":    usage,
		"bad status":   turnNotification("turn/completed", "inProgress"),
	} {
		t.Run(name, func(t *testing.T) {
			if name != "truncated" {
				wire += turnNotification("turn/completed", "completed")
			}
			s, _ := fixtureEvents(t, turnNotification("turn/started", "inProgress")+wire, nil)
			if _, err := s.Next(t.Context()); err != nil {
				t.Fatal(err)
			}
			if e, err := s.Next(t.Context()); err == nil || err == io.EOF || e.Type != "" {
				t.Fatal("invalid capture admitted", err)
			}
		})
	}
	for _, policy := range []*resultPolicy{{ref: strings.Repeat("x", 2049)}, {err: errors.New("secret-canary")}} {
		s, _ := fixtureEvents(t, turnNotification("turn/started", "inProgress")+usage+turnNotification("turn/completed", "completed"), policy)
		_, _ = s.Next(t.Context())
		if e, err := s.Next(t.Context()); err != harness.ErrProtocol || e.Type != "" {
			t.Fatal("unsafe reference admitted", err)
		}
	}
	s, _ := fixtureEvents(t, turnNotification("turn/started", "inProgress")+usage, nil)
	delete(s.capabilities, harness.UsageObservation)
	_, _ = s.Next(t.Context())
	if _, err := s.Next(t.Context()); err != harness.ErrUnsupported {
		t.Fatal("unnegotiated usage", err)
	}
}
