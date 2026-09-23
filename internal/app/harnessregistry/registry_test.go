package harnessregistry

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
)

const build = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// Only Descriptor may be called by registry construction/selection. An accidental
// call to any embedded lifecycle method fails this test instead of starting work.
type adapter struct {
	harness.HarnessAdapter
	d   harness.AdapterDescriptor
	err error
}

func (a *adapter) Descriptor(context.Context) (harness.AdapterDescriptor, error) { return a.d, a.err }

func fixture() (*adapter, Selection) {
	a := &adapter{d: harness.AdapterDescriptor{
		Kind: "codex", ImplementationVersion: "0.1.0", ContractVersion: "1.0.0",
		HarnessProtocols:   []harness.VersionRange{{Minimum: "1.0.0", Maximum: "1.2.0"}},
		AgentdProtocols:    []harness.VersionRange{{Minimum: "1.0.0", Maximum: "1.0.0"}},
		VendorStateFormats: []string{"codex.v1"},
		Capabilities:       harness.Capabilities{harness.StructuredEvents: harness.Required, harness.Interrupt: harness.Supported},
		Limits: harness.AdapterLimits{InputBytes: 1024, InputItems: 1, EventBytes: 1024, EventsPerSecond: 10,
			BufferedEvents: 8, SignalBytes: 128, DiagnosticBytes: 128, VendorStatePaths: 1,
			ShutdownTimeout: time.Second, CheckpointTimeout: time.Second},
	}}
	return a, Selection{Kind: "codex", BuildDigest: build, ContractVersion: "1.0.0", HarnessVersion: "1.1.0",
		AgentdVersion: "1.0.0", VendorStateFormat: "codex.v1", RequiredCapabilities: []harness.HarnessCapability{harness.StructuredEvents}}
}

func TestPinnedSelection(t *testing.T) {
	a, s := fixture()
	r, err := New(context.Background(), Registration{a, build})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		s.HarnessVersion = v
		got, err := r.Resolve(s)
		if err != nil || got != a {
			t.Fatalf("%s: %v", v, err)
		}
	}
}

func TestSchemaIdentifiersAndStateFormats(t *testing.T) {
	a, s := fixture()
	a.d.Kind, s.Kind = "custom_adapter.v1", "custom_adapter.v1"
	a.d.VendorStateFormats = []string{"1.0.0", "Vendor/state-v1"}
	s.VendorStateFormat = "Vendor/state-v1"
	r, err := New(context.Background(), Registration{a, build})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.Resolve(s); err != nil || got != a {
		t.Fatal(err)
	}
}

func TestSelectionRejectsWithoutFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Selection)
		want   error
	}{
		{"wrong kind", func(s *Selection) { s.Kind = "other" }, harness.ErrIncompatible},
		{"unpinned", func(s *Selection) { s.BuildDigest = "" }, harness.ErrInvalid},
		{"wrong build", func(s *Selection) { s.BuildDigest = "sha256:" + strings.Repeat("b", 64) }, harness.ErrIncompatible},
		{"contract major", func(s *Selection) { s.ContractVersion = "2.0.0" }, harness.ErrIncompatible},
		{"unknown contract minor", func(s *Selection) { s.ContractVersion = "1.1.0" }, harness.ErrIncompatible},
		{"above range", func(s *Selection) { s.HarnessVersion = "1.2.1" }, harness.ErrIncompatible},
		{"below range", func(s *Selection) { s.HarnessVersion = "0.9.0" }, harness.ErrIncompatible},
		{"wrong agentd", func(s *Selection) { s.AgentdVersion = "2.0.0" }, harness.ErrIncompatible},
		{"state format", func(s *Selection) { s.VendorStateFormat = "other.v1" }, harness.ErrIncompatible},
		{"unknown capability", func(s *Selection) { s.RequiredCapabilities = []harness.HarnessCapability{"future"} }, harness.ErrIncompatible},
		{"unsupported capability", func(s *Selection) { s.RequiredCapabilities = []harness.HarnessCapability{harness.NativeFork} }, harness.ErrIncompatible},
		{"prerelease", func(s *Selection) { s.HarnessVersion = "1.1.0-rc.1" }, harness.ErrIncompatible},
		{"expression", func(s *Selection) { s.HarnessVersion = ">=1.0.0" }, harness.ErrInvalid},
		{"short version", func(s *Selection) { s.HarnessVersion = "1.0" }, harness.ErrInvalid},
		{"oversized", func(s *Selection) { s.HarnessVersion = strings.Repeat("1", 129) }, harness.ErrInvalid},
		{"noncanonical", func(s *Selection) { s.HarnessVersion = "v1.0.0" }, harness.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, s := fixture()
			r, err := New(context.Background(), Registration{a, build})
			if err != nil {
				t.Fatal(err)
			}
			tc.change(&s)
			got, err := r.Resolve(s)
			if got != nil || !errors.Is(err, tc.want) {
				t.Fatalf("got adapter=%v err=%v", got != nil, err)
			}
		})
	}
}

func TestRegistrationRejectsInvalidDescriptors(t *testing.T) {
	for _, change := range []func(*adapter){
		func(a *adapter) { a.d.Kind = "../../codex" },
		func(a *adapter) { a.d.HarnessProtocols[0].Maximum = "2.0.0" },
		func(a *adapter) { a.d.HarnessProtocols[0].Maximum = "0.9.0" },
		func(a *adapter) { a.d.HarnessProtocols = nil },
		func(a *adapter) { a.d.AgentdProtocols[0].Minimum = "unknown" },
		func(a *adapter) { a.d.Capabilities["unknown"] = harness.Supported },
		func(a *adapter) { a.d.Limits.BufferedEvents = 0 },
		func(a *adapter) { a.d.VendorStateFormats = nil },
		func(a *adapter) { a.err = errors.New("sensitive vendor diagnostic") },
	} {
		a, _ := fixture()
		change(a)
		if _, err := New(context.Background(), Registration{a, build}); err != harness.ErrInvalid {
			t.Fatalf("unsafe descriptor accepted: %v", err)
		}
	}
	var nilPointer *adapter
	for _, a := range []harness.HarnessAdapter{nil, nilPointer} {
		if _, err := New(context.Background(), Registration{a, build}); err != harness.ErrInvalid {
			t.Fatal(err)
		}
	}
	a, _ := fixture()
	if _, err := New(context.Background(), Registration{a, build}, Registration{a, build}); err != harness.ErrConflict {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), make([]Registration, 33)...); err != harness.ErrInvalid {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(ctx, Registration{a, build}); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestSnapshotAndConcurrentResolution(t *testing.T) {
	a, s := fixture()
	b, _ := fixture()
	otherBuild := "sha256:" + strings.Repeat("b", 64)
	r, err := New(context.Background(), Registration{a, build}, Registration{b, otherBuild})
	if err != nil {
		t.Fatal(err)
	}
	// The registry owns copies of descriptor maps/slices; later caller mutation
	// cannot broaden the selected implementation's advertised support.
	a.d.HarnessProtocols[0].Maximum = "9.0.0"
	a.d.AgentdProtocols[0].Maximum = "9.0.0"
	a.d.VendorStateFormats[0] = "changed"
	a.d.Capabilities[harness.NativeFork] = harness.Supported
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			got, err := r.Resolve(s)
			if err != nil || got != a {
				t.Errorf("snapshot lookup: %v", err)
			}
		})
	}
	wg.Wait()
	s.RequiredCapabilities = []harness.HarnessCapability{harness.NativeFork}
	if _, err := r.Resolve(s); err != harness.ErrIncompatible {
		t.Fatal(err)
	}
	s.RequiredCapabilities = nil
	s.BuildDigest = otherBuild
	if got, err := r.Resolve(s); err != nil || got != b {
		t.Fatal("build pin ignored")
	}
}

func TestPrereleaseRequiresExplicitAllowance(t *testing.T) {
	a, s := fixture()
	a.d.ImplementationVersion = "0.1.0-rc.1"
	r, err := New(context.Background(), Registration{a, build})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(s); err != harness.ErrIncompatible {
		t.Fatal(err)
	}
	s.AllowPrerelease = true
	s.HarnessVersion = "1.1.0-rc.1"
	if got, err := r.Resolve(s); err != nil || got != a {
		t.Fatal(err)
	}
}
