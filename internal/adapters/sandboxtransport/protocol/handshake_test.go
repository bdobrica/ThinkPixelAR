package protocol

import (
	"bytes"
	"strings"
	"testing"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

const testID = "01950000-0000-7000-8000-000000000001"

func fixture() (*agentdv1.Hello, Expected) {
	b := &agentdv1.Binding{TenantId: testID, SessionId: testID, ExecutionId: testID, AttemptId: testID, SandboxBindingId: testID, SessionGeneration: 1}
	e := Expected{Binding: b, Challenge: bytes.Repeat([]byte{7}, 32), BuildDigest: "sha256:" + strings.Repeat("a", 64), AdapterKind: "test", AdapterDigest: "sha256:" + strings.Repeat("b", 64), SupportedCapabilities: []string{"envelope.v1", "heartbeat.v1"}, RequiredCapabilities: []string{"envelope.v1"}, Limits: HardLimits()}
	h := &agentdv1.Hello{Versions: &agentdv1.VersionRange{Major: 1, MaximumMinor: 2}, Binding: proto.Clone(b).(*agentdv1.Binding), Challenge: bytes.Clone(e.Challenge), BuildDigest: e.BuildDigest, AdapterKind: e.AdapterKind, AdapterDigest: e.AdapterDigest, SupportedCapabilities: []string{"heartbeat.v1", "envelope.v1", "future.optional"}, RequiredCapabilities: []string{"envelope.v1"}, Limits: HardLimits(), BootstrapProof: bytes.Repeat([]byte{9}, 32)}
	return h, e
}
func TestNegotiationBindsExpectedIdentityAndNarrowsLimits(t *testing.T) {
	h, e := fixture()
	h.Limits.EventBytes = 128 << 10
	raw, _ := proto.Marshal(h)
	decoded, err := DecodeHello(raw)
	if err != nil {
		t.Fatal(err)
	}
	w, err := Negotiate(decoded, e, testID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if w.Minor != 0 || len(w.Capabilities) != 2 || w.Limits.EventBytes != 128<<10 || !digestPattern.MatchString(w.NegotiationDigest) {
		t.Fatal("invalid selection")
	}
	if VerifyWelcome(h, e, w) != nil {
		t.Fatal("welcome rejected")
	}
	h.SupportedCapabilities = []string{"future.optional", "envelope.v1", "heartbeat.v1"}
	h.BootstrapProof = bytes.Repeat([]byte{8}, 32)
	again, err := Negotiate(h, e, testID, 1)
	if err != nil || again.NegotiationDigest != w.NegotiationDigest {
		t.Fatal("noncanonical or secret-dependent evidence")
	}
	w.Binding.SessionGeneration++
	if VerifyWelcome(h, e, w) == nil {
		t.Fatal("tampered welcome accepted")
	}
	if e.Binding.SessionGeneration != 1 {
		t.Fatal("shared mutable binding")
	}
}
func TestHandshakeRejectsIncompatibleOrHostileHello(t *testing.T) {
	changes := map[string]func(*agentdv1.Hello){
		"major":           func(h *agentdv1.Hello) { h.Versions.Major = 2 },
		"minor":           func(h *agentdv1.Hello) { h.Versions.MinimumMinor = 1 },
		"range":           func(h *agentdv1.Hello) { h.Versions.MinimumMinor = 3 },
		"missing version": func(h *agentdv1.Hello) { h.Versions = nil },
		"tenant":          func(h *agentdv1.Hello) { h.Binding.TenantId = "01950000-0000-7000-8000-000000000002" },
		"attempt":         func(h *agentdv1.Hello) { h.Binding.AttemptId = "01950000-0000-7000-8000-000000000002" },
		"generation":      func(h *agentdv1.Hello) { h.Binding.SessionGeneration = 2 },
		"challenge":       func(h *agentdv1.Hello) { h.Challenge[0]++ },
		"build":           func(h *agentdv1.Hello) { h.BuildDigest = "sha256:" + strings.Repeat("c", 64) },
		"adapter":         func(h *agentdv1.Hello) { h.AdapterDigest = "sha256:" + strings.Repeat("c", 64) },
		"required":        func(h *agentdv1.Hello) { h.RequiredCapabilities = append(h.RequiredCapabilities, "future.optional") },
		"duplicate":       func(h *agentdv1.Hello) { h.SupportedCapabilities = append(h.SupportedCapabilities, "envelope.v1") },
		"unoffered":       func(h *agentdv1.Hello) { h.RequiredCapabilities = []string{"unknown.required"} },
		"huge capability": func(h *agentdv1.Hello) {
			h.SupportedCapabilities = append(h.SupportedCapabilities, strings.Repeat("x", 65))
		},
		"unknown": func(h *agentdv1.Hello) {
			h.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 100, protowire.VarintType), 1))
		},
		"nested unknown": func(h *agentdv1.Hello) { h.Binding.ProtoReflect().SetUnknown([]byte{0x78, 1}) },
		"ceiling":        func(h *agentdv1.Hello) { h.Limits.FrameBytes++ },
		"unbounded":      func(h *agentdv1.Hello) { h.Limits.BufferedBytes = 0 },
		"heartbeat":      func(h *agentdv1.Hello) { h.Limits.LivenessWindowMs = 1000 },
		"frame overhead": func(h *agentdv1.Hello) { h.Limits.FrameBytes = h.Limits.CommandBytes },
		"missing limits": func(h *agentdv1.Hello) { h.Limits = nil },
		"proof":          func(h *agentdv1.Hello) { h.BootstrapProof = []byte{1} },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			h, e := fixture()
			change(h)
			if _, err := Negotiate(h, e, testID, 1); err != ErrHandshake {
				t.Fatal("hostile hello accepted", err)
			}
		})
	}
}
func TestHandshakeRejectsInvalidTrustedConfiguration(t *testing.T) {
	for _, change := range []func(*Expected){func(e *Expected) { e.Binding = nil }, func(e *Expected) { e.Challenge = nil }, func(e *Expected) { e.BuildDigest = "latest" }, func(e *Expected) { e.RequiredCapabilities = []string{"not.offered"} }, func(e *Expected) { e.Limits.FrameBytes++ }} {
		h, e := fixture()
		change(&e)
		if _, err := Negotiate(h, e, testID, 1); err == nil {
			t.Fatal("invalid expected config accepted")
		}
	}
	h, e := fixture()
	if _, err := Negotiate(h, e, testID, 0); err == nil {
		t.Fatal("zero epoch")
	}
	if _, err := Negotiate(h, e, "", 1); err == nil {
		t.Fatal("missing connection")
	}
}
func TestDecodeBounds(t *testing.T) {
	for _, raw := range [][]byte{nil, {0xff}, bytes.Repeat([]byte{0}, MaxFrameBytes+1), {0xa0, 0x06, 1}} {
		if _, err := DecodeHello(raw); err == nil {
			t.Fatal("invalid wire accepted")
		}
	}
}
func FuzzHandshake(f *testing.F) {
	h, _ := fixture()
	raw, _ := proto.Marshal(h)
	f.Add(raw)
	f.Fuzz(func(t *testing.T, raw []byte) {
		h, err := DecodeHello(raw)
		if err != nil {
			return
		}
		_, e := fixture()
		w, err := Negotiate(h, e, testID, 1)
		if err == nil && VerifyWelcome(h, e, w) != nil {
			t.Fatal("negotiation not self consistent")
		}
	})
}
