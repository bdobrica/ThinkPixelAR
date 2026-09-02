// Package checkpoint contains immutable, integrity-bound restore metadata.
package checkpoint

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type State string

const (
	Creating  State = "CREATING"
	Committed State = "COMMITTED"
	Deleting  State = "DELETING"
	Deleted   State = "DELETED"
)

type Purpose string

const (
	PurposeCheckpoint Purpose = "checkpoint"
	PurposeSuspend    Purpose = "suspend"
	PurposeFork       Purpose = "fork"
	PurposeMigration  Purpose = "migration"
)

var (
	ErrInvalidCheckpoint = errors.New("invalid checkpoint")
	ErrIllegalTransition = errors.New("illegal checkpoint transition")
	ErrVersionConflict   = errors.New("checkpoint state version conflict")
	digestPattern        = regexp.MustCompile(`^[A-Za-z0-9_-]{32,256}$`)
	signaturePattern     = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	states               = []State{Creating, Committed, Deleting, Deleted}
	purposes             = []Purpose{PurposeCheckpoint, PurposeSuspend, PurposeFork, PurposeMigration}
)

var RequiredExclusions = []string{
	"execution_credentials", "bootstrap_credentials", "provider_credentials",
	"gateway_tokens", "scm_and_tool_credentials", "signing_private_keys",
	"sandbox_process_authority",
}

func ParseState(value string) (State, error)     { return primitives.ParseEnum(value, states...) }
func ParsePurpose(value string) (Purpose, error) { return primitives.ParseEnum(value, purposes...) }

// Binding identifies the exact Session, Workspace generation, lineage, runtime,
// protocol, and state format for which this checkpoint may be restored.
type Binding struct {
	SessionID, WorkspaceID, WorkspaceGenerationID   primitives.ID
	WorkspaceGeneration                             uint64
	OperationID, ParentCheckpointID                 primitives.ID
	Purpose                                         Purpose
	RuntimeSpecID, RuntimeSpecDigest                string
	AdapterKind, AdapterVersion, AdapterBuildDigest string
	ProtocolName, ProtocolVersion                   string
	StateFormatName, StateFormatVersion             string
	RuntimeProfileDigest                            string
	RetentionDisposition                            string
}

// Integrity is the immutable signed publication evidence for a committed checkpoint.
type Integrity struct {
	CanonicalManifest  []byte
	Canonicalization   string
	DigestAlgorithm    string
	PayloadDigest      string
	CompositeRoot      string
	SignatureAlgorithm string
	Signer             string
	KeyID              string
	Signature          string
	SignedAt           time.Time
	VendorState        []byte
	Exclusions         []string
}

type Checkpoint struct {
	tenantID, id           primitives.ID
	binding                Binding
	state                  State
	stateVersion           uint64
	integrity              *Integrity
	createdAt, updatedAt   time.Time
	committedAt, deletedAt *time.Time
}

func New(tenantID, checkpointID primitives.ID, binding Binding, now time.Time) (*Checkpoint, error) {
	if !validID(tenantID) || !validID(checkpointID) || !validBinding(binding) || now.IsZero() {
		return nil, ErrInvalidCheckpoint
	}
	now = now.UTC()
	return &Checkpoint{tenantID: tenantID, id: checkpointID, binding: binding, state: Creating, createdAt: now, updatedAt: now}, nil
}

func (c *Checkpoint) Commit(integrity Integrity, expectedVersion uint64, now time.Time) error {
	if err := c.validateMutation(expectedVersion, now); err != nil {
		return err
	}
	if c.state != Creating || !validIntegrity(integrity) || integrity.SignedAt.UTC().After(now.UTC()) {
		return ErrInvalidCheckpoint
	}
	copy := cloneIntegrity(integrity)
	at := now.UTC()
	c.integrity, c.state, c.stateVersion, c.updatedAt, c.committedAt = &copy, Committed, c.stateVersion+1, at, &at
	return nil
}

func (c *Checkpoint) Delete(expectedVersion uint64, now time.Time) error {
	if err := c.validateMutation(expectedVersion, now); err != nil {
		return err
	}
	if c.state != Creating && c.state != Committed {
		return ErrIllegalTransition
	}
	c.state, c.stateVersion, c.updatedAt = Deleting, c.stateVersion+1, now.UTC()
	return nil
}

func (c *Checkpoint) MarkDeleted(expectedVersion uint64, now time.Time) error {
	if err := c.validateMutation(expectedVersion, now); err != nil {
		return err
	}
	if c.state != Deleting {
		return ErrIllegalTransition
	}
	at := now.UTC()
	c.state, c.stateVersion, c.updatedAt, c.deletedAt = Deleted, c.stateVersion+1, at, &at
	return nil
}

func (c *Checkpoint) validateMutation(expected uint64, now time.Time) error {
	if c == nil || now.IsZero() {
		return ErrInvalidCheckpoint
	}
	if expected != c.stateVersion {
		return ErrVersionConflict
	}
	if now.UTC().Before(c.updatedAt) {
		return ErrInvalidCheckpoint
	}
	return nil
}

func validBinding(b Binding) bool {
	if !validID(b.SessionID) || !validID(b.WorkspaceID) || !validID(b.WorkspaceGenerationID) || !validID(b.OperationID) || b.WorkspaceGeneration > math.MaxInt64 {
		return false
	}
	if _, err := ParsePurpose(string(b.Purpose)); err != nil {
		return false
	}
	if b.ParentCheckpointID != "" && !validID(b.ParentCheckpointID) {
		return false
	}
	for _, value := range []string{b.RuntimeSpecID, b.AdapterKind, b.AdapterVersion, b.ProtocolName, b.ProtocolVersion, b.StateFormatName, b.StateFormatVersion, b.RetentionDisposition} {
		if _, err := primitives.BoundedString(value, 1, 255, 255); err != nil {
			return false
		}
	}
	return validDigest(b.RuntimeSpecDigest) && validDigest(b.AdapterBuildDigest) && validDigest(b.RuntimeProfileDigest)
}

func validIntegrity(i Integrity) bool {
	if !validJSONObject(i.CanonicalManifest, 262144) || !validJSONArray(i.VendorState, 262144) || i.Canonicalization != "RFC8785-JCS" || (i.DigestAlgorithm != "sha-256" && i.DigestAlgorithm != "sha-512") || i.SignedAt.IsZero() || !sameExclusions(i.Exclusions) {
		return false
	}
	for _, value := range []string{i.SignatureAlgorithm, i.Signer, i.KeyID} {
		if _, err := primitives.BoundedString(value, 1, 128, 128); err != nil {
			return false
		}
	}
	return validDigest(i.PayloadDigest) && validDigest(i.CompositeRoot) && len(i.Signature) >= 32 && len(i.Signature) <= 4096 && signaturePattern.MatchString(i.Signature)
}

func sameExclusions(values []string) bool {
	if len(values) != len(RequiredExclusions) {
		return false
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if seen[value] {
			return false
		}
		seen[value] = true
	}
	for _, required := range RequiredExclusions {
		if !seen[required] {
			return false
		}
	}
	return true
}
func validID(id primitives.ID) bool                { _, err := primitives.ParseID(string(id)); return err == nil }
func validDigest(value string) bool                { return digestPattern.MatchString(value) }
func validJSONObject(value []byte, limit int) bool { return validJSONKind(value, limit, '{') }
func validJSONArray(value []byte, limit int) bool  { return validJSONKind(value, limit, '[') }
func validJSONKind(value []byte, limit int, first byte) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) >= 2 && len(trimmed) <= limit && trimmed[0] == first && json.Valid(trimmed)
}
func cloneIntegrity(i Integrity) Integrity {
	i.CanonicalManifest = append([]byte(nil), i.CanonicalManifest...)
	i.VendorState = append([]byte(nil), i.VendorState...)
	i.Exclusions = append([]string(nil), i.Exclusions...)
	return i
}

func (c *Checkpoint) TenantID() primitives.ID { return c.tenantID }
func (c *Checkpoint) ID() primitives.ID       { return c.id }
func (c *Checkpoint) Binding() Binding        { return c.binding }
func (c *Checkpoint) State() State            { return c.state }
func (c *Checkpoint) StateVersion() uint64    { return c.stateVersion }
func (c *Checkpoint) Integrity() (Integrity, bool) {
	if c.integrity == nil {
		return Integrity{}, false
	}
	return cloneIntegrity(*c.integrity), true
}
func (c *Checkpoint) CreatedAt() time.Time { return c.createdAt }
func (c *Checkpoint) UpdatedAt() time.Time { return c.updatedAt }
func (c *Checkpoint) CommittedAt() (time.Time, bool) {
	if c.committedAt == nil {
		return time.Time{}, false
	}
	return *c.committedAt, true
}
func (c *Checkpoint) DeletedAt() (time.Time, bool) {
	if c.deletedAt == nil {
		return time.Time{}, false
	}
	return *c.deletedAt, true
}
