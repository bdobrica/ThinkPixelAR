// Package protocol validates compatibility, not authentication or authority.
package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"slices"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const Major uint32 = 1
const Minor uint32 = 0
const MaxFrameBytes = 1 << 20

var ErrHandshake = errors.New("agentd compatibility handshake rejected")
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)

// Expected is derived by the caller from authenticated binding and immutable
// materialization evidence. It must never be constructed from Hello identity.
// This helper neither consumes bootstrap proofs nor accepts a transport stream.
type Expected struct {
	Binding               *agentdv1.Binding
	Challenge             []byte
	BuildDigest           string
	AdapterKind           string
	AdapterDigest         string
	SupportedCapabilities []string
	RequiredCapabilities  []string
	Limits                *agentdv1.Limits
}

func HardLimits() *agentdv1.Limits {
	return &agentdv1.Limits{FrameBytes: MaxFrameBytes, CommandBytes: 256 << 10, EventBytes: 256 << 10, DiagnosticBytes: 16 << 10, MutatingInflight: 1, ReadonlyInflight: 32, BufferedEvents: 256, BufferedBytes: 16 << 20, HeartbeatIntervalMs: 10000, LivenessWindowMs: 30000}
}

// DecodeHello bounds allocation and rejects unknown required/future fields in
// this first minor version. Future additive semantics require an explicit change.
func DecodeHello(raw []byte) (*agentdv1.Hello, error) {
	if len(raw) == 0 || len(raw) > MaxFrameBytes {
		return nil, ErrHandshake
	}
	h := &agentdv1.Hello{}
	if (proto.UnmarshalOptions{RecursionLimit: 16}).Unmarshal(raw, h) != nil || !known(h.ProtoReflect()) {
		return nil, ErrHandshake
	}
	return h, nil
}

// Negotiate selects the supported intersection and binds it to a caller-issued
// connection ID/epoch. The result is compatibility evidence only; launch still
// requires transport, durable fences, authority and a current command.
func Negotiate(h *agentdv1.Hello, e Expected, connectionID string, epoch uint64) (*agentdv1.Welcome, error) {
	if h == nil || proto.Size(h) > MaxFrameBytes || !known(h.ProtoReflect()) || h.Versions == nil || h.Versions.Major != Major || h.Versions.MinimumMinor > Minor || h.Versions.MinimumMinor > h.Versions.MaximumMinor || !validBinding(e.Binding) || !proto.Equal(h.Binding, e.Binding) || len(e.Challenge) != 32 || !bytes.Equal(h.Challenge, e.Challenge) || !digestPattern.MatchString(e.BuildDigest) || h.BuildDigest != e.BuildDigest || !namePattern.MatchString(e.AdapterKind) || h.AdapterKind != e.AdapterKind || !digestPattern.MatchString(e.AdapterDigest) || h.AdapterDigest != e.AdapterDigest || !validID(connectionID) || epoch == 0 || (len(h.BootstrapProof) != 0 && len(h.BootstrapProof) != 32) {
		return nil, ErrHandshake
	}
	if !capabilities(h.SupportedCapabilities, h.RequiredCapabilities) || !capabilities(e.SupportedCapabilities, e.RequiredCapabilities) {
		return nil, ErrHandshake
	}
	common := []string{}
	for _, c := range e.SupportedCapabilities {
		if slices.Contains(h.SupportedCapabilities, c) {
			common = append(common, c)
		}
	}
	for _, required := range [][]string{h.RequiredCapabilities, e.RequiredCapabilities} {
		for _, c := range required {
			if !slices.Contains(common, c) {
				return nil, ErrHandshake
			}
		}
	}
	slices.Sort(common)
	limits, err := intersectLimits(h.Limits, e.Limits)
	if err != nil {
		return nil, err
	}
	w := &agentdv1.Welcome{Major: Major, Minor: Minor, Binding: proto.Clone(e.Binding).(*agentdv1.Binding), Challenge: bytes.Clone(e.Challenge), ConnectionId: connectionID, ConnectionEpoch: epoch, Capabilities: common, Limits: limits, BuildDigest: e.BuildDigest, AdapterKind: e.AdapterKind, AdapterDigest: e.AdapterDigest}
	w.NegotiationDigest = negotiationDigest(w)
	return w, nil
}

// VerifyWelcome recomputes compatibility using independently configured agentd
// expectations and its own Hello. Server identity/epoch freshness are checked by
// the transport; payload agreement does not authenticate an endpoint.
func VerifyWelcome(h *agentdv1.Hello, e Expected, w *agentdv1.Welcome) error {
	if w == nil || !known(w.ProtoReflect()) {
		return ErrHandshake
	}
	expected, err := Negotiate(h, e, w.ConnectionId, w.ConnectionEpoch)
	if err != nil || !proto.Equal(expected, w) {
		return ErrHandshake
	}
	return nil
}
func negotiationDigest(w *agentdv1.Welcome) string {
	copy := proto.Clone(w).(*agentdv1.Welcome)
	copy.NegotiationDigest = ""
	// Nonces prove freshness, not durable evidence. Do not retain their digest.
	copy.Challenge = nil
	raw, _ := (proto.MarshalOptions{Deterministic: true}).Marshal(copy)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func validID(s string) bool { _, err := primitives.ParseID(s); return err == nil }
func validBinding(b *agentdv1.Binding) bool {
	return b != nil && known(b.ProtoReflect()) && validID(b.TenantId) && validID(b.SessionId) && validID(b.ExecutionId) && validID(b.AttemptId) && validID(b.SandboxBindingId) && b.SessionGeneration > 0
}
func capabilities(supported, required []string) bool {
	if len(supported) == 0 || len(supported) > 32 || len(required) > 32 {
		return false
	}
	for _, list := range [][]string{supported, required} {
		seen := map[string]bool{}
		for _, c := range list {
			if !namePattern.MatchString(c) || seen[c] {
				return false
			}
			seen[c] = true
		}
	}
	for _, c := range required {
		if !slices.Contains(supported, c) {
			return false
		}
	}
	return true
}
func intersectLimits(a, b *agentdv1.Limits) (*agentdv1.Limits, error) {
	if a == nil || b == nil || !known(a.ProtoReflect()) || !known(b.ProtoReflect()) {
		return nil, ErrHandshake
	}
	out := &agentdv1.Limits{}
	hard := HardLimits().ProtoReflect()
	fields := hard.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		av, bv, max := a.ProtoReflect().Get(f).Uint(), b.ProtoReflect().Get(f).Uint(), hard.Get(f).Uint()
		if av == 0 || bv == 0 || av > max || bv > max {
			return nil, ErrHandshake
		}
		out.ProtoReflect().Set(f, protoreflect.ValueOfUint32(uint32(min(av, bv))))
	}
	if out.LivenessWindowMs < 2*out.HeartbeatIntervalMs || out.CommandBytes >= out.FrameBytes || out.EventBytes >= out.FrameBytes || out.DiagnosticBytes > out.EventBytes || out.EventBytes > out.BufferedBytes {
		return nil, ErrHandshake
	}
	return out, nil
}
func known(m protoreflect.Message) bool {
	if len(m.GetUnknown()) != 0 {
		return false
	}
	ok := true
	m.Range(func(f protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if f.Kind() == protoreflect.MessageKind {
			if f.IsList() {
				for i := 0; i < v.List().Len(); i++ {
					if !known(v.List().Get(i).Message()) {
						ok = false
						return false
					}
				}
			} else {
				ok = known(v.Message())
			}
		}
		return ok
	})
	return ok
}
