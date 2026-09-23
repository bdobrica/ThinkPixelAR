package harness

import (
	"errors"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
)

func TestRegisteredCandidateMappings(t *testing.T) {
	for _, tc := range []struct {
		kind, target string
		capability   HarnessCapability
	}{
		{HarnessStarted, "", StructuredEvents}, {HarnessReady, "", StructuredEvents}, {HarnessExitObserved, "", StructuredEvents},
		{ExecutionStarted, "", StructuredEvents}, {ExecutionProgress, "", StructuredEvents},
		{ExecutionCompletionObserved, "", StructuredEvents}, {ExecutionFailureObserved, "", StructuredEvents},
		{MessageDelta, "assistant.message.delta", StructuredEvents}, {MessageCompleted, "assistant.message.completed", StructuredEvents},
		{ProcessStarted, "", StructuredProcessEvents}, {ProcessOutput, "", StructuredProcessEvents}, {ProcessCompleted, "", StructuredProcessEvents},
		{ToolRequested, "tool.requested", StructuredToolEvents}, {ToolStarted, "tool.status_changed", StructuredToolEvents},
		{ToolCompleted, "tool.status_changed", StructuredToolEvents}, {ToolFailed, "tool.status_changed", StructuredToolEvents},
		{ApprovalRequested, "permission.requested", LocalApprovalEvents}, {ApprovalResolved, "permission.resolved", LocalApprovalEvents},
		{CheckpointPrepareStarted, "", CheckpointPrepare}, {CheckpointPrepareCompleted, "", CheckpointPrepare}, {UsageObserved, "", UsageObservation},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			rule, err := EventRuleFor(tc.kind)
			if err != nil || string(rule.RuntimeType) != tc.target || rule.Capability != tc.capability || rule.PayloadKind == "" {
				t.Fatal("registry mapping mismatch")
			}
		})
	}
	for _, forbidden := range []string{"execution.completed", "execution.failed", "checkpoint.committed", "artifact.published", "reasoning.delta", "vendor.unknown", ""} {
		if _, err := EventRuleFor(forbidden); !errors.Is(err, ErrProtocol) {
			t.Fatal("unregistered or authoritative candidate accepted")
		}
	}
}

func TestCandidateDeclarationsFailClosed(t *testing.T) {
	valid := HarnessEvent{Type: ToolCompleted, SchemaVersion: EventSchemaVersion,
		Content: Content{Schema: EventSchemaVersion, Classification: runtimeevent.Confidential}}
	caps := Capabilities{StructuredEvents: Supported, StructuredToolEvents: Supported}
	if _, err := CheckEventDeclaration(valid, caps); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*HarnessEvent){
		func(e *HarnessEvent) { e.Type = "execution.completed" },
		func(e *HarnessEvent) { e.SchemaVersion = "unknown" },
		func(e *HarnessEvent) { e.Content.Schema = "vendor.raw" },
		func(e *HarnessEvent) { e.Content.Classification = runtimeevent.Public },
		func(e *HarnessEvent) { e.Content.Classification = runtimeevent.Internal },
	} {
		e := valid
		mutate(&e)
		if _, err := CheckEventDeclaration(e, caps); !errors.Is(err, ErrProtocol) {
			t.Fatal("invalid declaration accepted")
		}
	}
	if _, err := CheckEventDeclaration(valid, Capabilities{StructuredEvents: Supported}); !errors.Is(err, ErrUnsupported) {
		t.Fatal("unnegotiated event capability accepted")
	}
}
