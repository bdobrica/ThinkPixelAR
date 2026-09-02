package runtimeprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var (
	ErrInvalidSnapshot = errors.New("invalid runtime profile resolution snapshot")
	digestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	identifierPattern  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?$`)
)

// Snapshot is the immutable result of resolving one Runtime Profile for one
// Execution. JSON fields contain canonical RFC 8785 bytes; infrastructure
// details remain behind opaque operator-controlled references.
type Snapshot struct {
	tenantID                   primitives.ID
	executionID                primitives.ID
	schemaVersion              uint64
	profileName                string
	canonicalResolution        []byte
	resolutionDigest           string
	implementationReference    string
	implementationVersion      string
	implementationDigest       string
	canonicalSupportedVersions []byte
	supportedVersionsDigest    string
	decisionReason             string
	createdAt                  time.Time
}

// NewSnapshot validates and captures an immutable per-Execution resolution.
func NewSnapshot(tenantID, executionID primitives.ID, schemaVersion uint64, profileName string,
	canonicalResolution []byte, resolutionDigest, implementationReference, implementationVersion,
	implementationDigest string, canonicalSupportedVersions []byte, supportedVersionsDigest,
	decisionReason string, now time.Time) (*Snapshot, error) {
	if !validID(tenantID) || !validID(executionID) || schemaVersion != 1 || !validIdentifier(profileName) ||
		!validJSONObject(canonicalResolution, 65536) || !digestPattern.MatchString(resolutionDigest) ||
		!validBounded(implementationReference, 255) || !validBounded(implementationVersion, 128) ||
		!digestPattern.MatchString(implementationDigest) || !validJSONObject(canonicalSupportedVersions, 32768) ||
		!digestPattern.MatchString(supportedVersionsDigest) || !validBounded(decisionReason, 255) || now.IsZero() {
		return nil, ErrInvalidSnapshot
	}
	return &Snapshot{
		tenantID: tenantID, executionID: executionID, schemaVersion: schemaVersion, profileName: profileName,
		canonicalResolution: append([]byte(nil), canonicalResolution...), resolutionDigest: resolutionDigest,
		implementationReference: implementationReference, implementationVersion: implementationVersion,
		implementationDigest:       implementationDigest,
		canonicalSupportedVersions: append([]byte(nil), canonicalSupportedVersions...),
		supportedVersionsDigest:    supportedVersionsDigest, decisionReason: decisionReason, createdAt: now.UTC(),
	}, nil
}

func validID(id primitives.ID) bool { _, err := primitives.ParseID(string(id)); return err == nil }

func validIdentifier(value string) bool {
	return len(value) <= 128 && identifierPattern.MatchString(value)
}

func validBounded(value string, maximum int) bool {
	_, err := primitives.BoundedString(value, 1, maximum, maximum)
	return err == nil
}

func validJSONObject(value []byte, maximum int) bool {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) < 2 || len(trimmed) > maximum || trimmed[0] != '{' || !json.Valid(trimmed) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(trimmed, &object) == nil && object != nil
}

func (s *Snapshot) TenantID() primitives.ID         { return s.tenantID }
func (s *Snapshot) ExecutionID() primitives.ID      { return s.executionID }
func (s *Snapshot) SchemaVersion() uint64           { return s.schemaVersion }
func (s *Snapshot) ProfileName() string             { return s.profileName }
func (s *Snapshot) ResolutionDigest() string        { return s.resolutionDigest }
func (s *Snapshot) ImplementationReference() string { return s.implementationReference }
func (s *Snapshot) ImplementationVersion() string   { return s.implementationVersion }
func (s *Snapshot) ImplementationDigest() string    { return s.implementationDigest }
func (s *Snapshot) SupportedVersionsDigest() string { return s.supportedVersionsDigest }
func (s *Snapshot) DecisionReason() string          { return s.decisionReason }
func (s *Snapshot) CreatedAt() time.Time            { return s.createdAt }
func (s *Snapshot) CanonicalResolution() []byte {
	return append([]byte(nil), s.canonicalResolution...)
}
func (s *Snapshot) CanonicalSupportedVersions() []byte {
	return append([]byte(nil), s.canonicalSupportedVersions...)
}
