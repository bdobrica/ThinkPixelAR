//go:build linux

// Package sessionresume implements the bounded standalone operator resume lane.
// Operator configuration and exports live outside the disposable sandbox.
package sessionresume

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/harness/codex"
	checkpoints "github.com/bdobrica/ThinkPixelAR/internal/app/checkpoint"
	sessions "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	port "github.com/bdobrica/ThinkPixelAR/internal/ports/checkpoint"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	canonical "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

// Config approves one exact request and immutable export for a finite interval.
// This is AR operator policy, not input accepted from HTTP, vendor state or a
// workspace. The lane supports local authority, one bounded Workspace file and
// Codex's two export objects. It cannot be used as AG or gateway admission.
type Config struct {
	Version                             int
	Enabled                             bool
	ExpiresAt                           time.Time
	Request                             sessions.ResumeRequest
	Binding                             domain.RuntimeBinding
	Manifest                            []byte
	PublicKey                           []byte
	Issuer, KeyID                       string
	Directory                           string
	SSH, Namespace, NamespaceUID, Image string
	ProofScript, ProofScriptDigest      string
}

type manifest struct {
	Runtime   checkpoints.RuntimeManifest   `json:"runtime"`
	Workspace checkpoints.WorkspaceManifest `json:"workspace"`
	Vendors   []port.VendorObject           `json:"vendor_state"`
}

type Bundle struct {
	config   Config
	path     string
	manifest manifest
}

func ReadFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > limit {
		return nil, workspace.ErrIntegrity
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, workspace.ErrIntegrity
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(info, after) {
		return nil, workspace.ErrIntegrity
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, workspace.ErrIntegrity
	}
	return b, nil
}

func Load(path string) (*Bundle, error) {
	raw, err := ReadFile(path, 1<<20)
	if err != nil {
		return nil, err
	}
	var c Config
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
		return nil, workspace.ErrInvalid
	}
	if c.Version != 1 || c.Request.Validate() != nil || len(c.PublicKey) != ed25519.PublicKeySize || c.Issuer == "" || c.KeyID == "" || c.Binding.AuthorityMode != "LOCAL" || c.Binding.AuthorityNamespace != "thinkpixelar/local" || !filepath.IsAbs(c.Directory) || !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`).MatchString(c.SSH) || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$`).MatchString(c.Namespace) || c.NamespaceUID == "" || !regexp.MustCompile(`^[a-zA-Z0-9./:_-]+@sha256:[a-f0-9]{64}$`).MatchString(c.Image) {
		return nil, workspace.ErrInvalid
	}
	var spec struct {
		Image struct {
			Reference string `json:"reference"`
		} `json:"image"`
	}
	if json.Unmarshal(c.Binding.RuntimeSpec, &spec) != nil || spec.Image.Reference != c.Image || c.Binding.RuntimeSpecDigest != sandbox.Digest(c.Binding.RuntimeSpec) || c.Binding.RuntimeProfileDigest != "sha256:4b83749db369501e78e94290609add6a25e8f0ee5999aa10729b448759922a0a" || sandbox.Digest(c.Binding.RuntimeProfileSnapshot) != c.Binding.RuntimeProfileDigest {
		return nil, workspace.ErrIntegrity
	}
	b := &Bundle{config: c, path: path}
	if json.Unmarshal(c.Manifest, &b.manifest) != nil || b.manifest.Runtime.AdapterVersion != codex.Version || b.manifest.Runtime.AdapterBuild != codex.LinuxARM64SHA256 || b.manifest.Runtime.SpecDigest != checkpoints.Digest(c.Binding.RuntimeSpecDigest) || b.manifest.Runtime.ProfileDigest != checkpoints.Digest(c.Binding.RuntimeProfileDigest) || len(b.manifest.Vendors) != 2 {
		return nil, workspace.ErrIntegrity
	}
	runtime := b.manifest.Runtime
	if runtime.AdapterKind != "codex" || runtime.Protocol != (port.VersionedName{Name: "app-server", Version: "v2"}) || runtime.StateFormat != (port.VersionedName{Name: "codex", Version: "v1"}) {
		return nil, workspace.ErrIntegrity
	}
	if b.manifest.Vendors[0].ID != "restore" || b.manifest.Vendors[1].ID != "rollout" {
		return nil, workspace.ErrIntegrity
	}
	for _, v := range b.manifest.Vendors {
		if v.Format != (port.VersionedName{Name: "codex", Version: "v1"}) || v.Algorithm != "sha-256" {
			return nil, workspace.ErrIntegrity
		}
	}
	return b, nil
}

func (b *Bundle) current() error {
	now, err := Load(b.path)
	if err != nil || !reflect.DeepEqual(now.config, b.config) || !now.config.Enabled || !time.Now().Before(now.config.ExpiresAt) {
		return workspace.ErrUnavailable
	}
	return nil
}
func (b *Bundle) object(name string) ([]byte, error) {
	raw, err := ReadFile(filepath.Join(b.config.Directory, name), 1<<20)
	if err != nil {
		return nil, err
	}
	digest := checkpoints.Digest(sandbox.Digest(raw))
	if name == "workspace" {
		if digest != b.manifest.Workspace.Root {
			return nil, workspace.ErrIntegrity
		}
		return raw, nil
	}
	for _, v := range b.manifest.Vendors {
		if v.ID == name && v.Digest == digest && v.Size == int64(len(raw)) {
			return raw, nil
		}
	}
	return nil, workspace.ErrIntegrity
}
func (b *Bundle) Access(_ context.Context, r sessions.ResumeRequest) error {
	if r != b.config.Request {
		return workspace.ErrInvalid
	}
	return b.current()
}
func (b *Bundle) Policy(_ context.Context, i sessions.ResumeIntent) error {
	// PostgreSQL JSONB normalizes whitespace and key order. Compare the exact
	// canonical runtime, not its incidental database serialization.
	runtime := i.Runtime
	spec, se := canonical.Transform(runtime.RuntimeSpec)
	profile, pe := canonical.Transform(runtime.RuntimeProfileSnapshot)
	runtime.RuntimeSpec, runtime.RuntimeProfileSnapshot = spec, profile
	if se != nil || pe != nil || b.current() != nil || !reflect.DeepEqual(runtime, b.config.Binding) || !bytes.Equal(i.Manifest, b.config.Manifest) {
		return workspace.ErrIntegrity
	}
	for _, n := range []string{"workspace", "restore", "rollout"} {
		if _, err := b.object(n); err != nil {
			return err
		}
	}
	return nil
}
func (b *Bundle) Validator() (*checkpoints.RestoreValidator, error) {
	return checkpoints.NewRestoreValidator(checkpoints.RestoreChecks{
		Keys: func(_ context.Context, t primitives.ID, issuer, key string, _ time.Time) (ed25519.PublicKey, error) {
			if b.current() != nil || t != b.config.Request.Caller.TenantID || issuer != b.config.Issuer || key != b.config.KeyID {
				return nil, workspace.ErrIntegrity
			}
			return ed25519.PublicKey(bytes.Clone(b.config.PublicKey)), nil
		},
		Compatible: func(_ context.Context, r checkpoints.RuntimeManifest, v []port.VendorObject) error {
			if r != b.manifest.Runtime || !reflect.DeepEqual(v, b.manifest.Vendors) {
				return workspace.ErrIntegrity
			}
			return nil
		},
		Workspace: func(_ context.Context, t primitives.ID, m checkpoints.WorkspaceManifest) error {
			if t != b.config.Request.Caller.TenantID || m != b.manifest.Workspace {
				return workspace.ErrIntegrity
			}
			_, err := b.object("workspace")
			return err
		},
		OpenVendor: func(_ context.Context, t primitives.ID, v port.VendorObject) (io.ReadCloser, error) {
			if t != b.config.Request.Caller.TenantID {
				return nil, workspace.ErrIntegrity
			}
			for _, expected := range b.manifest.Vendors {
				if v == expected {
					raw, err := b.object(v.ID)
					return io.NopCloser(bytes.NewReader(raw)), err
				}
			}
			return nil, workspace.ErrIntegrity
		}, MaxVendorBytes: 2 << 20,
	})
}
