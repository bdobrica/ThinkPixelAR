package harness

import (
	"errors"
	"testing"
)

func TestCapabilityGateFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		caps    Capabilities
		request HarnessCapability
		want    error
	}{
		{"absent", nil, Interrupt, ErrUnsupported},
		{"unsupported", Capabilities{Interrupt: Unsupported}, Interrupt, ErrUnsupported},
		{"supported", Capabilities{Interrupt: Supported}, Interrupt, nil},
		{"required implementation", Capabilities{Interrupt: Required}, Interrupt, nil},
		{"unknown requirement", Capabilities{Interrupt: Supported}, "future-capability", ErrUnsupported},
		{"unknown declaration", Capabilities{"future-capability": Supported}, Interrupt, ErrInvalid},
		{"invalid level", Capabilities{Interrupt: "TRUE"}, Interrupt, ErrInvalid},
		{"fork not implied", Capabilities{Resume: Supported}, NativeFork, ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.caps.Require(tc.request); !errors.Is(err, tc.want) {
				t.Fatalf("gate returned %v; want %v", err, tc.want)
			}
			if tc.want != nil && tc.caps.Supports(tc.request) {
				t.Fatal("invalid/unsupported capability advertised")
			}
		})
	}
}

func TestDemoCapabilitiesDoNotRequireOptionalFeatures(t *testing.T) {
	caps := Capabilities{StructuredEvents: Required, Streaming: Supported, Interrupt: Supported}
	if err := caps.Require(StructuredEvents, Streaming, Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := caps.Require(StructuredEvents, CheckpointPrepare); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if caps.Supports(NativeFork) || caps.Supports(Resume) {
		t.Fatal("demo capabilities expanded")
	}
}
