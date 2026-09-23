// Package harnessconformance provides the shared first-turn adapter test suite.
// It is test support, never a production adapter or a qualification certificate.
package harnessconformance

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/app/harnessregistry"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimebinding"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// Fixture supplies a fresh adapter and valid trusted requests for each case.
// Its factory registers unconditional resource cleanup with t.Cleanup. Execute
// must produce at least two candidate events and remain interruptible. Operation
// IDs are distinct and request digests are valid, immutable identities.
type Fixture struct {
	Adapter           harness.HarnessAdapter
	Compatibility     harness.CompatibilityRequest
	WantCompatibility harness.CompatibilityResult
	Start             harness.StartHarnessRequest
	Execute           harness.ExecuteRequest
	Interrupt         harness.InterruptRequest
	Close             harness.CloseHarnessRequest
}

type Factory func(*testing.T) Fixture

// Run executes the demo baseline, not the complete production qualification matrix.
// Calls get a 30-second context. Adapters must honor it; use go test -timeout as
// the outer watchdog for a broken implementation. Cases are intentionally serial.
func Run(t *testing.T, factory Factory) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := factory(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			if err := c.check(ctx, f); err != nil {
				t.Fatal(err)
			}
		})
	}
}

var cases = []struct {
	name  string
	check func(context.Context, Fixture) error
}{
	{"descriptor", descriptor},
	{"negotiation", negotiation},
	{"lifecycle", lifecycle},
	{"replay_and_stale_handle", replay},
	{"unsupported_optional_operations", unsupported},
}

func failure(step string) error { return errors.New("harness conformance: " + step) }

func descriptor(ctx context.Context, f Fixture) error {
	d, err := f.Adapter.Descriptor(ctx)
	if err != nil {
		return failure("descriptor failed")
	}
	before, err := json.Marshal(d)
	if err != nil {
		return failure("invalid descriptor")
	}
	d, err = f.Adapter.Descriptor(ctx)
	if err != nil {
		return failure("descriptor repeat failed")
	}
	after, err := json.Marshal(d)
	if err != nil || string(before) != string(after) {
		return failure("descriptor changed")
	}
	if d.Capabilities.Require(harness.StructuredEvents, harness.Streaming, harness.Interrupt) != nil {
		return failure("demo capabilities unavailable")
	}
	r, err := harnessregistry.New(ctx, harnessregistry.Registration{Adapter: f.Adapter, BuildDigest: f.Compatibility.AdapterBuildDigest})
	if err != nil {
		return failure("descriptor registration rejected")
	}
	w := f.WantCompatibility
	if _, err := r.Resolve(harnessregistry.Selection{Kind: w.AdapterKind, BuildDigest: w.AdapterBuildDigest,
		ContractVersion: w.ContractVersion, HarnessVersion: w.HarnessProtocol, AgentdVersion: w.AgentdProtocol,
		VendorStateFormat: w.VendorStateFormat, RequiredCapabilities: f.Compatibility.RequiredCapabilities,
		AllowPrerelease: f.Compatibility.AllowPrerelease}); err != nil {
		return failure("descriptor selection rejected")
	}
	return nil
}

func negotiation(ctx context.Context, f Fixture) error {
	for _, mutate := range []func(*harness.CompatibilityRequest){
		func(r *harness.CompatibilityRequest) { r.AdapterKind = "conformance-wrong-kind" },
		func(r *harness.CompatibilityRequest) { r.ContractVersion = "999999.0.0" },
		func(r *harness.CompatibilityRequest) {
			r.RequiredCapabilities = []harness.HarnessCapability{"conformance-unknown"}
		},
	} {
		r := f.Compatibility
		mutate(&r)
		if _, err := f.Adapter.Negotiate(ctx, r); !errors.Is(err, harness.ErrIncompatible) && !errors.Is(err, harness.ErrUnsupported) {
			return failure("incompatible negotiation accepted or wrong error class")
		}
	}
	w, err := f.Adapter.Negotiate(ctx, f.Compatibility)
	if err != nil || !reflect.DeepEqual(w, f.WantCompatibility) {
		return failure("negotiated result mismatch")
	}
	return nil
}

func start(ctx context.Context, f Fixture) (harness.HarnessHandle, error) {
	result, err := f.Adapter.Negotiate(ctx, f.Compatibility)
	if err != nil || !reflect.DeepEqual(result, f.WantCompatibility) {
		return harness.HarnessHandle{}, failure("negotiate before start")
	}
	h, err := f.Adapter.Start(ctx, f.Start)
	if err != nil {
		return h, failure("start failed")
	}
	if h.ID != f.Start.HandleID || h.Fence != f.Start.Fence || h.AdapterKind != result.AdapterKind ||
		h.AdapterBuildDigest != result.AdapterBuildDigest || h.NegotiationDigest != result.ResultDigest ||
		h.VendorSessionReference == "" || !validID(h.ProcessInstanceID) {
		return h, failure("start handle binding")
	}
	return h, nil
}

func cleanup(f Fixture, h harness.HarnessHandle) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = f.Adapter.Close(ctx, h, f.Close) // Factory cleanup remains mandatory on failure.
}

func lifecycle(ctx context.Context, f Fixture) error {
	h, err := start(ctx, f)
	if err != nil {
		return err
	}
	defer cleanup(f, h)
	status, err := f.Adapter.Status(ctx, h)
	if err != nil || status.State != runtimebinding.HarnessReady {
		return failure("start did not reach protocol readiness")
	}
	stream, err := f.Adapter.Execute(ctx, h, f.Execute)
	if err != nil || stream == nil {
		return failure("execute failed")
	}
	defer func() { _ = stream.Close() }()
	d, err := f.Adapter.Descriptor(ctx)
	if err != nil {
		return failure("event limits unavailable")
	}
	var previous harness.HarnessEvent
	for i := uint64(1); i <= 2; i++ {
		e, err := stream.Next(ctx)
		if err != nil {
			return failure("expected fixture event missing")
		}
		if !validID(e.EventID) || !validID(e.StreamID) || e.Sequence != i || e.Handle != h || e.Operation != f.Execute.Operation ||
			e.Type == "" || e.SchemaVersion == "" || e.OccurredAt.IsZero() || e.ObservedAt.IsZero() ||
			(e.Content.Classification != runtimeevent.Public && e.Content.Classification != runtimeevent.Internal && e.Content.Classification != runtimeevent.Confidential) ||
			len(e.Content.Inline) > d.Limits.EventBytes || (len(e.Content.Inline) > 0 && e.Content.ArtifactReference != "") ||
			(i > 1 && (e.StreamID != previous.StreamID || e.EventID == previous.EventID)) {
			return failure("event envelope integrity")
		}
		previous = e
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := stream.Next(cancelled); !errors.Is(err, context.Canceled) {
		return failure("stream cancellation")
	}
	if err := f.Adapter.Interrupt(ctx, h, f.Interrupt); err != nil {
		return failure("interrupt failed")
	}
	if err := f.Adapter.Close(ctx, h, f.Close); err != nil {
		return failure("close failed")
	}
	if err := f.Adapter.Close(ctx, h, f.Close); err != nil {
		return failure("close replay failed")
	}
	return nil
}

func replay(ctx context.Context, f Fixture) error {
	h, err := start(ctx, f)
	if err != nil {
		return err
	}
	defer cleanup(f, h)
	repeated, err := f.Adapter.Start(ctx, f.Start)
	if err != nil || repeated != h {
		return failure("start replay changed handle")
	}
	conflict := f.Start
	conflict.Operation.RequestDigest = otherDigest(conflict.Operation.RequestDigest)
	if _, err := f.Adapter.Start(ctx, conflict); !errors.Is(err, harness.ErrConflict) {
		return failure("start conflict accepted")
	}
	stale := h
	stale.Fence.Generation++
	if _, err := f.Adapter.Status(ctx, stale); !errors.Is(err, harness.ErrStale) {
		return failure("stale status accepted")
	}
	if _, err := f.Adapter.Execute(ctx, stale, f.Execute); !errors.Is(err, harness.ErrStale) {
		return failure("stale execute accepted")
	}
	s, err := f.Adapter.Execute(ctx, h, f.Execute)
	if err != nil || s == nil {
		return failure("execute before replay")
	}
	defer func() { _ = s.Close() }()
	first, err := s.Next(ctx)
	if err != nil {
		return failure("first operation event")
	}
	repeatedStream, err := f.Adapter.Execute(ctx, h, f.Execute)
	if err != nil || repeatedStream == nil {
		return failure("execute replay failed")
	}
	defer func() { _ = repeatedStream.Close() }()
	replayed, err := repeatedStream.Next(ctx)
	// Replay may return an earlier event or reconnect farther along the same stream.
	if err != nil || replayed.StreamID != first.StreamID || replayed.Operation != first.Operation || replayed.Handle != h {
		return failure("execute replay changed operation stream")
	}
	conflictingInput := f.Execute
	conflictingInput.Operation.RequestDigest = otherDigest(conflictingInput.Operation.RequestDigest)
	if _, err := f.Adapter.Execute(ctx, h, conflictingInput); !errors.Is(err, harness.ErrConflict) {
		return failure("execute conflict accepted")
	}
	return nil
}

func validID(id primitives.ID) bool { _, err := primitives.ParseID(string(id)); return err == nil }

// This case checks refusals only. Advertising an optional feature still needs
// its positive conformance cases before that feature is qualified.
func unsupported(ctx context.Context, f Fixture) error {
	h, err := start(ctx, f)
	if err != nil {
		return err
	}
	defer cleanup(f, h)
	d, err := f.Adapter.Descriptor(ctx)
	if err != nil {
		return failure("optional capability descriptor")
	}
	if !d.Capabilities.Supports(harness.Resume) {
		r := harness.ResumeHarnessRequest{StartHarnessRequest: f.Start}
		r.Mutation = f.Execute.Mutation
		if _, err := f.Adapter.Resume(ctx, r); !errors.Is(err, harness.ErrUnsupported) {
			return failure("unsupported resume accepted")
		}
	}
	if !d.Capabilities.Supports(harness.Signals) {
		if err := f.Adapter.Signal(ctx, h, harness.SignalRequest{Mutation: f.Execute.Mutation, Kind: "additional-input", Input: f.Execute.Input}); !errors.Is(err, harness.ErrUnsupported) {
			return failure("unsupported signal accepted")
		}
	}
	if !d.Capabilities.Supports(harness.CheckpointPrepare) {
		if _, err := f.Adapter.PrepareCheckpoint(ctx, h, harness.CheckpointRequest{Mutation: f.Execute.Mutation, Format: f.Compatibility.CheckpointFormat}); !errors.Is(err, harness.ErrUnsupported) {
			return failure("unsupported checkpoint accepted")
		}
	}
	status, err := f.Adapter.Status(ctx, h)
	if err != nil || status.State != runtimebinding.HarnessReady {
		return failure("unsupported request changed ready state")
	}
	return nil
}

func otherDigest(current string) string {
	d := "sha256:" + strings.Repeat("a", 64)
	if d == current {
		return "sha256:" + strings.Repeat("b", 64)
	}
	return d
}
