package checkpoint

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/checkpoint"
	port "github.com/bdobrica/ThinkPixelAR/internal/ports/checkpoint"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
)

type testSigner struct {
	key     ed25519.PrivateKey
	corrupt bool
}

func (s testSigner) Identity() (string, string, ed25519.PublicKey) {
	return "issuer", "key-1", s.key.Public().(ed25519.PublicKey)
}
func (s testSigner) Sign(_ context.Context, raw []byte) ([]byte, error) {
	sig := ed25519.Sign(s.key, raw)
	if s.corrupt {
		sig[0] ^= 1
	}
	return sig, nil
}
func manifestFixture(t *testing.T) (port.Request, workspace.CheckpointProof, time.Time, testSigner) {
	t.Helper()
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	d := strings.Repeat("a", 64)
	r := port.Request{ID: "01900000-0000-7000-8000-000000000001", Boundary: workspace.CheckpointRequest{TenantID: "01900000-0000-7000-8000-000000000002"}, Binding: domain.Binding{SessionID: "01900000-0000-7000-8000-000000000003", WorkspaceID: "01900000-0000-7000-8000-000000000004", WorkspaceGenerationID: "01900000-0000-7000-8000-000000000005", WorkspaceGeneration: 1, OperationID: "01900000-0000-7000-8000-000000000001", Purpose: domain.PurposeCheckpoint, RuntimeSpecID: "runtime", RuntimeSpecDigest: d, AdapterKind: "codex", AdapterVersion: "0.155.0", AdapterBuildDigest: d, ProtocolName: "app-server", ProtocolVersion: "v2", StateFormatName: "codex", StateFormatVersion: "v1", RuntimeProfileDigest: d}, VendorState: []port.VendorObject{{ID: "object-1", Reference: "immutable-1", MediaType: "application/octet-stream", Format: port.VersionedName{Name: "codex", Version: "v1"}, Algorithm: "sha-256", Digest: d, Size: 25, Classification: "Confidential", Role: "required"}}}
	return r, workspace.CheckpointProof{SnapshotReference: "snapshot-1", ManifestDigest: "sha256:" + d}, time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC), testSigner{key: key}
}
func TestManifestSignedCanonicalEnvelope(t *testing.T) {
	r, p, now, key := manifestFixture(t)
	ctx := context.Background()
	i, e := Build(ctx, r, p, "sha256:"+strings.Repeat("b", 64), now, key)
	if e != nil {
		t.Fatal(e)
	}
	second, e := Build(ctx, r, p, "sha256:"+strings.Repeat("b", 64), now, key)
	if e != nil || !bytes.Equal(i.CanonicalManifest, second.CanonicalManifest) {
		t.Fatal("nondeterministic manifest", e)
	}
	var m map[string]any
	if e = json.Unmarshal(i.CanonicalManifest, &m); e != nil {
		t.Fatal(e)
	}
	normalized, e := jcs(m)
	if e != nil || !bytes.Equal(normalized, i.CanonicalManifest) {
		t.Fatal("noncanonical")
	}
	fields := m["integrity"].(map[string]any)
	delete(fields, "payload_digest")
	delete(fields, "signature")
	raw, _ := jcs(m)
	if hash(raw) != i.PayloadDigest {
		t.Fatal("wrong payload digest")
	}
	signed, _ := jcs([]any{"thinkpixel.checkpoint.signature/v1", "thinkpixel.checkpoint/v1", r.ID, r.Boundary.TenantID, r.Binding.SessionID, i.PayloadDigest, now.Format(time.RFC3339Nano)})
	sig, _ := base64.RawURLEncoding.DecodeString(i.Signature)
	if !ed25519.Verify(key.key.Public().(ed25519.PublicKey), signed, sig) {
		t.Fatal("bad signature")
	}
	for _, field := range []string{"checkpoint_id", "tenant_id", "session_id", "operation_id", "runtime", "workspace", "vendor_state", "lineage", "exclusions"} {
		old := m[field]
		m[field] = "mutation"
		raw, _ = jcs(m)
		if hash(raw) == i.PayloadDigest {
			t.Fatalf("unbound %s", field)
		}
		m[field] = old
	}
	r.VendorState[0].Digest = strings.Repeat("c", 64)
	changed, e := Build(ctx, r, p, "sha256:"+strings.Repeat("b", 64), now, key)
	if e != nil || changed.CompositeRoot == i.CompositeRoot {
		t.Fatal("vendor digest not bound", e)
	}
}
func TestManifestRejectsInvalidCandidates(t *testing.T) {
	cases := []struct {
		name   string
		change func(*port.Request, *workspace.CheckpointProof, *testSigner)
	}{
		{"credential-url", func(_ *port.Request, p *workspace.CheckpointProof, _ *testSigner) {
			p.SnapshotReference = "https://user:secret@example.test/object"
		}},
		{"duplicate-vendor", func(r *port.Request, _ *workspace.CheckpointProof, _ *testSigner) {
			r.VendorState = append(r.VendorState, r.VendorState[0])
		}},
		{"unsafe-integer", func(r *port.Request, _ *workspace.CheckpointProof, _ *testSigner) {
			r.VendorState[0].Size = 9007199254740992
		}},
		{"unsupported-digest", func(r *port.Request, _ *workspace.CheckpointProof, _ *testSigner) { r.VendorState[0].Algorithm = "md5" }},
		{"bad-signature", func(_ *port.Request, _ *workspace.CheckpointProof, k *testSigner) { k.corrupt = true }},
		{"unknown-role", func(r *port.Request, _ *workspace.CheckpointProof, _ *testSigner) { r.VendorState[0].Role = "ignored" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, p, now, k := manifestFixture(t)
			tc.change(&r, &p, &k)
			if _, e := Build(context.Background(), r, p, strings.Repeat("b", 64), now, k); !errors.Is(e, workspace.ErrIntegrity) {
				t.Fatal(e)
			}
		})
	}
}
