package harness

import (
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

const EventSchemaVersion = "thinkpixel.harness-event/v1"

// Candidate names are distinct from committed Runtime Event types even where
// their spelling matches. A harness never chooses an authoritative transition.
const (
	HarnessStarted              = "harness.started"
	HarnessReady                = "harness.ready"
	HarnessExitObserved         = "harness.exit-observed"
	ExecutionStarted            = "execution.started"
	ExecutionProgress           = "execution.progress"
	ExecutionCompletionObserved = "execution.completion-observed"
	ExecutionFailureObserved    = "execution.failure-observed"
	MessageDelta                = "message.delta"
	MessageCompleted            = "message.completed"
	ProcessStarted              = "process.started"
	ProcessOutput               = "process.output"
	ProcessCompleted            = "process.completed"
	ToolRequested               = "tool.requested"
	ToolStarted                 = "tool.started"
	ToolCompleted               = "tool.completed"
	ToolFailed                  = "tool.failed"
	ApprovalRequested           = "approval.requested"
	ApprovalResolved            = "approval.resolved"
	CheckpointPrepareStarted    = "checkpoint.prepare.started"
	CheckpointPrepareCompleted  = "checkpoint.prepare.completed"
	UsageObserved               = "usage.observed"
)

// EventRule names the closed payload family and required negotiated capability.
// RuntimeType is a possible projection AFTER trusted validation, not permission
// to persist. Empty means observation only: AR must decide any canonical event.
type EventRule struct {
	PayloadKind string
	Capability  HarnessCapability
	RuntimeType runtimeevent.Type
}

func EventRuleFor(kind string) (EventRule, error) {
	switch kind {
	case HarnessStarted, HarnessReady, HarnessExitObserved, ExecutionStarted, ExecutionProgress, ExecutionCompletionObserved, ExecutionFailureObserved:
		return EventRule{PayloadKind: "observation", Capability: StructuredEvents}, nil
	case MessageDelta:
		return EventRule{"message-delta", StructuredEvents, "assistant.message.delta"}, nil
	case MessageCompleted:
		return EventRule{"message-completed", StructuredEvents, "assistant.message.completed"}, nil
	case ProcessStarted, ProcessOutput, ProcessCompleted:
		return EventRule{PayloadKind: "process", Capability: StructuredProcessEvents}, nil
	case ToolRequested:
		return EventRule{"tool", StructuredToolEvents, "tool.requested"}, nil
	case ToolStarted, ToolCompleted, ToolFailed:
		return EventRule{"tool", StructuredToolEvents, "tool.status_changed"}, nil
	case ApprovalRequested:
		return EventRule{"approval", LocalApprovalEvents, "permission.requested"}, nil
	case ApprovalResolved:
		return EventRule{"approval", LocalApprovalEvents, "permission.resolved"}, nil
	case CheckpointPrepareStarted, CheckpointPrepareCompleted:
		return EventRule{PayloadKind: "checkpoint", Capability: CheckpointPrepare}, nil
	case UsageObserved:
		return EventRule{PayloadKind: "usage", Capability: UsageObservation}, nil
	default:
		return EventRule{}, ErrProtocol
	}
}

// CheckEventDeclaration checks only registry/schema/classification/capability
// declarations. It does not validate payloads, fences, sequence or authority.
// The v1 candidate lane is Confidential; adapters cannot lower its classification.
func CheckEventDeclaration(e HarnessEvent, negotiated Capabilities) (EventRule, error) {
	rule, err := EventRuleFor(e.Type)
	if err != nil || e.SchemaVersion != EventSchemaVersion || e.Content.Schema != EventSchemaVersion ||
		e.Content.Classification != runtimeevent.Confidential {
		return EventRule{}, ErrProtocol
	}
	if negotiated.Require(StructuredEvents, rule.Capability) != nil {
		return EventRule{}, ErrUnsupported
	}
	return rule, nil
}

// These closed payload shapes are built from explicitly selected vendor fields,
// never by serializing a raw vendor object. They are not validators. Decoders must
// reject unknown/duplicate fields and apply the rules in docs/contracts/harness-events.md.
type ObservationPayload struct {
	ReasonCode      string `json:"reason_code,omitempty"`
	ResultReference string `json:"result_reference,omitempty"`
}

type MessageDeltaPayload struct {
	MessageID primitives.ID `json:"message_id"`
	Text      string        `json:"text"` // User-visible, policy-sanitized content only.
}

type MessageCompletedPayload struct {
	MessageID        primitives.ID `json:"message_id"`
	FirstSequence    uint64        `json:"first_sequence"`
	LastSequence     uint64        `json:"last_sequence"`
	ContentReference string        `json:"content_reference,omitempty"`
}

type ProcessPayload struct {
	ProcessID       primitives.ID `json:"process_id"`
	OutputReference string        `json:"output_reference,omitempty"`
	ExitCode        *int32        `json:"exit_code,omitempty"`
}

type ToolPayload struct {
	ToolCallID       primitives.ID `json:"tool_call_id"`
	ContentReference string        `json:"content_reference,omitempty"`
}

type ApprovalPayload struct {
	ApprovalID       primitives.ID `json:"approval_id"`
	Decision         string        `json:"decision,omitempty"`
	ContentReference string        `json:"content_reference,omitempty"`
}

type CheckpointPayload struct {
	PreparationID primitives.ID `json:"preparation_id"`
}

type UsagePayload struct {
	InputTokens  uint64 `json:"input_tokens"`
	OutputTokens uint64 `json:"output_tokens"`
}
