// Package runtimeevent contains immutable, ordered runtime observations.
package runtimeevent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

const (
	SchemaVersion   = "thinkpixel.runtime-event/v1"
	MaxPayloadBytes = 64 * 1024
	MaxReferenceLen = 2048
	maxJSONDepth    = 16
	maxJSONMembers  = 256
)

type Source string
type Classification string
type Type string

const (
	SourceAgentRuntime      Source = "agent-runtime"
	SourceAgentd            Source = "agentd"
	SourceHarnessAdapter    Source = "harness-adapter"
	SourceSandboxProvider   Source = "sandbox-provider"
	SourceWorkspaceProvider Source = "workspace-provider"
	SourceRunAuthority      Source = "run-authority"
	SourceGateway           Source = "gateway"

	Public       Classification = "Public"
	Internal     Classification = "Internal"
	Confidential Classification = "Confidential"
)

var (
	ErrInvalidEvent = errors.New("invalid runtime event")
	sources         = []Source{SourceAgentRuntime, SourceAgentd, SourceHarnessAdapter, SourceSandboxProvider, SourceWorkspaceProvider, SourceRunAuthority, SourceGateway}
	classifications = []Classification{Public, Internal, Confidential}
	types           = []Type{
		"session.created", "session.state_changed", "session.degraded", "session.closed",
		"execution.accepted", "execution.started", "execution.completed", "execution.failed", "execution.cancelled", "execution.timed_out",
		"attempt.started", "attempt.replaced", "attempt.terminal", "sandbox.state_changed", "sandbox.health_changed",
		"workspace.generation_committed", "checkpoint.committed", "checkpoint.deleted",
		"assistant.message.delta", "assistant.message.completed", "tool.requested", "tool.status_changed", "artifact.published",
		"signal.accepted", "permission.requested", "permission.resolved", "stream.gap",
	}
)

type Correlation struct {
	RequestID primitives.ID
	TraceID   string
	SpanID    string
}

// Event is the immutable envelope persisted after source and fence validation.
type Event struct {
	eventID, tenantID, sessionID, executionID, attemptID primitives.ID
	sequence, aggregateVersion                           uint64
	typeName                                             Type
	occurredAt, recordedAt                               time.Time
	source                                               Source
	classification                                       Classification
	payload                                              []byte
	correlation                                          Correlation
	retentionPolicy                                      string
	retainUntil                                          *time.Time
}

func New(eventID, tenantID, sessionID, executionID, attemptID primitives.ID, sequence, aggregateVersion uint64,
	typeName Type, occurredAt, recordedAt time.Time, source Source, classification Classification, payload []byte,
	correlation Correlation, retentionPolicy string, retainUntil *time.Time) (*Event, error) {
	if !validID(eventID) || !validID(tenantID) || !validID(sessionID) || (executionID != "" && !validID(executionID)) ||
		(attemptID != "" && (!validID(attemptID) || executionID == "")) || sequence == 0 || aggregateVersion == 0 ||
		!enum(typeName, types) || !enum(source, sources) || !enum(classification, classifications) ||
		occurredAt.IsZero() || recordedAt.IsZero() || !validCorrelation(correlation) ||
		!bounded(retentionPolicy, 128) || (retainUntil != nil && !retainUntil.After(recordedAt)) || validatePayload(payload) != nil {
		return nil, ErrInvalidEvent
	}
	copyPayload := append([]byte(nil), payload...)
	var expiry *time.Time
	if retainUntil != nil {
		at := retainUntil.UTC()
		expiry = &at
	}
	return &Event{eventID: eventID, tenantID: tenantID, sessionID: sessionID, executionID: executionID, attemptID: attemptID,
		sequence: sequence, aggregateVersion: aggregateVersion, typeName: typeName, occurredAt: occurredAt.UTC(), recordedAt: recordedAt.UTC(),
		source: source, classification: classification, payload: copyPayload, correlation: correlation,
		retentionPolicy: retentionPolicy, retainUntil: expiry}, nil
}

func (e *Event) Sequence() uint64               { return e.sequence }
func (e *Event) Payload() []byte                { return append([]byte(nil), e.payload...) }
func (e *Event) EventID() primitives.ID         { return e.eventID }
func (e *Event) TenantID() primitives.ID        { return e.tenantID }
func (e *Event) SessionID() primitives.ID       { return e.sessionID }
func (e *Event) ExecutionID() primitives.ID     { return e.executionID }
func (e *Event) AttemptID() primitives.ID       { return e.attemptID }
func (e *Event) AggregateVersion() uint64       { return e.aggregateVersion }
func (e *Event) Type() Type                     { return e.typeName }
func (e *Event) OccurredAt() time.Time          { return e.occurredAt }
func (e *Event) RecordedAt() time.Time          { return e.recordedAt }
func (e *Event) Source() Source                 { return e.source }
func (e *Event) Classification() Classification { return e.classification }
func (e *Event) Correlation() Correlation       { return e.correlation }
func (e *Event) RetentionPolicy() string        { return e.retentionPolicy }
func (e *Event) RetainUntil() (time.Time, bool) {
	if e.retainUntil == nil {
		return time.Time{}, false
	}
	return *e.retainUntil, true
}

// ValidateReference applies the storage bound used by registered payload reference fields.
func ValidateReference(value string) error {
	if !bounded(value, MaxReferenceLen) {
		return ErrInvalidEvent
	}
	return nil
}

func validatePayload(value []byte) error {
	if len(value) < 2 || len(value) > MaxPayloadBytes {
		return ErrInvalidEvent
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	members := 0
	if err := consumeJSON(decoder, 0, &members, true); err != nil {
		return ErrInvalidEvent
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalidEvent
	}
	return nil
}

func consumeJSON(d *json.Decoder, depth int, members *int, root bool) error {
	if depth > maxJSONDepth {
		return ErrInvalidEvent
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, compound := token.(json.Delim)
	if !compound {
		if root {
			return ErrInvalidEvent
		}
		if s, ok := token.(string); ok && strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) }) >= 0 {
			return ErrInvalidEvent
		}
		return nil
	}
	if root && delim != '{' {
		return ErrInvalidEvent
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || strings.IndexFunc(name, unicode.IsControl) >= 0 {
				return ErrInvalidEvent
			}
			if _, exists := seen[name]; exists {
				return ErrInvalidEvent
			}
			seen[name] = struct{}{}
			*members++
			if (root && len(seen) > 64) || *members > maxJSONMembers {
				return ErrInvalidEvent
			}
			if err := consumeJSON(d, depth+1, members, false); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			*members++
			if *members > maxJSONMembers {
				return ErrInvalidEvent
			}
			if err := consumeJSON(d, depth+1, members, false); err != nil {
				return err
			}
		}
	default:
		return ErrInvalidEvent
	}
	end, err := d.Token()
	if err != nil {
		return err
	}
	if end != json.Delim(map[json.Delim]json.Delim{'{': '}', '[': ']'}[delim]) {
		return ErrInvalidEvent
	}
	return nil
}

func validCorrelation(c Correlation) bool {
	return (c.RequestID == "" || validID(c.RequestID)) && (c.TraceID == "" || lowerHex(c.TraceID, 32)) &&
		(c.SpanID == "" || lowerHex(c.SpanID, 16)) && (c.SpanID == "" || c.TraceID != "")
}
func lowerHex(s string, length int) bool {
	if len(s) != length {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
func validID(id primitives.ID) bool { _, err := primitives.ParseID(string(id)); return err == nil }
func bounded(s string, max int) bool {
	_, err := primitives.BoundedString(s, 1, max, max)
	return err == nil
}
func enum[T comparable](value T, allowed []T) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
