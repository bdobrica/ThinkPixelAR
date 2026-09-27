// Package sessionruntimes resolves operator-approved immutable Session bindings.
package sessionruntimes

import (
	"bytes"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bdobrica/ThinkPixelAR/docs/contracts"
	"github.com/bdobrica/ThinkPixelAR/internal/config/runtimeprofiles"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	canonical "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var ErrInvalid = errors.New("unavailable or incompatible Session runtime")
var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var digest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// Approved is trusted operator configuration, never a public request. The
// digest is an independently approved canonical manifest digest, not a tag.
type Approved struct {
	ID, AgentID, AgentVersionID, Digest, PolicyDigest string
	Manifest                                          []byte
}

// Qualify verifies installed adapter/protocol compatibility, required
// capabilities, image provenance and platform qualification for this exact
// manifest/profile pair. No callback means no admission. It must not mutate
// its inputs; its returned non-secret evidence is persisted with creation.
type Qualify func(json.RawMessage, runtimeprofile.Profile) (json.RawMessage, error)

type Resolution struct {
	Binding              session.RuntimeBinding
	Implementation       json.RawMessage `json:"implementation"`
	ImplementationDigest string          `json:"implementation_digest"`
	Qualification        json.RawMessage `json:"qualification"`
	PolicyDigest         string          `json:"policy_digest"`
}

type Catalog struct{ entries map[string]Resolution }

// NewLocal freezes approved runtimes and validated profiles. Replacing this
// catalog affects only new creations; idempotent replay uses durable state.
func NewLocal(approved []Approved, profiles *runtimeprofiles.Registry, qualify Qualify) (*Catalog, error) {
	if len(approved) == 0 || len(approved) > 128 || profiles == nil || qualify == nil {
		return nil, ErrInvalid
	}
	compiler := jsonschema.NewCompiler()
	v, err := jsonschema.UnmarshalJSON(strings.NewReader(contracts.AgentRuntimeSpecSchema()))
	if err != nil {
		return nil, ErrInvalid
	}
	const uri = "https://schemas.thinkpixel.io/thinkpixelar/contracts/v1/agent-runtime-spec.json"
	if compiler.AddResource(uri, v) != nil {
		return nil, ErrInvalid
	}
	schema, err := compiler.Compile(uri)
	if err != nil {
		return nil, ErrInvalid
	}
	c := &Catalog{entries: map[string]Resolution{}}
	for _, a := range approved {
		if !identifier.MatchString(a.ID) || !identifier.MatchString(a.AgentID) || !identifier.MatchString(a.AgentVersionID) || !digest.MatchString(a.PolicyDigest) || len(a.Manifest) > 32768 || !utf8.Valid(a.Manifest) {
			return nil, ErrInvalid
		}
		raw, err := canonical.Transform(a.Manifest)
		if err != nil || sandbox.Digest(raw) != a.Digest {
			return nil, ErrInvalid
		}
		value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil || schema.Validate(value) != nil {
			return nil, ErrInvalid
		}
		var spec struct {
			Image          struct{ Reference, Digest string }
			DurablePaths   []string `json:"durable_vendor_paths"`
			Workspace      string   `json:"workspace_mount"`
			RuntimeProfile struct {
				Name      string
				Isolation string `json:"minimum_isolation_class"`
				Network   string `json:"required_network_profile"`
			} `json:"runtime_profile"`
			Platform struct {
				OS            string
				Architectures []string
				GPU           bool `json:"requires_gpu"`
			}
		}
		if json.Unmarshal(raw, &spec) != nil || !strings.HasSuffix(spec.Image.Reference, "@"+spec.Image.Digest) {
			return nil, ErrInvalid
		}
		p, praw, pdigest, impl, idigest, ok := profiles.Lookup(spec.RuntimeProfile.Name)
		rank := map[string]int{"container-standard": 1, "microvm-strong": 2, "confidential-strong": 3}
		if !ok || len(praw) > 16384 || len(impl) > 32768 || rank[p.IsolationClass] < rank[spec.RuntimeProfile.Isolation] || p.Platform.OS != spec.Platform.OS || p.Storage.WorkspaceMount != spec.Workspace || (spec.RuntimeProfile.Network != "" && spec.RuntimeProfile.Network != p.Network.Profile) || spec.Platform.GPU != p.Platform.GPUAllowed {
			return nil, ErrInvalid
		}
		for _, arch := range p.Platform.Architectures {
			if !slices.Contains(spec.Platform.Architectures, arch) {
				return nil, ErrInvalid
			}
		}
		for _, dir := range spec.DurablePaths {
			if path.Clean(dir) != dir || !strings.HasPrefix(dir, strings.TrimSuffix(p.Storage.VendorStateRoot, "/")+"/") {
				return nil, ErrInvalid
			}
		}
		evidence, err := qualify(append(json.RawMessage(nil), raw...), p)
		if err != nil || len(evidence) > 16384 || !utf8.Valid(evidence) {
			return nil, ErrInvalid
		}
		evidence, err = canonical.Transform(evidence)
		if err != nil || len(evidence) < 2 || evidence[0] != '{' {
			return nil, ErrInvalid
		}
		key := a.ID + "/" + p.Name
		if _, exists := c.entries[key]; exists {
			return nil, ErrInvalid
		}
		c.entries[key] = Resolution{Binding: session.RuntimeBinding{AuthorityMode: "LOCAL", AuthorityNamespace: "thinkpixelar/local", AgentID: a.AgentID, AgentVersionID: a.AgentVersionID, RuntimeSpecSchemaVersion: "1", RuntimeSpec: raw, RuntimeSpecDigest: a.Digest, RuntimeProfileSchemaVersion: "1", RuntimeProfileSnapshot: praw, RuntimeProfileDigest: pdigest}, Implementation: impl, ImplementationDigest: idigest, Qualification: evidence, PolicyDigest: a.PolicyDigest}
	}
	return c, nil
}
func (c *Catalog) Resolve(runtimeID, profileID string) (Resolution, error) {
	if c == nil {
		return Resolution{}, ErrInvalid
	}
	r, ok := c.entries[runtimeID+"/"+profileID]
	if !ok {
		return Resolution{}, ErrInvalid
	}
	r.Binding.RuntimeSpec = bytes.Clone(r.Binding.RuntimeSpec)
	r.Binding.RuntimeProfileSnapshot = bytes.Clone(r.Binding.RuntimeProfileSnapshot)
	r.Implementation = bytes.Clone(r.Implementation)
	r.Qualification = bytes.Clone(r.Qualification)
	return r, nil
}
