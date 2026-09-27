package local

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/config/runtimeprofiles"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/authority"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	canonical "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

var ErrConfiguration = errors.New("invalid local authority configuration")
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ApprovedRuntime comes from the operator's validated, digest-pinned resolver.
// That resolver owns manifest schema, image/adapter compatibility and minimum
// runtime requirements; request payloads cannot populate this allowlist.
type ApprovedRuntime struct {
	Binding      session.RuntimeBinding
	Profiles     []string
	Capabilities []string
}

type Config struct {
	Mode                             string
	Revision                         string
	DefaultProfile                   string
	Profiles                         []string
	DefaultDuration, MaximumDuration time.Duration
	// Every enabled profile must fit these global ceilings. Profile values are
	// bounded defaults as well as ceilings for per-request narrowing.
	CPU, Memory, EphemeralStorage, WorkspaceBytes, MaxProcesses int64
	Architectures, Networks                                     []string
	Runtimes                                                    []ApprovedRuntime
	// Optional allowlists. Empty lists allow all authenticated local callers.
	Tenants    []string
	Principals []string
}

type resolved struct {
	Profile                      runtimeprofile.Profile
	Digest, ImplementationDigest string
	Implementation               []byte
}

type Authority struct {
	config   Config
	profiles map[string]resolved
	revision string
	store    persistence.TransactionManager
	clock    clock.Clock
}

var _ authority.Admission = (*Authority)(nil)

// New snapshots trusted operator policy. Registry reloads and caller mutations
// cannot alter this instance. No default or fallback authority mode exists.
func New(c Config, registry *runtimeprofiles.Registry, store persistence.TransactionManager, clk clock.Clock) (*Authority, error) {
	if c.Mode != "local" || !digestPattern.MatchString(c.Revision) || registry == nil || store == nil || clk == nil || c.DefaultDuration <= 0 || c.MaximumDuration < c.DefaultDuration || c.MaximumDuration > 365*24*time.Hour || len(c.Profiles) == 0 || len(c.Profiles) > 128 || !slices.Contains(c.Profiles, c.DefaultProfile) || len(c.Runtimes) == 0 || len(c.Runtimes) > 128 {
		return nil, ErrConfiguration
	}
	for _, v := range []int64{c.CPU, c.Memory, c.EphemeralStorage, c.WorkspaceBytes, c.MaxProcesses} {
		if v <= 0 {
			return nil, ErrConfiguration
		}
	}
	if !subset(c.Architectures, []string{"amd64", "arm64"}) || len(c.Architectures) == 0 || len(c.Networks) == 0 || !subset(c.Networks, []string{"none", "thinkpixel-only", "restricted-development", "package-mirrors", "unrestricted-standalone"}) {
		return nil, ErrConfiguration
	}
	for _, v := range c.Principals {
		if !digestPattern.MatchString(v) {
			return nil, ErrConfiguration
		}
	}
	for _, v := range c.Tenants {
		if !validID(v) {
			return nil, ErrConfiguration
		}
	}
	raw, _ := json.Marshal(c)
	var frozen Config
	_ = json.Unmarshal(raw, &frozen)
	c = frozen
	a := &Authority{config: c, profiles: map[string]resolved{}, store: store, clock: clk}
	for _, name := range c.Profiles {
		p, _, digest, impl, idigest, ok := registry.Lookup(name)
		if !ok || a.profiles[name].Digest != "" || p.Resources.CPU.Limit > c.CPU || p.Resources.Memory.Limit > c.Memory || p.Resources.EphemeralStorage.Limit > c.EphemeralStorage || p.Storage.WorkspaceBytes > c.WorkspaceBytes || p.Resources.MaxProcesses > c.MaxProcesses || !subset(p.Platform.Architectures, c.Architectures) || !slices.Contains(c.Networks, p.Network.Profile) || p.Resources.GPU.Count != 0 || p.Platform.GPUAllowed {
			return nil, ErrConfiguration
		}
		a.profiles[name] = resolved{p, digest, idigest, impl}
	}
	for i, r := range c.Runtimes {
		b := r.Binding
		if b.AuthorityMode != "LOCAL" || b.AuthorityNamespace != "thinkpixelar/local" || b.AgentID == "" || b.AgentVersionID == "" || b.RuntimeSpecSchemaVersion == "" || b.RuntimeProfileSchemaVersion == "" || len(b.RuntimeSpec) > 32768 || len(b.RuntimeProfileSnapshot) > 16384 || !canonicalDigest(b.RuntimeSpec, b.RuntimeSpecDigest) || !canonicalDigest(b.RuntimeProfileSnapshot, b.RuntimeProfileDigest) || len(r.Profiles) == 0 || !subset(r.Profiles, c.Profiles) || !subset(r.Capabilities, []string{"shell", "process", "fork"}) {
			return nil, ErrConfiguration
		}
		for _, previous := range c.Runtimes[:i] {
			if sameBinding(previous.Binding, b) {
				return nil, ErrConfiguration
			}
		}
		for _, name := range r.Profiles {
			entry := a.profiles[name]
			// Leave room for bounded identity/time fields in the 64 KiB replay record.
			raw, _ := json.Marshal(authority.Grant{Runtime: b, Profile: entry.Profile, Implementation: entry.Implementation})
			if len(raw) > 60000 {
				return nil, ErrConfiguration
			}
		}
	}
	// Include actual resolved profiles and implementation evidence, not just a
	// caller-asserted revision string, in the immutable policy evidence digest.
	raw, _ = json.Marshal(struct {
		Config   Config
		Profiles map[string]resolved
	}{c, a.profiles})
	a.revision = sandbox.Digest(raw)
	return a, nil
}
func canonicalDigest(raw []byte, digest string) bool {
	if !digestPattern.MatchString(digest) || len(raw) == 0 || raw[0] != '{' {
		return false
	}
	b, e := canonical.Transform(raw)
	return e == nil && sandbox.Digest(b) == digest
}
func subset(values, allowed []string) bool {
	seen := map[string]bool{}
	for _, v := range values {
		if seen[v] || !slices.Contains(allowed, v) {
			return false
		}
		seen[v] = true
	}
	return true
}
