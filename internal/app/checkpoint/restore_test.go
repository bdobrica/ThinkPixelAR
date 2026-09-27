package checkpoint

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/checkpoint"
	port "github.com/bdobrica/ThinkPixelAR/internal/ports/checkpoint"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func restoreFixture(t *testing.T) ([]byte, RestoreTarget, RestoreChecks, testSigner) {
	t.Helper()
	r, p, now, key := manifestFixture(t)
	content := "durable conversation"
	r.VendorState[0].Size = int64(len(content))
	r.VendorState[0].Digest = hash([]byte(content))
	evidence := strings.Repeat("b", 64)
	manifest, err := Build(context.Background(), r, p, evidence, now, key)
	if err != nil {
		t.Fatal(err)
	}
	target := RestoreTarget{r.Boundary.TenantID, r.ID, domain.Committed, r.Binding, WorkspaceManifest{r.Binding.WorkspaceID, r.Binding.WorkspaceGenerationID, 1, p.SnapshotReference, Digest(p.ManifestDigest), "sha-256", evidence}}
	checks := RestoreChecks{
		Keys: func(_ context.Context, tenant primitives.ID, issuer, id string, at time.Time) (ed25519.PublicKey, error) {
			if tenant != target.TenantID || issuer != "issuer" || id != "key-1" || !at.Equal(now) {
				return nil, errors.New("untrusted key")
			}
			return key.key.Public().(ed25519.PublicKey), nil
		},
		Compatible: func(_ context.Context, r RuntimeManifest, objects []port.VendorObject) error {
			if r.AdapterVersion != "0.155.0" || r.Protocol != (port.VersionedName{Name: "app-server", Version: "v2"}) || len(objects) != 1 || objects[0].Format != r.StateFormat {
				return ErrIncompatible
			}
			return nil
		},
		Workspace: func(_ context.Context, tenant primitives.ID, w WorkspaceManifest) error {
			if tenant != target.TenantID || w != target.Workspace {
				return ErrInvalidRestore
			}
			return nil
		},
		OpenVendor: func(_ context.Context, tenant primitives.ID, o port.VendorObject) (io.ReadCloser, error) {
			if tenant != target.TenantID || o.Reference != "immutable-1" {
				return nil, ErrInvalidRestore
			}
			return io.NopCloser(strings.NewReader(content)), nil
		}, MaxVendorBytes: 1024,
	}
	return manifest.CanonicalManifest, target, checks, key
}
func validateRestore(t *testing.T, raw []byte, target RestoreTarget, checks RestoreChecks) error {
	t.Helper()
	v, err := NewRestoreValidator(checks)
	if err != nil {
		t.Fatal(err)
	}
	return v.Validate(context.Background(), raw, target)
}
func alterRestore(t *testing.T, raw []byte, key *testSigner, change func(map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	change(m)
	if key != nil {
		i := m["integrity"].(map[string]any)
		delete(i, "signature")
		delete(i, "payload_digest")
		payload, err := jcs(m)
		if err != nil {
			t.Fatal(err)
		}
		i["payload_digest"] = hash(payload)
		message, _ := jcs([]any{"thinkpixel.checkpoint.signature/v1", m["schema_version"], m["checkpoint_id"], m["tenant_id"], m["session_id"], i["payload_digest"], i["signed_at"]})
		signature, _ := key.Sign(context.Background(), message)
		i["signature"] = base64.RawURLEncoding.EncodeToString(signature)
	}
	out, err := jcs(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestRestorePublishedManifest(t *testing.T) {
	raw, target, checks, _ := restoreFixture(t)
	if err := validateRestore(t, raw, target, checks); err != nil {
		t.Fatal(err)
	}
	// Revalidation must consult current key and compatibility policy again.
	checks.Keys = func(context.Context, primitives.ID, string, string, time.Time) (ed25519.PublicKey, error) {
		return nil, errors.New("revoked private detail")
	}
	if err := validateRestore(t, raw, target, checks); err != ErrInvalidRestore {
		t.Fatal(err)
	}
}
func TestRestoreRejectsUntrustedEnvelope(t *testing.T) {
	cases := map[string]func([]byte) []byte{
		"duplicate": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"checkpoint_id":`), []byte(`"checkpoint_id":"other","checkpoint_id":`), 1)
		},
		"whitespace": func(b []byte) []byte { return append([]byte(" "), b...) },
		"trailing":   func(b []byte) []byte { return append(b, []byte(`{}`)...) },
		"utf8":       func(b []byte) []byte { return append(b, 0xff) },
		"surrogate":  func(b []byte) []byte { return bytes.Replace(b, []byte(`"codex"`), []byte(`"\ud800"`), 1) },
		"over-limit": func(b []byte) []byte { return bytes.Repeat([]byte(" "), 262145) },
		"deep":       func(b []byte) []byte { return []byte(strings.Repeat("[", 18) + "0" + strings.Repeat("]", 18)) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			raw, target, checks, _ := restoreFixture(t)
			checks.Workspace = func(context.Context, primitives.ID, WorkspaceManifest) error { t.Fatal("storage reached"); return nil }
			if err := validateRestore(t, change(raw), target, checks); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	for _, field := range []string{"checkpoint_id", "tenant_id", "session_id", "operation_id", "runtime", "workspace", "vendor_state", "lineage", "exclusions", "integrity", "unknown"} {
		t.Run(field, func(t *testing.T) {
			raw, target, checks, _ := restoreFixture(t)
			raw = alterRestore(t, raw, nil, func(m map[string]any) { m[field] = "mutation" })
			if err := validateRestore(t, raw, target, checks); err == nil {
				t.Fatal("accepted mutation")
			}
		})
	}
}
func TestRestoreRejectsSignedInvalidMetadata(t *testing.T) {
	cases := map[string]func(map[string]any){
		"root":      func(m map[string]any) { m["integrity"].(map[string]any)["composite_root"] = strings.Repeat("c", 64) },
		"schema":    func(m map[string]any) { m["schema_version"] = "thinkpixel.checkpoint/v99" },
		"algorithm": func(m map[string]any) { m["integrity"].(map[string]any)["signature_algorithm"] = "none" },
		"uppercase": func(m map[string]any) {
			m["runtime"].(map[string]any)["agent_runtime_spec_digest"] = strings.Repeat("A", 64)
		},
		"bad-time":   func(m map[string]any) { m["committed_at"] = "invalid" },
		"time-order": func(m map[string]any) { m["committed_at"] = "2020-01-01T00:00:00Z" },
		"vendor-format": func(m map[string]any) {
			m["vendor_state"].([]any)[0].(map[string]any)["state_format"] = map[string]any{"name": "other", "version": "v2"}
		},
		"generation":       func(m map[string]any) { m["workspace"].(map[string]any)["generation"] = 9007199254740992 },
		"vendor-size":      func(m map[string]any) { m["vendor_state"].([]any)[0].(map[string]any)["size_bytes"] = 9007199254740992 },
		"vendor-duplicate": func(m map[string]any) { v := m["vendor_state"].([]any); m["vendor_state"] = append(v, v[0]) },
		"extensions":       func(m map[string]any) { m["extensions"] = map[string]any{"unknown.behavior": true} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			raw, target, checks, key := restoreFixture(t)
			raw = alterRestore(t, raw, &key, change)
			if err := validateRestore(t, raw, target, checks); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
func TestRestoreTrustedBindingsAndCompatibility(t *testing.T) {
	cases := map[string]func(*RestoreTarget){
		"tenant": func(t *RestoreTarget) { t.TenantID = "other" }, "session": func(t *RestoreTarget) { t.Binding.SessionID = "other" },
		"checkpoint": func(t *RestoreTarget) { t.CheckpointID = "other" }, "parent": func(t *RestoreTarget) { t.Binding.ParentCheckpointID = "other" },
		"generation": func(t *RestoreTarget) { t.Workspace.Generation++ }, "spec": func(t *RestoreTarget) { t.Binding.RuntimeSpecDigest = strings.Repeat("c", 64) },
		"profile": func(t *RestoreTarget) { t.Binding.RuntimeProfileDigest = strings.Repeat("c", 64) }, "build": func(t *RestoreTarget) { t.Binding.AdapterBuildDigest = strings.Repeat("c", 64) },
		"protocol": func(t *RestoreTarget) { t.Binding.ProtocolVersion = "v3" }, "state-format": func(t *RestoreTarget) { t.Binding.StateFormatVersion = "v2" },
		"creating": func(t *RestoreTarget) { t.State = domain.Creating }, "deleting": func(t *RestoreTarget) { t.State = domain.Deleting }, "deleted": func(t *RestoreTarget) { t.State = domain.Deleted },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			raw, target, checks, _ := restoreFixture(t)
			change(&target)
			if err := validateRestore(t, raw, target, checks); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	raw, target, checks, _ := restoreFixture(t)
	checks.Compatible = func(context.Context, RuntimeManifest, []port.VendorObject) error {
		return errors.New("unsupported capabilities private detail")
	}
	if err := validateRestore(t, raw, target, checks); err != ErrIncompatible {
		t.Fatal(err)
	}
}
func TestRestoreVerifiesStoredBytes(t *testing.T) {
	for _, content := range []string{"", "durable conversatio", "durable conversation-extra", "corrupt conversation"} {
		raw, target, checks, _ := restoreFixture(t)
		closed := false
		checks.OpenVendor = func(context.Context, primitives.ID, port.VendorObject) (io.ReadCloser, error) {
			return trackedReader{strings.NewReader(content), &closed}, nil
		}
		if err := validateRestore(t, raw, target, checks); err != ErrInvalidRestore || !closed {
			t.Fatal(err, closed)
		}
	}
	raw, target, checks, _ := restoreFixture(t)
	checks.Workspace = func(context.Context, primitives.ID, WorkspaceManifest) error {
		return errors.New("missing snapshot or credential canary")
	}
	checks.OpenVendor = func(context.Context, primitives.ID, port.VendorObject) (io.ReadCloser, error) {
		t.Fatal("vendor reached")
		return nil, nil
	}
	if err := validateRestore(t, raw, target, checks); err != ErrInvalidRestore {
		t.Fatal(err)
	}
	raw, target, checks, _ = restoreFixture(t)
	checks.MaxVendorBytes = 1
	if err := validateRestore(t, raw, target, checks); err != ErrInvalidRestore {
		t.Fatal(err)
	}
	v, _ := NewRestoreValidator(checks)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := v.Validate(ctx, raw, target); err != ErrInvalidRestore {
		t.Fatal(err)
	}
	if _, err := NewRestoreValidator(RestoreChecks{}); err != ErrInvalidRestore {
		t.Fatal(err)
	}
}

type trackedReader struct {
	io.Reader
	closed *bool
}

func (r trackedReader) Close() error { *r.closed = true; return nil }

func TestRestoreSignatureAndReadFailures(t *testing.T) {
	raw, target, checks, _ := restoreFixture(t)
	// Corrupt only the signature: the payload digest remains correct.
	bad := alterRestore(t, raw, nil, func(m map[string]any) {
		m["integrity"].(map[string]any)["signature"] = base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	})
	if err := validateRestore(t, bad, target, checks); err != ErrInvalidRestore {
		t.Fatal(err)
	}
	checks.Keys = func(context.Context, primitives.ID, string, string, time.Time) (ed25519.PublicKey, error) {
		return make(ed25519.PublicKey, 32), nil
	}
	if err := validateRestore(t, raw, target, checks); err != ErrInvalidRestore {
		t.Fatal(err)
	}
	for _, failure := range []string{"missing", "nil-reader", "read", "close"} {
		t.Run(failure, func(t *testing.T) {
			raw, target, checks, _ := restoreFixture(t)
			closed := false
			checks.OpenVendor = func(context.Context, primitives.ID, port.VendorObject) (io.ReadCloser, error) {
				if failure == "missing" {
					return nil, errors.New("private object location")
				}
				if failure == "nil-reader" {
					return nil, nil
				}
				return failingRestoreReader{strings.NewReader("durable conversation"), failure, &closed}, nil
			}
			if err := validateRestore(t, raw, target, checks); err != ErrInvalidRestore {
				t.Fatal(err)
			}
			if (failure == "read" || failure == "close") && !closed {
				t.Fatal("reader not closed")
			}
		})
	}
}

type failingRestoreReader struct {
	io.Reader
	failure string
	closed  *bool
}

func (r failingRestoreReader) Read(p []byte) (int, error) {
	if r.failure == "read" {
		return 0, errors.New("private read error")
	}
	return r.Reader.Read(p)
}
func (r failingRestoreReader) Close() error {
	*r.closed = true
	if r.failure == "close" {
		return errors.New("private close error")
	}
	return nil
}
