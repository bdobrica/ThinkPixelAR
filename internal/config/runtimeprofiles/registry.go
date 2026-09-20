// Package runtimeprofiles atomically loads trusted Runtime Profile configuration.
package runtimeprofiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"unicode/utf8"

	"github.com/bdobrica/ThinkPixelAR/docs/contracts"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	canonical "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Resolve must validate all opaque implementation references and required
// provider/isolation/architecture/storage/network/process/lifecycle capabilities.
// It returns the complete JSON reference-resolution evidence to digest. It may
// not treat a profile's isolation name or a caller assertion as qualification.
type Resolve func(runtimeprofile.Profile) ([]byte, error)

type entry struct {
	profile, implementation      []byte
	digest, implementationDigest string
}

type Registry struct {
	mu      sync.RWMutex
	schema  *jsonschema.Schema
	entries map[string]entry
}

func New() (*Registry, error) {
	c := jsonschema.NewCompiler()
	v, err := jsonschema.UnmarshalJSON(bytes.NewBufferString(contracts.RuntimeProfileSchema()))
	if err != nil {
		return nil, err
	}
	const uri = "https://schemas.thinkpixel.io/thinkpixelar/contracts/v1/runtime-profile.json"
	if err = c.AddResource(uri, v); err != nil {
		return nil, err
	}
	s, err := c.Compile(uri)
	if err != nil {
		return nil, err
	}
	return &Registry{schema: s, entries: map[string]entry{}}, nil
}

// Reload publishes an entire validated set or leaves the previous set untouched.
// The caller owns serialized configuration reload ordering.
func (r *Registry) Reload(documents [][]byte, resolve Resolve) error {
	if resolve == nil || len(documents) == 0 || len(documents) > 128 {
		return runtimeprofile.ErrInvalidProfile
	}
	next := make(map[string]entry, len(documents))
	for _, raw := range documents {
		if len(raw) > 65536 || !utf8.Valid(raw) || !json.Valid(raw) {
			return runtimeprofile.ErrInvalidProfile
		}
		normalized, err := canonical.Transform(raw)
		if err != nil {
			return runtimeprofile.ErrInvalidProfile
		}
		v, err := jsonschema.UnmarshalJSON(bytes.NewReader(normalized))
		if err != nil || r.schema.Validate(v) != nil {
			return runtimeprofile.ErrInvalidProfile
		}
		var p runtimeprofile.Profile
		if err = json.Unmarshal(normalized, &p); err != nil || p.ValidateConstraints() != nil {
			return runtimeprofile.ErrInvalidProfile
		}
		if _, ok := next[p.Name]; ok {
			return runtimeprofile.ErrInvalidProfile
		}
		impl, err := resolve(p)
		if err != nil || len(impl) == 0 || len(impl) > 65536 {
			return errors.New("runtime profile implementation unavailable")
		}
		impl, err = canonical.Transform(impl)
		if err != nil || len(impl) == 0 || impl[0] != '{' {
			return errors.New("invalid runtime profile implementation evidence")
		}
		next[p.Name] = entry{profile: normalized, implementation: impl, digest: digest(normalized), implementationDigest: digest(impl)}
	}
	r.mu.Lock()
	r.entries = next
	r.mu.Unlock()
	return nil
}

// Lookup returns independent copies; neither callers nor future reloads mutate
// an already returned resolution. Persist these bytes before acquiring compute.
func (r *Registry) Lookup(name string) (runtimeprofile.Profile, []byte, string, []byte, string, bool) {
	r.mu.RLock()
	e, ok := r.entries[name]
	r.mu.RUnlock()
	if !ok {
		return runtimeprofile.Profile{}, nil, "", nil, "", false
	}
	var p runtimeprofile.Profile
	_ = json.Unmarshal(e.profile, &p)
	return p, append([]byte(nil), e.profile...), e.digest, append([]byte(nil), e.implementation...), e.implementationDigest, true
}
func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
