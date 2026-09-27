package checkpoint

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bdobrica/ThinkPixelAR/docs/contracts"
	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/checkpoint"
	port "github.com/bdobrica/ThinkPixelAR/internal/ports/checkpoint"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	canonical "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	ErrInvalidRestore = errors.New("INVALID_CHECKPOINT")
	ErrIncompatible   = errors.New("INCOMPATIBLE_CHECKPOINT")
)

// RestoreTarget comes from trusted, currently authorized COMMITTED metadata, not
// from the manifest or a caller's claim. The caller must retain the selected
// generation/objects through restore and recheck execution authority and fences
// before starting a harness. Validation neither issues authority nor starts work.
type RestoreTarget struct {
	TenantID, CheckpointID primitives.ID
	State                  domain.State
	Binding                domain.Binding
	Workspace              WorkspaceManifest
}

type WorkspaceManifest struct {
	ID             primitives.ID `json:"workspace_id"`
	GenerationID   primitives.ID `json:"generation_id"`
	Generation     uint64        `json:"generation"`
	Snapshot       string        `json:"snapshot_ref"`
	Root           string        `json:"manifest_root"`
	Algorithm      string        `json:"digest_algorithm"`
	EvidenceDigest string        `json:"storage_evidence_digest"`
}

type RuntimeManifest struct {
	SpecID         string             `json:"agent_runtime_spec_id"`
	SpecDigest     string             `json:"agent_runtime_spec_digest"`
	AdapterKind    string             `json:"adapter_kind"`
	AdapterVersion string             `json:"adapter_version"`
	AdapterBuild   string             `json:"adapter_build_digest"`
	Protocol       port.VersionedName `json:"protocol"`
	StateFormat    port.VersionedName `json:"state_format"`
	ProfileDigest  string             `json:"runtime_profile_digest"`
}

type restoreManifest struct {
	Schema    string              `json:"schema_version"`
	ID        primitives.ID       `json:"checkpoint_id"`
	Tenant    primitives.ID       `json:"tenant_id"`
	Session   primitives.ID       `json:"session_id"`
	Operation primitives.ID       `json:"operation_id"`
	Created   string              `json:"created_at"`
	Committed string              `json:"committed_at"`
	Runtime   RuntimeManifest     `json:"runtime"`
	Workspace WorkspaceManifest   `json:"workspace"`
	Vendors   []port.VendorObject `json:"vendor_state"`
	Lineage   struct {
		Purpose domain.Purpose `json:"purpose"`
		Parent  primitives.ID  `json:"parent_checkpoint_id"`
	} `json:"lineage"`
	Integrity struct {
		Canonicalization   string `json:"canonicalization"`
		Algorithm          string `json:"digest_algorithm"`
		Payload            string `json:"payload_digest"`
		Composite          string `json:"composite_root"`
		SignatureAlgorithm string `json:"signature_algorithm"`
		Issuer             string `json:"signer"`
		Key                string `json:"key_id"`
		Signature          string `json:"signature"`
		Signed             string `json:"signed_at"`
	} `json:"integrity"`
}

// RestoreChecks are mandatory trusted adapters; none is inferred from signed
// content. They must honor context, return no content in errors and retain no
// mutable inputs. Keys enforces issuer/key lifecycle policy at validation time.
// Compatible checks the registered adapter's conformance, platform/capabilities,
// exact protocol and all vendor formats against the immutable runtime binding.
// Workspace independently verifies the immutable snapshot/root/evidence and
// credential exclusions, and pins it through restore. OpenVendor resolves only
// the tenant-scoped immutable reference, verifies storage metadata and credential
// exclusions, pins it through restore, and returns its exact bytes. All objects,
// including optional ones, are checked in this initial conservative profile.
type RestoreChecks struct {
	Keys           func(context.Context, primitives.ID, string, string, time.Time) (ed25519.PublicKey, error)
	Compatible     func(context.Context, RuntimeManifest, []port.VendorObject) error
	Workspace      func(context.Context, primitives.ID, WorkspaceManifest) error
	OpenVendor     func(context.Context, primitives.ID, port.VendorObject) (io.ReadCloser, error)
	MaxVendorBytes int64
}

type RestoreValidator struct {
	checks RestoreChecks
	schema *jsonschema.Schema
}

func NewRestoreValidator(checks RestoreChecks) (*RestoreValidator, error) {
	if checks.Keys == nil || checks.Compatible == nil || checks.Workspace == nil || checks.OpenVendor == nil || checks.MaxVendorBytes <= 0 || checks.MaxVendorBytes > 9007199254740991 {
		return nil, ErrInvalidRestore
	}
	compiler := jsonschema.NewCompiler()
	value, err := jsonschema.UnmarshalJSON(strings.NewReader(contracts.CheckpointManifestSchema()))
	if err != nil {
		return nil, ErrInvalidRestore
	}
	if err = compiler.AddResource("checkpoint.json", value); err != nil {
		return nil, ErrInvalidRestore
	}
	schema, err := compiler.Compile("checkpoint.json")
	if err != nil {
		return nil, ErrInvalidRestore
	}
	return &RestoreValidator{checks, schema}, nil
}

// Validate fails closed before any restoration or harness operation. It supports
// the CHK-001 SHA-256/Ed25519 profile with exact runtime/build matching; adapter
// upgrades and migration need a separate explicitly qualified path. Success is
// only a point-in-time integrity result, never a reusable admission token.
func (v *RestoreValidator) Validate(ctx context.Context, raw []byte, target RestoreTarget) error {
	if v == nil || v.schema == nil || ctx.Err() != nil || target.State != domain.Committed || len(raw) == 0 || len(raw) > 262144 || !utf8.Valid(raw) {
		return ErrInvalidRestore
	}
	raw = bytes.Clone(raw)
	// Bound nesting before invoking JCS. JCS rejects duplicate keys and malformed
	// Unicode escapes; exact byte equality rejects alternate encodings/numbers.
	depth := 0
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ErrInvalidRestore
		}
		if d, ok := token.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
			if depth > 16 {
				return ErrInvalidRestore
			}
		}
	}
	normalized, err := canonical.Transform(raw)
	if err != nil || !bytes.Equal(normalized, raw) {
		return ErrInvalidRestore
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil || v.schema.Validate(value) != nil {
		return ErrInvalidRestore
	}
	var m restoreManifest
	if json.Unmarshal(raw, &m) != nil {
		return ErrInvalidRestore
	}
	// No extension semantics are currently registered, even though the envelope
	// permits namespaced extensions. Unknown semantics cannot silently be ignored.
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields["extensions"] != nil {
		return ErrIncompatible
	}
	i := m.Integrity
	if i.Canonicalization != "RFC8785-JCS" || i.Algorithm != "sha-256" || i.SignatureAlgorithm != "Ed25519" || m.Workspace.Algorithm != "sha-256" {
		return ErrIncompatible
	}
	for _, d := range []string{i.Payload, i.Composite, m.Workspace.Root, m.Workspace.EvidenceDigest, m.Runtime.SpecDigest, m.Runtime.AdapterBuild, m.Runtime.ProfileDigest} {
		if !sha256Hex(d) {
			return ErrInvalidRestore
		}
	}
	if m.Workspace.Generation > 9007199254740991 {
		return ErrInvalidRestore
	}
	total := int64(0)
	for n, object := range m.Vendors {
		if object.Algorithm != "sha-256" {
			return ErrIncompatible
		}
		if !sha256Hex(object.Digest) || object.Size < 0 || object.Size > v.checks.MaxVendorBytes-total || n > 0 && m.Vendors[n-1].ID >= object.ID {
			return ErrInvalidRestore
		}
		total += object.Size
	}
	signed, err := time.Parse(time.RFC3339Nano, i.Signed)
	created, ce := time.Parse(time.RFC3339Nano, m.Created)
	committed, co := time.Parse(time.RFC3339Nano, m.Committed)
	if err != nil || ce != nil || co != nil || signed.Before(created) || committed.Before(signed) {
		return ErrInvalidRestore
	}
	var integrity map[string]json.RawMessage
	if json.Unmarshal(fields["integrity"], &integrity) != nil {
		return ErrInvalidRestore
	}
	delete(integrity, "signature")
	delete(integrity, "payload_digest")
	fields["integrity"], _ = json.Marshal(integrity)
	payload, err := jcs(fields)
	if err != nil || hash(payload) != i.Payload {
		return ErrInvalidRestore
	}
	leaves := []any{map[string]any{"type": "workspace", "value": m.Workspace}}
	for _, object := range m.Vendors {
		leaves = append(leaves, map[string]any{"type": "vendor_state", "value": object})
	}
	composite, err := jcs(leaves)
	if err != nil || hash(append([]byte("thinkpixel.checkpoint.composite/v1\x00"), composite...)) != i.Composite {
		return ErrInvalidRestore
	}
	b := target.Binding
	if m.ID != target.CheckpointID || m.Tenant != target.TenantID || m.Session != b.SessionID || m.Operation != b.OperationID || m.Lineage.Parent != b.ParentCheckpointID || m.Lineage.Purpose != b.Purpose || m.Workspace != target.Workspace || m.Workspace.ID != b.WorkspaceID || m.Workspace.GenerationID != b.WorkspaceGenerationID || m.Workspace.Generation != b.WorkspaceGeneration {
		return ErrInvalidRestore
	}
	expected := RuntimeManifest{b.RuntimeSpecID, b.RuntimeSpecDigest, b.AdapterKind, b.AdapterVersion, b.AdapterBuildDigest, port.VersionedName{Name: b.ProtocolName, Version: b.ProtocolVersion}, port.VersionedName{Name: b.StateFormatName, Version: b.StateFormatVersion}, b.RuntimeProfileDigest}
	if m.Runtime != expected {
		return ErrIncompatible
	}
	key, err := v.checks.Keys(ctx, target.TenantID, i.Issuer, i.Key, signed)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return ErrInvalidRestore
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(i.Signature)
	message, me := jcs([]any{"thinkpixel.checkpoint.signature/v1", m.Schema, m.ID, m.Tenant, m.Session, i.Payload, i.Signed})
	if err != nil || me != nil || !ed25519.Verify(key, message, sig) {
		return ErrInvalidRestore
	}
	if v.checks.Compatible(ctx, m.Runtime, append([]port.VendorObject(nil), m.Vendors...)) != nil {
		return ErrIncompatible
	}
	if ctx.Err() != nil || v.checks.Workspace(ctx, target.TenantID, m.Workspace) != nil {
		return ErrInvalidRestore
	}
	for _, object := range m.Vendors {
		if ctx.Err() != nil {
			return ErrInvalidRestore
		}
		reader, err := v.checks.OpenVendor(ctx, target.TenantID, object)
		if err != nil {
			if reader != nil {
				_ = reader.Close()
			}
			return ErrInvalidRestore
		}
		if reader == nil {
			return ErrInvalidRestore
		}
		digest := sha256.New()
		count, copyErr := io.Copy(digest, io.LimitReader(contextReader{ctx, reader}, object.Size+1))
		closeErr := reader.Close()
		if copyErr != nil || closeErr != nil || count != object.Size || hex.EncodeToString(digest.Sum(nil)) != object.Digest {
			return ErrInvalidRestore
		}
	}
	if ctx.Err() != nil {
		return ErrInvalidRestore
	}
	return nil
}

func sha256Hex(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
