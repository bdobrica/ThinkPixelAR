// Package harnessregistry selects explicitly installed, immutable harness adapters.
package harnessregistry

import (
	"context"
	"reflect"
	"regexp"
	"slices"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
	"golang.org/x/mod/semver"
)

var (
	kindPattern   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,126}[a-z0-9])?$`)
	formatPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Registration comes from trusted composition, never sandbox discovery. The
// build digest binds the installed adapter, not a claim from Descriptor.
type Registration struct {
	Adapter     harness.HarnessAdapter
	BuildDigest string
}

type entry struct {
	Registration
	descriptor harness.AdapterDescriptor
}

// Registry is immutable after construction and safe for concurrent resolution.
// Installed adapters must keep their build/configuration and descriptor immutable.
type Registry struct{ entries map[string][]entry }

// New snapshots bounded descriptors. It neither negotiates nor starts a process.
// No default adapter, dynamic loading, or latest-version selection is provided.
func New(ctx context.Context, registrations ...Registration) (*Registry, error) {
	if len(registrations) == 0 || len(registrations) > 32 {
		return nil, harness.ErrInvalid
	}
	r := &Registry{entries: make(map[string][]entry)}
	for _, registration := range registrations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if nilAdapter(registration.Adapter) || !digestPattern.MatchString(registration.BuildDigest) {
			return nil, harness.ErrInvalid
		}
		d, err := registration.Adapter.Descriptor(ctx)
		if err != nil {
			return nil, harness.ErrInvalid
		} // Never reflect vendor diagnostics.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !validDescriptor(d) {
			return nil, harness.ErrInvalid
		}
		for _, prior := range r.entries[d.Kind] {
			if prior.BuildDigest == registration.BuildDigest {
				return nil, harness.ErrConflict
			}
		}
		d.HarnessProtocols = slices.Clone(d.HarnessProtocols)
		d.AgentdProtocols = slices.Clone(d.AgentdProtocols)
		d.VendorStateFormats = slices.Clone(d.VendorStateFormats)
		capabilities := make(harness.Capabilities, len(d.Capabilities))
		for name, level := range d.Capabilities {
			capabilities[name] = level
		}
		d.Capabilities = capabilities
		r.entries[d.Kind] = append(r.entries[d.Kind], entry{registration, d})
	}
	return r, nil
}

// Selection pins actual packaged versions. Ranges belong to descriptors; callers
// cannot use a wildcard to select an arbitrary installed artifact. ContractVersion
// is exact for this initial lane. BuildDigest is mandatory even with one adapter.
type Selection struct {
	Kind, BuildDigest, ContractVersion               string
	HarnessVersion, AgentdVersion, VendorStateFormat string
	RequiredCapabilities                             []harness.HarnessCapability
	AllowPrerelease                                  bool
}

// Resolve only selects an implementation. Call its Negotiate before acquisition
// and verify the actual authenticated handshake before work. It grants no authority.
func (r *Registry) Resolve(s Selection) (harness.HarnessAdapter, error) {
	if !kindPattern.MatchString(s.Kind) || !digestPattern.MatchString(s.BuildDigest) ||
		len(s.RequiredCapabilities) > 12 || !formatPattern.MatchString(s.VendorStateFormat) {
		return nil, harness.ErrInvalid
	}
	contract, cv := parse(s.ContractVersion)
	hv, hok := parse(s.HarnessVersion)
	av, aok := parse(s.AgentdVersion)
	if !cv || !hok || !aok {
		return nil, harness.ErrInvalid
	}
	if !s.AllowPrerelease && (semver.Prerelease(contract) != "" || semver.Prerelease(hv) != "" || semver.Prerelease(av) != "") {
		return nil, harness.ErrIncompatible
	}
	if r == nil {
		return nil, harness.ErrIncompatible
	}
	for _, candidate := range r.entries[s.Kind] {
		d := candidate.descriptor
		if candidate.BuildDigest != s.BuildDigest {
			continue
		}
		iv, _ := parse(d.ImplementationVersion)
		if (!s.AllowPrerelease && semver.Prerelease(iv) != "") || d.ContractVersion != s.ContractVersion ||
			!contains(d.HarnessProtocols, hv) || !contains(d.AgentdProtocols, av) ||
			!slices.Contains(d.VendorStateFormats, s.VendorStateFormat) || d.Capabilities.Require(s.RequiredCapabilities...) != nil {
			return nil, harness.ErrIncompatible
		}
		return candidate.Adapter, nil
	}
	return nil, harness.ErrIncompatible
}

func nilAdapter(a harness.HarnessAdapter) bool {
	if a == nil {
		return true
	}
	v := reflect.ValueOf(a)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

// Use the pinned semantic-version library with a byte bound and canonical
// spelling: no whitespace, v prefix, shorthand, wildcard or expression grammar.
func parse(s string) (string, bool) {
	if len(s) == 0 || len(s) > 128 {
		return "", false
	}
	v := "v" + s // The library requires this prefix; our contract does not.
	return v, semver.IsValid(v) && semver.Canonical(v)+semver.Build(v) == v
}

func validRanges(ranges []harness.VersionRange) bool {
	if len(ranges) == 0 || len(ranges) > 16 {
		return false
	}
	for _, r := range ranges {
		lo, lok := parse(r.Minimum)
		hi, hik := parse(r.Maximum)
		if !lok || !hik || semver.Major(lo) != semver.Major(hi) || semver.Compare(hi, lo) < 0 {
			return false
		}
	}
	return true
}

func contains(ranges []harness.VersionRange, v string) bool {
	for _, r := range ranges {
		lo, _ := parse(r.Minimum)
		hi, _ := parse(r.Maximum)
		if semver.Major(v) == semver.Major(lo) && semver.Compare(v, lo) >= 0 && semver.Compare(hi, v) >= 0 {
			return true
		}
	}
	return false
}

func validDescriptor(d harness.AdapterDescriptor) bool {
	_, iv := parse(d.ImplementationVersion)
	_, cv := parse(d.ContractVersion)
	if !kindPattern.MatchString(d.Kind) || !iv || !cv || !validRanges(d.HarnessProtocols) ||
		!validRanges(d.AgentdProtocols) || len(d.VendorStateFormats) == 0 || len(d.VendorStateFormats) > 32 ||
		len(d.Capabilities) > 12 || d.Capabilities.Validate() != nil {
		return false
	}
	for _, format := range d.VendorStateFormats {
		if !formatPattern.MatchString(format) {
			return false
		}
	}
	l := d.Limits
	return l.InputBytes > 0 && l.InputItems > 0 && l.EventBytes > 0 && l.EventsPerSecond > 0 &&
		l.BufferedEvents > 0 && l.SignalBytes > 0 && l.DiagnosticBytes > 0 && l.VendorStatePaths > 0 &&
		l.ShutdownTimeout > 0 && l.CheckpointTimeout > 0
}
