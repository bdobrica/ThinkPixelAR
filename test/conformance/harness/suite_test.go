package harnessconformance

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimebinding"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// This deterministic in-memory double validates the suite, not a real harness.
// Faults are deliberate adapter bugs which the same black-box checks must catch.
type memoryAdapter struct {
	f               Fixture
	d               harness.AdapterDescriptor
	h               harness.HarnessHandle
	state           runtimebinding.HarnessState
	fault           string
	descriptorCalls int
	executing       bool
}

func fixture(t *testing.T, fault string) Fixture {
	t.Helper()
	digest := otherDigest("")
	fence := harness.Fence{TenantID: id(1), SessionID: id(2), ExecutionID: id(3), AttemptID: id(4), SandboxBindingID: id(5), Generation: 1, AttemptOrdinal: 1}
	mutation := func(n int) harness.Mutation {
		return harness.Mutation{Fence: fence,
			Operation: runtimebinding.OperationIdentity{ID: id(n), RequestDigest: digest}, Deadline: time.Now().Add(time.Minute)}
	}
	caps := harness.Capabilities{harness.StructuredEvents: harness.Required, harness.Streaming: harness.Supported, harness.Interrupt: harness.Supported}
	limits := harness.AdapterLimits{InputBytes: 1024, InputItems: 1, EventBytes: 1024, EventsPerSecond: 10,
		BufferedEvents: 4, SignalBytes: 128, DiagnosticBytes: 128, VendorStatePaths: 1, ShutdownTimeout: time.Second, CheckpointTimeout: time.Second}
	f := Fixture{
		Compatibility: harness.CompatibilityRequest{AdapterKind: "fixture", AdapterBuildDigest: digest, ContractVersion: "1.0.0",
			RuntimeDigest: digest, EventSchemaVersion: "fixture.v1", VendorStateFormat: "fixture.v1",
			HarnessProtocol: harness.VersionRange{Minimum: "1.0.0", Maximum: "1.0.0"}, AgentdProtocol: harness.VersionRange{Minimum: "1.0.0", Maximum: "1.0.0"},
			RequiredCapabilities: []harness.HarnessCapability{harness.StructuredEvents, harness.Streaming, harness.Interrupt}, Limits: limits},
		WantCompatibility: harness.CompatibilityResult{AdapterKind: "fixture", AdapterBuildDigest: digest, ImplementationVersion: "1.0.0",
			ContractVersion: "1.0.0", EventSchemaVersion: "fixture.v1", HarnessProtocol: "1.0.0", AgentdProtocol: "1.0.0",
			VendorStateFormat: "fixture.v1", RuntimeDigest: digest, RequestDigest: digest, ResultDigest: digest, ReasonCode: "compatible", Capabilities: caps, Limits: limits},
		Start: harness.StartHarnessRequest{Mutation: mutation(6), HandleID: id(7), RuntimeDigest: digest, NegotiationDigest: digest,
			Workspace: harness.Mount{Path: "/workspace", Reference: "fixture-workspace"}},
		Execute:   harness.ExecuteRequest{Mutation: mutation(8), InputID: id(9), Input: harness.Content{Classification: runtimeevent.Confidential, Schema: "fixture.v1", Inline: []byte("test")}},
		Interrupt: harness.InterruptRequest{Mutation: mutation(10), ReasonCode: "cancelled"},
		Close:     harness.CloseHarnessRequest{Mutation: mutation(11), ReasonCode: "test-cleanup"},
	}
	a := &memoryAdapter{f: f, fault: fault, d: harness.AdapterDescriptor{Kind: "fixture", ImplementationVersion: "1.0.0", ContractVersion: "1.0.0",
		HarnessProtocols: []harness.VersionRange{{Minimum: "1.0.0", Maximum: "1.0.0"}}, AgentdProtocols: []harness.VersionRange{{Minimum: "1.0.0", Maximum: "1.0.0"}},
		VendorStateFormats: []string{"fixture.v1"}, Capabilities: caps, Limits: limits}}
	f.Adapter = a
	t.Cleanup(func() { a.state = runtimebinding.HarnessExited })
	return f
}

func id(n int) primitives.ID { return primitives.ID(fmt.Sprintf("00000000-0000-7000-8000-%012d", n)) }

func (a *memoryAdapter) Descriptor(context.Context) (harness.AdapterDescriptor, error) {
	a.descriptorCalls++
	d := a.d
	if a.fault == "descriptor-drift" && a.descriptorCalls > 1 {
		d.ImplementationVersion = "1.0.1"
	}
	return d, nil
}
func (a *memoryAdapter) Negotiate(_ context.Context, r harness.CompatibilityRequest) (harness.CompatibilityResult, error) {
	if a.fault != "accept-incompatible" && (r.AdapterKind != a.d.Kind || r.ContractVersion != a.d.ContractVersion || a.d.Capabilities.Require(r.RequiredCapabilities...) != nil) {
		return harness.CompatibilityResult{}, harness.ErrIncompatible
	}
	return a.f.WantCompatibility, nil
}
func (a *memoryAdapter) Start(_ context.Context, r harness.StartHarnessRequest) (harness.HarnessHandle, error) {
	if r.Operation != a.f.Start.Operation && a.fault != "accept-start-conflict" {
		return harness.HarnessHandle{}, harness.ErrConflict
	}
	if a.h.ID == "" {
		a.h = harness.HarnessHandle{ID: r.HandleID, ProcessInstanceID: id(12), Fence: r.Fence, AdapterKind: "fixture", AdapterBuildDigest: r.RuntimeDigest,
			NegotiationDigest: r.NegotiationDigest, VendorSessionReference: "fixture-session"}
		a.state = runtimebinding.HarnessReady
		if a.fault == "bad-handle" {
			a.h.Fence.Generation++
		}
		if a.fault == "not-ready" {
			a.state = runtimebinding.HarnessStarting
		}
	} else if a.fault == "new-handle" {
		a.h.ProcessInstanceID = id(99)
	}
	return a.h, nil
}
func (a *memoryAdapter) Status(_ context.Context, h harness.HarnessHandle) (harness.HarnessStatus, error) {
	if h != a.h && a.fault != "accept-stale" {
		return harness.HarnessStatus{}, harness.ErrStale
	}
	return harness.HarnessStatus{State: a.state, ObservedAt: time.Now()}, nil
}
func (a *memoryAdapter) Execute(_ context.Context, h harness.HarnessHandle, r harness.ExecuteRequest) (harness.HarnessEventStream, error) {
	if h != a.h {
		return nil, harness.ErrStale
	}
	if r.Operation != a.f.Execute.Operation && a.fault != "accept-execute-conflict" {
		return nil, harness.ErrConflict
	}
	streamID := id(13)
	if a.executing && a.fault == "new-stream" {
		streamID = id(99)
	}
	a.executing = true
	a.state = runtimebinding.HarnessExecuting
	return &memoryStream{a: a, streamID: streamID}, nil
}
func (a *memoryAdapter) Interrupt(context.Context, harness.HarnessHandle, harness.InterruptRequest) error {
	if a.fault == "interrupt-failure" {
		return harness.ErrOutcomeUnknown
	}
	a.state = runtimebinding.HarnessReady
	return nil
}
func (a *memoryAdapter) Close(context.Context, harness.HarnessHandle, harness.CloseHarnessRequest) error {
	if a.fault == "close-failure" {
		return harness.ErrOutcomeUnknown
	}
	a.state = runtimebinding.HarnessExited
	return nil
}
func (a *memoryAdapter) Resume(context.Context, harness.ResumeHarnessRequest) (harness.HarnessHandle, error) {
	if a.fault == "unsupported-resume" {
		return a.h, nil
	}
	return harness.HarnessHandle{}, harness.ErrUnsupported
}
func (*memoryAdapter) Signal(context.Context, harness.HarnessHandle, harness.SignalRequest) error {
	return harness.ErrUnsupported
}
func (*memoryAdapter) PrepareCheckpoint(context.Context, harness.HarnessHandle, harness.CheckpointRequest) (harness.HarnessCheckpoint, error) {
	return harness.HarnessCheckpoint{}, harness.ErrUnsupported
}

type memoryStream struct {
	a        *memoryAdapter
	streamID primitives.ID
	next     int
}

func (s *memoryStream) Next(ctx context.Context) (harness.HarnessEvent, error) {
	if err := ctx.Err(); err != nil && s.a.fault != "ignore-cancellation" {
		return harness.HarnessEvent{}, err
	}
	if s.next >= 2 && s.a.fault != "ignore-cancellation" {
		return harness.HarnessEvent{}, io.EOF
	}
	s.next++
	e := harness.HarnessEvent{StreamID: s.streamID, EventID: id(20 + s.next), Sequence: uint64(s.next), Handle: s.a.h, Operation: s.a.f.Execute.Operation,
		Type: "fixture.progress", SchemaVersion: "fixture.v1", OccurredAt: time.Now(), ObservedAt: time.Now(),
		Content: harness.Content{Classification: runtimeevent.Confidential, Schema: "fixture.v1", Inline: []byte("progress")}}
	if s.a.fault == "event-gap" {
		e.Sequence++
	}
	if s.a.fault == "event-classification" {
		e.Content.Classification = "unknown"
	}
	if s.a.fault == "oversized-event" {
		e.Content.Inline = make([]byte, s.a.d.Limits.EventBytes+1)
	}
	return e, nil
}
func (*memoryStream) Close() error { return nil }

func TestDemoConformance(t *testing.T) { Run(t, func(t *testing.T) Fixture { return fixture(t, "") }) }

func TestSuiteRejectsBrokenAdapters(t *testing.T) {
	for _, tc := range []struct{ caseName, fault string }{
		{"descriptor", "descriptor-drift"}, {"negotiation", "accept-incompatible"},
		{"lifecycle", "bad-handle"}, {"lifecycle", "not-ready"}, {"lifecycle", "event-gap"},
		{"lifecycle", "oversized-event"}, {"lifecycle", "ignore-cancellation"},
		{"lifecycle", "interrupt-failure"}, {"lifecycle", "close-failure"},
		{"replay_and_stale_handle", "new-handle"}, {"replay_and_stale_handle", "accept-start-conflict"},
		{"replay_and_stale_handle", "accept-execute-conflict"}, {"replay_and_stale_handle", "accept-stale"},
		{"replay_and_stale_handle", "new-stream"},
		{"lifecycle", "event-classification"}, {"unsupported_optional_operations", "unsupported-resume"},
	} {
		t.Run(tc.fault, func(t *testing.T) {
			for _, c := range cases {
				if c.name == tc.caseName {
					ctx, cancel := context.WithTimeout(t.Context(), time.Second)
					defer cancel()
					if err := c.check(ctx, fixture(t, tc.fault)); err == nil {
						t.Fatal("suite accepted broken adapter")
					}
					return
				}
			}
			t.Fatal("missing conformance case")
		})
	}
}
