package runtimebinding

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var testNow = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

func TestSandboxBindingOperationsAndObservations(t *testing.T) {
	b, err := NewSandbox(id(1), id(2), id(3), id(4), id(5), 1, 1, "agent-sandbox", "opaque:resource", digest("a"), operation(6, "b"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.RecordOperation(OperationSuspend, operation(7, "c"), 1, testNow.Add(time.Minute)); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("version error = %v", err)
	}
	if err := b.RecordOperation(OperationSuspend, operation(7, "c"), 0, testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := b.RecordOperation(OperationSuspend, operation(7, "c"), 1, testNow.Add(2*time.Minute)); err != nil {
		t.Fatalf("idempotent operation = %v", err)
	}
	if err := b.Observe(SandboxReady, "provider-ready", digest("d"), testNow.Add(time.Minute), 1, testNow.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if b.State() != SandboxReady || b.StateVersion() != 2 {
		t.Fatalf("state/version = %s/%d", b.State(), b.StateVersion())
	}
	if err := b.Observe(SandboxActive, "active", "", testNow, 2, testNow.Add(3*time.Minute)); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("regressed observation = %v", err)
	}
}

func TestHarnessBindingIsNeutralAndFenced(t *testing.T) {
	spec := HarnessSpecification{AdapterKind: "codex", AdapterVersion: "1.2.3", AdapterBuildDigest: digest("a"), NegotiationDigest: digest("b"), ProtocolName: "app-server", ProtocolVersion: "2", ProcessReference: "process:opaque", VendorSessionReference: "session:opaque"}
	b, err := NewHarness(id(1), id(2), id(3), id(4), id(5), id(6), 1, 1, spec, operation(7, "c"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Observe(HarnessReady, "handshake-valid", testNow.Add(time.Second), 0, testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if b.State() != HarnessReady || b.SandboxBindingID() != id(6) {
		t.Fatalf("unexpected binding state")
	}
	if err := b.Observe(HarnessExecuting, "running", testNow.Add(2*time.Second), 0, testNow.Add(2*time.Minute)); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale update = %v", err)
	}
}

func TestSandboxProviderReferenceCanBeReservedThenBoundOnce(t *testing.T) {
	b, err := NewSandbox(id(1), id(2), id(3), id(4), id(5), 1, 1, "agent-sandbox", "", digest("a"), operation(6, "b"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.BindProviderReference("opaque:resource", 0, testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if value, ok := b.ProviderReference(); !ok || value != "opaque:resource" {
		t.Fatalf("reference = %q, %v", value, ok)
	}
	if err := b.BindProviderReference("opaque:other", 1, testNow.Add(2*time.Minute)); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("replacement = %v", err)
	}
}

func TestBindingsRejectInvalidAndSensitiveShapes(t *testing.T) {
	if _, err := NewSandbox(id(1), id(2), id(3), id(4), id(5), 1, 1, "", "", digest("a"), operation(6, "b"), testNow); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("empty provider kind = %v", err)
	}
	spec := HarnessSpecification{AdapterKind: "codex", AdapterVersion: "1", AdapterBuildDigest: digest("a"), NegotiationDigest: "bad", ProtocolName: "p", ProtocolVersion: "1", ProcessReference: "p", VendorSessionReference: "v"}
	if _, err := NewHarness(id(1), id(2), id(3), id(4), id(5), id(6), 1, 1, spec, operation(7, "c"), testNow); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("bad digest = %v", err)
	}
}

func operation(last byte, char string) OperationIdentity {
	return OperationIdentity{ID: id(last), RequestDigest: digest(char)}
}
func digest(char string) string { return "sha256:" + strings.Repeat(char, 64) }
func id(last byte) primitives.ID {
	value, err := primitives.ParseID("01890f3a-5b7c-7def-8000-00000000000" + string(rune('0'+last)))
	if err != nil {
		panic(err)
	}
	return value
}
