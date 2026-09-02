// Package workspace contains durable, provider-neutral Workspace domain concepts.
package workspace

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
	Provisioning State = "PROVISIONING"
	Ready        State = "READY"
	Attached     State = "ATTACHED"
	Snapshotting State = "SNAPSHOTTING"
	Degraded     State = "DEGRADED"
	Deleting     State = "DELETING"
	Deleted      State = "DELETED"
)

var (
	states                 = []State{Provisioning, Ready, Attached, Snapshotting, Degraded, Deleting, Deleted}
	ErrInvalidWorkspace    = errors.New("invalid workspace")
	ErrIllegalTransition   = errors.New("illegal workspace transition")
	ErrVersionConflict     = errors.New("workspace state version conflict")
	ErrInvalidGeneration   = errors.New("invalid workspace generation")
	ErrGenerationExhausted = errors.New("workspace generation exhausted")
	digestPattern          = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

func ParseState(value string) (State, error) { return primitives.ParseEnum(value, states...) }

// Binding is immutable provider, storage, source, and creation evidence.
type Binding struct {
	SessionID            primitives.ID
	ProviderKind         string
	ProviderReference    string
	CapacityBytes        uint64
	AccessMode           string
	VolumeMode           string
	EncryptionClass      string
	StorageProfile       string
	ConfigDigest         string
	SourceType           string
	SourceReference      string
	Provenance           []byte
	ProvenanceDigest     string
	CreateOperationID    primitives.ID
	RetentionDisposition string
}

type Workspace struct {
	tenantID, id         primitives.ID
	binding              Binding
	state                State
	stateVersion         uint64
	currentGeneration    uint64
	currentGenerationID  *primitives.ID
	recoveryState        State
	createdAt, updatedAt time.Time
	deletedAt            *time.Time
}

func New(tenantID, workspaceID primitives.ID, binding Binding, now time.Time) (*Workspace, error) {
	if !validID(tenantID) || !validID(workspaceID) || !validBinding(binding) || now.IsZero() {
		return nil, ErrInvalidWorkspace
	}
	now = now.UTC()
	return &Workspace{tenantID: tenantID, id: workspaceID, binding: cloneBinding(binding), state: Provisioning, createdAt: now, updatedAt: now}, nil
}

func validBinding(b Binding) bool {
	if !validID(b.SessionID) || !validID(b.CreateOperationID) || b.CapacityBytes == 0 || b.CapacityBytes > math.MaxInt64 || b.ProviderReference == "" || b.SourceReference == "" {
		return false
	}
	for _, value := range []string{b.ProviderKind, b.ProviderReference, b.AccessMode, b.VolumeMode, b.EncryptionClass, b.StorageProfile, b.SourceType, b.SourceReference, b.RetentionDisposition} {
		if _, err := primitives.BoundedString(value, 1, 2048, 2048); err != nil {
			return false
		}
	}
	return digestPattern.MatchString(b.ConfigDigest) && digestPattern.MatchString(b.ProvenanceDigest) && validJSONObject(b.Provenance)
}

func (w *Workspace) Transition(next State, expectedVersion uint64, now time.Time) error {
	if w == nil {
		return ErrInvalidWorkspace
	}
	if expectedVersion != w.stateVersion {
		return ErrVersionConflict
	}
	if _, err := ParseState(string(next)); err != nil || now.IsZero() || now.UTC().Before(w.updatedAt) {
		return ErrInvalidWorkspace
	}
	if next == w.state {
		if next == Deleting || next == Deleted {
			return nil
		}
		return ErrIllegalTransition
	}
	if !workspaceTransitions[w.state][next] || (w.state == Degraded && next != w.recoveryState && next != Deleting) {
		return ErrIllegalTransition
	}
	previous := w.state
	if next == Degraded {
		w.recoveryState = previous
	} else if previous == Degraded {
		w.recoveryState = ""
	}
	w.state, w.stateVersion, w.updatedAt = next, w.stateVersion+1, now.UTC()
	if next == Deleted {
		at := now.UTC()
		w.deletedAt = &at
	}
	return nil
}

var workspaceTransitions = map[State]map[State]bool{
	Provisioning: {Ready: true, Degraded: true, Deleting: true},
	Ready:        {Attached: true, Snapshotting: true, Degraded: true, Deleting: true},
	Attached:     {Ready: true, Snapshotting: true, Degraded: true, Deleting: true},
	Snapshotting: {Ready: true, Attached: true, Degraded: true, Deleting: true},
	Degraded:     {Provisioning: true, Ready: true, Attached: true, Snapshotting: true, Deleting: true},
	Deleting:     {Deleted: true},
}

// Publish makes a committed generation current. Publication must be atomic with persistence.
func (w *Workspace) Publish(g Generation, expectedVersion uint64, now time.Time) error {
	if w == nil {
		return ErrInvalidWorkspace
	}
	if expectedVersion != w.stateVersion {
		return ErrVersionConflict
	}
	if w.state != Provisioning || now.IsZero() || now.UTC().Before(w.updatedAt) || g.tenantID != w.tenantID || g.workspaceID != w.id || g.sessionID != w.binding.SessionID || g.generation != 0 || w.currentGenerationID != nil {
		return ErrInvalidGeneration
	}
	id := g.id
	w.currentGenerationID, w.stateVersion, w.updatedAt = &id, w.stateVersion+1, now.UTC()
	return nil
}

// Advance publishes the direct child of the current generation.
func (w *Workspace) Advance(g Generation, expectedVersion uint64, now time.Time) error {
	if w == nil {
		return ErrInvalidWorkspace
	}
	if expectedVersion != w.stateVersion {
		return ErrVersionConflict
	}
	if w.currentGeneration == math.MaxInt64 {
		return ErrGenerationExhausted
	}
	if w.state != Snapshotting || w.currentGenerationID == nil || g.tenantID != w.tenantID || g.workspaceID != w.id || g.sessionID != w.binding.SessionID || g.generation != w.currentGeneration+1 || g.parentID == nil || *g.parentID != *w.currentGenerationID || now.IsZero() || now.UTC().Before(w.updatedAt) {
		return ErrInvalidGeneration
	}
	id := g.id
	w.currentGeneration, w.currentGenerationID, w.stateVersion, w.updatedAt = g.generation, &id, w.stateVersion+1, now.UTC()
	return nil
}

type GenerationBinding struct {
	OperationID                primitives.ID
	ProviderSnapshotReference  string
	IntegrityAlgorithm         string
	IntegrityRoot              string
	ManifestDigest             string
	LogicalBytes               uint64
	LogicalFiles               uint64
	CreatorExecutionID         primitives.ID
	CreatorAttemptID           primitives.ID
	CreatorExecutionGeneration uint64
	StorageEvidence            []byte
	StorageEvidenceDigest      string
	Classification             string
	RetentionDisposition       string
}

// Generation is immutable committed Workspace boundary metadata.
type Generation struct {
	tenantID, id, workspaceID, sessionID primitives.ID
	generation                           uint64
	parentID                             *primitives.ID
	binding                              GenerationBinding
	createdAt                            time.Time
}

func NewGeneration(tenantID, generationID, workspaceID, sessionID primitives.ID, number uint64, parent *Generation, binding GenerationBinding, now time.Time) (Generation, error) {
	if !validID(tenantID) || !validID(generationID) || !validID(workspaceID) || !validID(sessionID) || number > math.MaxInt64 || now.IsZero() || !validGenerationBinding(binding) {
		return Generation{}, ErrInvalidGeneration
	}
	var parentID *primitives.ID
	if number == 0 {
		if parent != nil {
			return Generation{}, ErrInvalidGeneration
		}
	} else {
		if parent == nil || parent.tenantID != tenantID || parent.workspaceID != workspaceID || parent.sessionID != sessionID || parent.generation == math.MaxInt64 || number != parent.generation+1 {
			return Generation{}, ErrInvalidGeneration
		}
		id := parent.id
		parentID = &id
	}
	return Generation{tenantID: tenantID, id: generationID, workspaceID: workspaceID, sessionID: sessionID, generation: number, parentID: parentID, binding: cloneGenerationBinding(binding), createdAt: now.UTC()}, nil
}

func validGenerationBinding(b GenerationBinding) bool {
	if !validID(b.OperationID) || b.LogicalBytes > math.MaxInt64 || b.LogicalFiles > math.MaxInt64 || !digestPattern.MatchString(b.IntegrityRoot) || !digestPattern.MatchString(b.ManifestDigest) || !digestPattern.MatchString(b.StorageEvidenceDigest) || !validJSONObject(b.StorageEvidence) {
		return false
	}
	for _, value := range []string{b.IntegrityAlgorithm, b.Classification, b.RetentionDisposition} {
		if _, err := primitives.BoundedString(value, 1, 2048, 2048); err != nil {
			return false
		}
	}
	if b.ProviderSnapshotReference != "" {
		if _, err := primitives.BoundedString(b.ProviderSnapshotReference, 1, 2048, 2048); err != nil {
			return false
		}
	}
	if b.CreatorExecutionGeneration == 0 {
		return b.CreatorExecutionID == "" && b.CreatorAttemptID == ""
	}
	return validID(b.CreatorExecutionID) && validID(b.CreatorAttemptID)
}

func validID(id primitives.ID) bool { _, err := primitives.ParseID(string(id)); return err == nil }
func validJSONObject(value []byte) bool {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) < 2 || len(trimmed) > 65536 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(trimmed, &object) == nil && object != nil
}
func cloneBinding(b Binding) Binding { b.Provenance = append([]byte(nil), b.Provenance...); return b }
func cloneGenerationBinding(b GenerationBinding) GenerationBinding {
	b.StorageEvidence = append([]byte(nil), b.StorageEvidence...)
	return b
}

func (w *Workspace) TenantID() primitives.ID { return w.tenantID }
func (w *Workspace) ID() primitives.ID       { return w.id }
func (w *Workspace) Binding() Binding        { return cloneBinding(w.binding) }
func (w *Workspace) State() State            { return w.state }
func (w *Workspace) StateVersion() uint64    { return w.stateVersion }
func (w *Workspace) CurrentGeneration() (uint64, primitives.ID, bool) {
	if w.currentGenerationID == nil {
		return 0, "", false
	}
	return w.currentGeneration, *w.currentGenerationID, true
}
func (w *Workspace) RecoveryState() State       { return w.recoveryState }
func (w *Workspace) CreatedAt() time.Time       { return w.createdAt }
func (w *Workspace) UpdatedAt() time.Time       { return w.updatedAt }
func (g Generation) TenantID() primitives.ID    { return g.tenantID }
func (g Generation) ID() primitives.ID          { return g.id }
func (g Generation) WorkspaceID() primitives.ID { return g.workspaceID }
func (g Generation) SessionID() primitives.ID   { return g.sessionID }
func (g Generation) Number() uint64             { return g.generation }
func (g Generation) ParentID() (primitives.ID, bool) {
	if g.parentID == nil {
		return "", false
	}
	return *g.parentID, true
}
func (g Generation) Binding() GenerationBinding { return cloneGenerationBinding(g.binding) }
func (g Generation) CreatedAt() time.Time       { return g.createdAt }
