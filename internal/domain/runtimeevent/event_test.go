package runtimeevent

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func TestEventRetainsImmutableOrderedEnvelope(t *testing.T) {
	payload := []byte(`{"state":"RUNNING"}`)
	e, err := New(id(1), id(2), id(3), id(4), id(5), 7, 2, "execution.started", time.Unix(1, 0), time.Unix(2, 0),
		SourceAgentRuntime, Confidential, payload, Correlation{RequestID: id(6), TraceID: strings.Repeat("a", 32), SpanID: strings.Repeat("b", 16)}, "session-default", timePtr(time.Unix(20, 0)))
	if err != nil {
		t.Fatal(err)
	}
	payload[0] = '['
	got := e.Payload()
	got[0] = '['
	if e.Sequence() != 7 || e.EventID() != id(1) || e.Payload()[0] != '{' {
		t.Fatal("event envelope was not retained immutably")
	}
}

func TestEventRejectsInvalidEnvelope(t *testing.T) {
	base := func(payload []byte) error {
		_, err := New(id(1), id(2), id(3), "", "", 1, 1, "session.created", time.Unix(1, 0), time.Unix(2, 0), SourceAgentRuntime, Confidential, payload, Correlation{}, "default", nil)
		return err
	}
	for name, payload := range map[string][]byte{
		"array": []byte(`[]`), "duplicate": []byte(`{"a":1,"a":2}`), "control": []byte(`{"a":"\u0000"}`),
		"trailing": []byte(`{} true`), "oversize": append([]byte(`{"a":"`), append(bytes.Repeat([]byte{'a'}, MaxPayloadBytes), []byte(`"}`)...)...),
	} {
		t.Run(name, func(t *testing.T) {
			if err := base(payload); err != ErrInvalidEvent {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestEventRejectsAttemptWithoutExecutionAndUnknownType(t *testing.T) {
	for _, tc := range []struct {
		execution, attempt primitives.ID
		typ                Type
	}{{"", id(5), "attempt.started"}, {id(4), id(5), "vendor.raw"}} {
		_, err := New(id(1), id(2), id(3), tc.execution, tc.attempt, 1, 1, tc.typ, time.Unix(1, 0), time.Unix(2, 0), SourceAgentd, Internal, []byte(`{}`), Correlation{}, "default", nil)
		if err != ErrInvalidEvent {
			t.Fatalf("error = %v", err)
		}
	}
}

func TestReferenceBound(t *testing.T) {
	if ValidateReference(strings.Repeat("r", MaxReferenceLen)) != nil || ValidateReference("") != ErrInvalidEvent || ValidateReference(strings.Repeat("r", MaxReferenceLen+1)) != ErrInvalidEvent {
		t.Fatal("reference bounds not enforced")
	}
}

func id(last byte) primitives.ID {
	return primitives.ID("01890f3e-7b2d-7000-8000-00000000000" + string('0'+last))
}
func timePtr(value time.Time) *time.Time { return &value }
