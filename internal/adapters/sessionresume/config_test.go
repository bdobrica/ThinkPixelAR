//go:build linux

package sessionresume

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/harness/codex"
	checkpoints "github.com/bdobrica/ThinkPixelAR/internal/app/checkpoint"
	sessions "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	port "github.com/bdobrica/ThinkPixelAR/internal/ports/checkpoint"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	canonical "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

func bundleFixture(t *testing.T) (string, Config) {
	t.Helper()
	dir := t.TempDir()
	id, _ := primitives.NewID(time.Now())
	image := "test.invalid/codex@" + sandbox.Digest([]byte("image"))
	spec, _ := json.Marshal(map[string]any{"image": map[string]string{"reference": image}})
	profile, e := os.ReadFile("../../../docs/profiles/coding-homelab-arm64.json")
	if e != nil {
		t.Fatal(e)
	}
	profile, e = canonical.Transform(profile)
	if e != nil {
		t.Fatal(e)
	}
	binding := domain.RuntimeBinding{AuthorityMode: "LOCAL", AuthorityNamespace: "thinkpixelar/local", RuntimeSpec: spec, RuntimeSpecDigest: sandbox.Digest(spec), RuntimeProfileSnapshot: profile, RuntimeProfileDigest: sandbox.Digest(profile)}
	m := manifest{Runtime: checkpoints.RuntimeManifest{SpecDigest: checkpoints.Digest(binding.RuntimeSpecDigest), ProfileDigest: checkpoints.Digest(binding.RuntimeProfileDigest), AdapterKind: "codex", Protocol: port.VersionedName{Name: "app-server", Version: "v2"}, StateFormat: port.VersionedName{Name: "codex", Version: "v1"}, AdapterVersion: codex.Version, AdapterBuild: codex.LinuxARM64SHA256}, Workspace: checkpoints.WorkspaceManifest{Root: checkpoints.Digest(sandbox.Digest([]byte("workspace")))}}
	for _, name := range []string{"workspace", "restore", "rollout"} {
		if e = os.WriteFile(filepath.Join(dir, name), []byte(name), 0600); e != nil {
			t.Fatal(e)
		}
		if name != "workspace" {
			m.Vendors = append(m.Vendors, port.VendorObject{ID: name, Format: port.VersionedName{Name: "codex", Version: "v1"}, Algorithm: "sha-256", Digest: checkpoints.Digest(sandbox.Digest([]byte(name))), Size: int64(len(name))})
		}
	}
	raw, _ := json.Marshal(m)
	c := Config{Version: 1, Enabled: true, ExpiresAt: time.Now().Add(time.Hour), Request: sessions.ResumeRequest{Caller: sessions.Caller{TenantID: id, PrincipalDigest: sandbox.Digest([]byte("principal"))}, SessionID: id, OperationID: id, CheckpointID: id}, Binding: binding, Manifest: raw, PublicKey: make([]byte, ed25519.PublicKeySize), Issuer: "test", KeyID: "test", Directory: dir, SSH: "k3spi", Namespace: "resume-test", NamespaceUID: "ns-uid", Image: image}
	path := filepath.Join(dir, "config.json")
	writeApproval(t, path, c)
	return path, c
}
func writeApproval(t *testing.T, path string, c Config) {
	t.Helper()
	raw, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestBundleCurrentApprovalAndObjectIntegrity(t *testing.T) {
	path, c := bundleFixture(t)
	b, e := Load(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = b.Access(context.Background(), c.Request); e != nil {
		t.Fatal(e)
	}
	if _, e = b.object("workspace"); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(c.Directory, "workspace"), []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = b.object("workspace"); e == nil {
		t.Fatal("changed export accepted")
	}
	c.Enabled = false
	writeApproval(t, path, c)
	if b.Access(context.Background(), c.Request) == nil {
		t.Fatal("revoked approval accepted")
	}
}
func TestBundleRejectsSubstitutionAndPublicFiles(t *testing.T) {
	for _, mutation := range []string{"image", "runtime", "profile", "mode", "tenant", "permissions", "expiry"} {
		t.Run(mutation, func(t *testing.T) {
			path, c := bundleFixture(t)
			b, e := Load(path)
			if e != nil {
				t.Fatal(e)
			}
			switch mutation {
			case "image":
				c.Image = "other.invalid/codex@" + sandbox.Digest([]byte("other"))
			case "runtime":
				c.Binding.RuntimeSpec = []byte(`{}`)
			case "profile":
				c.Binding.RuntimeProfileDigest = sandbox.Digest([]byte("other"))
			case "mode":
				c.Binding.AuthorityMode = "AG"
			case "tenant":
				c.Request.Caller.TenantID, _ = primitives.NewID(time.Now())
			case "expiry":
				c.ExpiresAt = time.Now().Add(-time.Second)
			}
			writeApproval(t, path, c)
			if mutation == "permissions" {
				if e = os.Chmod(path, 0644); e != nil {
					t.Fatal(e)
				}
			}
			if b.Access(context.Background(), b.Request()) == nil {
				t.Fatal("changed authorization accepted")
			}
		})
	}
}
func TestProbeConfigDoesNotEnableForwardWork(t *testing.T) {
	_, c := bundleFixture(t)
	id := c.Request.SessionID
	for _, generation := range []int64{0, 1} {
		i := sessions.ResumeIntent{Request: c.Request, BootstrapID: id, SandboxID: id, ExecutionGeneration: generation}
		if e := probeConfig(i).Validate(); e != nil {
			t.Fatal(generation, e)
		}
	}
}
func TestSubsetRejectsAdditionalContainersAndChangedMounts(t *testing.T) {
	b := &Bundle{config: Config{Image: "pinned"}}
	want := normalized(b.spec("candidate"))
	changed := normalized(b.spec("candidate")).(map[string]any)
	changed["containers"] = append(changed["containers"].([]any), map[string]any{"name": "extra"})
	if subset(want, changed) {
		t.Fatal("extra container accepted")
	}
	if !subset(want, normalized(b.spec("candidate"))) {
		t.Fatal("same blueprint rejected")
	}
}

func TestPolicyUsesCanonicalDatabaseRuntime(t *testing.T) {
	path, c := bundleFixture(t)
	b, e := Load(path)
	if e != nil {
		t.Fatal(e)
	}
	i := sessions.ResumeIntent{Runtime: c.Binding, Manifest: c.Manifest}
	var value any
	if e = json.Unmarshal(i.Runtime.RuntimeSpec, &value); e != nil {
		t.Fatal(e)
	}
	i.Runtime.RuntimeSpec, e = json.MarshalIndent(value, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	if e = b.Policy(context.Background(), i); e != nil {
		t.Fatal("equivalent JSONB runtime rejected", e)
	}
}

func TestBundleRejectsUnknownAdapterProtocol(t *testing.T) {
	path, c := bundleFixture(t)
	var m manifest
	if err := json.Unmarshal(c.Manifest, &m); err != nil {
		t.Fatal(err)
	}
	m.Runtime.Protocol.Version = "unknown"
	c.Manifest, _ = json.Marshal(m)
	writeApproval(t, path, c)
	if _, err := Load(path); err == nil {
		t.Fatal("unknown protocol accepted")
	}
}
