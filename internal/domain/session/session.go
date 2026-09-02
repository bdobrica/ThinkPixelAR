// Package session contains the vendor-neutral Session aggregate and lifecycle.
package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// State is the durable Session lifecycle state.
type State string

const (
	Provisioning State = "PROVISIONING"
	Ready        State = "READY"
	Active       State = "ACTIVE"
	Idle         State = "IDLE"
	Suspended    State = "SUSPENDED"
	Degraded     State = "DEGRADED"
	Closing      State = "CLOSING"
	Closed       State = "CLOSED"
)

var (
	states                 = []State{Provisioning, Ready, Active, Idle, Suspended, Degraded, Closing, Closed}
	ErrInvalidSession      = errors.New("invalid session")
	ErrIllegalTransition   = errors.New("illegal session transition")
	ErrVersionConflict     = errors.New("session state version conflict")
	ErrGenerationExhausted = errors.New("session execution generation exhausted")
	sha256Digest           = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ParseState validates a closed Session state value.
func ParseState(value string) (State, error) { return primitives.ParseEnum(value, states...) }

// RuntimeBinding is the immutable runtime identity captured at Session creation.
type RuntimeBinding struct {
	AuthorityMode               string
	AuthorityNamespace          string
	AgentID                     string
	AgentVersionID              string
	RuntimeSpecSchemaVersion    string
	RuntimeSpec                 []byte
	RuntimeSpecDigest           string
	RuntimeProfileSchemaVersion string
	RuntimeProfileSnapshot      []byte
	RuntimeProfileDigest        string
}

// Session is the authoritative lifecycle aggregate. Its fields are private so
// callers cannot bypass transition, version, generation, or binding rules.
type Session struct {
	tenantID            primitives.ID
	id                  primitives.ID
	state               State
	stateVersion        uint64
	executionGeneration uint64
	binding             RuntimeBinding
	recoveryState       State
	createdAt           time.Time
	updatedAt           time.Time
	closedAt            *time.Time
}

// New creates a durable Session in PROVISIONING with version and generation zero.
func New(tenantID, sessionID primitives.ID, binding RuntimeBinding, now time.Time) (*Session, error) {
	if _, err := primitives.ParseID(string(tenantID)); err != nil {
		return nil, fmt.Errorf("%w: tenant ID", ErrInvalidSession)
	}
	if _, err := primitives.ParseID(string(sessionID)); err != nil {
		return nil, fmt.Errorf("%w: session ID", ErrInvalidSession)
	}
	if err := validateBinding(binding); err != nil {
		return nil, err
	}
	if now.IsZero() {
		return nil, fmt.Errorf("%w: creation time", ErrInvalidSession)
	}
	now = now.UTC()
	return &Session{
		tenantID: tenantID, id: sessionID, state: Provisioning,
		binding: cloneBinding(binding), createdAt: now, updatedAt: now,
	}, nil
}

func validateBinding(binding RuntimeBinding) error {
	values := []string{
		binding.AuthorityMode, binding.AuthorityNamespace, binding.AgentID,
		binding.AgentVersionID, binding.RuntimeSpecSchemaVersion,
		binding.RuntimeProfileSchemaVersion,
	}
	for _, value := range values {
		if _, err := primitives.BoundedString(value, 1, 255, 255); err != nil {
			return fmt.Errorf("%w: runtime binding", ErrInvalidSession)
		}
	}
	if binding.AuthorityMode != "LOCAL" && binding.AuthorityMode != "THINKPIXEL_AG" {
		return fmt.Errorf("%w: authority mode", ErrInvalidSession)
	}
	if !sha256Digest.MatchString(binding.RuntimeSpecDigest) || !sha256Digest.MatchString(binding.RuntimeProfileDigest) {
		return fmt.Errorf("%w: binding digest", ErrInvalidSession)
	}
	if !validJSONObject(binding.RuntimeSpec) || !validJSONObject(binding.RuntimeProfileSnapshot) {
		return fmt.Errorf("%w: binding snapshot", ErrInvalidSession)
	}
	return nil
}

func validJSONObject(value []byte) bool {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) < 2 || len(trimmed) > 65536 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(trimmed, &object) == nil && object != nil
}

// Transition validates and applies one lifecycle edge with optimistic versioning.
// Admission to ACTIVE also advances the independent execution generation once.
func (s *Session) Transition(next State, expectedVersion uint64, now time.Time) error {
	if s == nil {
		return ErrInvalidSession
	}
	if expectedVersion != s.stateVersion {
		return ErrVersionConflict
	}
	if _, err := ParseState(string(next)); err != nil || now.IsZero() || now.UTC().Before(s.updatedAt) {
		return ErrInvalidSession
	}
	if next == s.state {
		if next == Closing || next == Closed {
			return nil
		}
		return ErrIllegalTransition
	}
	if !s.canTransition(next) {
		return ErrIllegalTransition
	}
	previous := s.state
	if (previous == Ready || previous == Idle) && next == Active && s.executionGeneration == math.MaxInt64 {
		return ErrGenerationExhausted
	}
	if next == Degraded {
		s.recoveryState = previous
	} else if previous == Degraded {
		s.recoveryState = ""
	}
	if (previous == Ready || previous == Idle) && next == Active {
		s.executionGeneration++
	}
	s.state = next
	s.stateVersion++
	s.updatedAt = now.UTC()
	if next == Closed {
		closedAt := now.UTC()
		s.closedAt = &closedAt
	}
	return nil
}

func (s *Session) canTransition(next State) bool {
	if s.state != Closed && next == Closing {
		return true
	}
	if s.state == Degraded {
		return next == s.recoveryState
	}
	allowed := map[State]map[State]bool{
		Provisioning: {Ready: true, Degraded: true},
		Ready:        {Active: true, Suspended: true, Degraded: true},
		Active:       {Idle: true, Degraded: true},
		Idle:         {Active: true, Suspended: true, Degraded: true},
		Suspended:    {Ready: true, Idle: true, Degraded: true},
		Closing:      {Closed: true},
	}
	return allowed[s.state][next]
}

// Accessors expose immutable snapshots of aggregate state.
func (s *Session) TenantID() primitives.ID     { return s.tenantID }
func (s *Session) ID() primitives.ID           { return s.id }
func (s *Session) State() State                { return s.state }
func (s *Session) StateVersion() uint64        { return s.stateVersion }
func (s *Session) ExecutionGeneration() uint64 { return s.executionGeneration }
func (s *Session) RecoveryState() State        { return s.recoveryState }
func (s *Session) CreatedAt() time.Time        { return s.createdAt }
func (s *Session) UpdatedAt() time.Time        { return s.updatedAt }
func (s *Session) Binding() RuntimeBinding     { return cloneBinding(s.binding) }
func (s *Session) ClosedAt() (time.Time, bool) {
	if s.closedAt == nil {
		return time.Time{}, false
	}
	return *s.closedAt, true
}

func cloneBinding(binding RuntimeBinding) RuntimeBinding {
	binding.RuntimeSpec = append([]byte(nil), binding.RuntimeSpec...)
	binding.RuntimeProfileSnapshot = append([]byte(nil), binding.RuntimeProfileSnapshot...)
	return binding
}
