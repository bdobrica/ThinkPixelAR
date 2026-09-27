// Package checkpoint builds signed manifests from independently verified inputs.
package checkpoint

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/bdobrica/ThinkPixelAR/docs/contracts"
	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/checkpoint"
	port "github.com/bdobrica/ThinkPixelAR/internal/ports/checkpoint"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	canonical "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Signer lives in trusted control-plane composition, never agentd or a Workspace.
// Sign must use Ed25519 and the returned signature is independently checked.
type Signer interface {
	Identity() (issuer, keyID string, publicKey ed25519.PublicKey)
	Sign(context.Context, []byte) ([]byte, error)
}

// Digest converts a database SHA-256 digest to the envelope's unprefixed hex form.
func Digest(s string) string { return strings.TrimPrefix(s, "sha256:") }
func hash(raw []byte) string { d := sha256.Sum256(raw); return hex.EncodeToString(d[:]) }
func jcs(v any) ([]byte, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	return canonical.Transform(raw)
}

// Build implements the v1 SHA-256/Ed25519 publication profile. Verification of
// physical durability, compatibility and credential exclusion must precede it.
func Build(ctx context.Context, r port.Request, proof workspace.CheckpointProof, evidenceDigest string, now time.Time, signer Signer) (domain.Integrity, error) {
	fail := func() (domain.Integrity, error) { return domain.Integrity{}, workspace.ErrIntegrity }
	if signer == nil || now.IsZero() || len(r.VendorState) > 64 {
		return fail()
	}
	b := r.Binding
	// Stable ordering is required, never silently reorder supplied object identities.
	for i, v := range r.VendorState {
		if v.Size < 0 || v.Size > 9007199254740991 || v.Algorithm != "sha-256" || len(v.Digest) != 64 || (i > 0 && r.VendorState[i-1].ID >= v.ID) {
			return fail()
		}
		if _, e := hex.DecodeString(v.Digest); e != nil {
			return fail()
		}
	}
	if b.WorkspaceGeneration > 9007199254740991 {
		return fail()
	}
	workspaceLeaf := map[string]any{"workspace_id": b.WorkspaceID, "generation_id": b.WorkspaceGenerationID, "generation": b.WorkspaceGeneration, "snapshot_ref": proof.SnapshotReference, "manifest_root": Digest(proof.ManifestDigest), "digest_algorithm": "sha-256", "storage_evidence_digest": Digest(evidenceDigest)}
	leaves := []any{map[string]any{"type": "workspace", "value": workspaceLeaf}}
	for _, v := range r.VendorState {
		leaves = append(leaves, map[string]any{"type": "vendor_state", "value": v})
	}
	raw, e := jcs(leaves)
	if e != nil {
		return fail()
	}
	composite := hash(append([]byte("thinkpixel.checkpoint.composite/v1\x00"), raw...))
	issuer, key, pub := signer.Identity()
	pub = append(ed25519.PublicKey(nil), pub...)
	at := now.UTC().Format(time.RFC3339Nano)
	integrity := map[string]any{"canonicalization": "RFC8785-JCS", "digest_algorithm": "sha-256", "composite_root": composite, "signature_algorithm": "Ed25519", "signer": issuer, "key_id": key, "signed_at": at}
	lineage := map[string]any{"purpose": b.Purpose}
	if b.ParentCheckpointID != "" {
		lineage["parent_checkpoint_id"] = b.ParentCheckpointID
	}
	vendor := r.VendorState
	if vendor == nil {
		vendor = []port.VendorObject{}
	}
	manifest := map[string]any{"schema_version": "thinkpixel.checkpoint/v1", "checkpoint_id": r.ID, "tenant_id": r.Boundary.TenantID, "session_id": b.SessionID, "operation_id": b.OperationID, "created_at": at, "committed_at": at, "runtime": map[string]any{"agent_runtime_spec_id": b.RuntimeSpecID, "agent_runtime_spec_digest": b.RuntimeSpecDigest, "adapter_kind": b.AdapterKind, "adapter_version": b.AdapterVersion, "adapter_build_digest": b.AdapterBuildDigest, "protocol": port.VersionedName{Name: b.ProtocolName, Version: b.ProtocolVersion}, "state_format": port.VersionedName{Name: b.StateFormatName, Version: b.StateFormatVersion}, "runtime_profile_digest": b.RuntimeProfileDigest}, "workspace": workspaceLeaf, "vendor_state": vendor, "lineage": lineage, "integrity": integrity, "exclusions": domain.RequiredExclusions}
	raw, e = jcs(manifest)
	if e != nil {
		return fail()
	}
	payload := hash(raw)
	// payload_digest and signature are omitted from the hashed envelope to avoid
	// self-reference. All other integrity fields are covered.
	integrity["payload_digest"] = payload
	signed, e := jcs([]any{"thinkpixel.checkpoint.signature/v1", "thinkpixel.checkpoint/v1", r.ID, r.Boundary.TenantID, b.SessionID, payload, at})
	if e != nil {
		return fail()
	}
	sig, e := signer.Sign(ctx, append([]byte(nil), signed...))
	if e != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, signed, sig) {
		return fail()
	}
	integrity["signature"] = base64.RawURLEncoding.EncodeToString(sig)
	raw, e = jcs(manifest)
	if e != nil || len(raw) > 262144 {
		return fail()
	}
	compiler := jsonschema.NewCompiler()
	schemaJSON, e := jsonschema.UnmarshalJSON(strings.NewReader(contracts.CheckpointManifestSchema()))
	if e != nil {
		return fail()
	}
	if e = compiler.AddResource("checkpoint.json", schemaJSON); e != nil {
		return fail()
	}
	schema, e := compiler.Compile("checkpoint.json")
	if e != nil {
		return fail()
	}
	value, e := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if e != nil || schema.Validate(value) != nil {
		return fail()
	}
	vendors, _ := jcs(vendor)
	return domain.Integrity{CanonicalManifest: raw, Canonicalization: "RFC8785-JCS", DigestAlgorithm: "sha-256", PayloadDigest: payload, CompositeRoot: composite, SignatureAlgorithm: "Ed25519", Signer: issuer, KeyID: key, Signature: base64.RawURLEncoding.EncodeToString(sig), SignedAt: now.UTC(), VendorState: vendors, Exclusions: append([]string(nil), domain.RequiredExclusions...)}, nil
}
