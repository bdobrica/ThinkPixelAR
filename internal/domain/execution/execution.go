// Package execution contains the durable logical Execution aggregate.
package execution

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// State is the closed Execution lifecycle state.
type State string

const (
	Queued        State = "QUEUED"
	Materializing State = "MATERIALIZING"
	Running       State = "RUNNING"
	Cancelling    State = "CANCELLING"
	TimingOut     State = "TIMING_OUT"
	Succeeded     State = "SUCCEEDED"
	Failed        State = "FAILED"
	Cancelled     State = "CANCELLED"
	TimedOut      State = "TIMED_OUT"
)

var (
	states               = []State{Queued, Materializing, Running, Cancelling, TimingOut, Succeeded, Failed, Cancelled, TimedOut}
	ErrInvalidExecution  = errors.New("invalid execution")
	ErrIllegalTransition = errors.New("illegal execution transition")
	ErrVersionConflict   = errors.New("execution state version conflict")
	digestPattern        = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ParseState validates a closed Execution state value.
func ParseState(value string) (State, error) { return primitives.ParseEnum(value, states...) }

// Binding is the immutable Session, authority, and resolved agent evidence for an Execution.
type Binding struct {
	SessionID           primitives.ID
	SessionGeneration   uint64
	AuthorityMode       string
	AuthorityNamespace  string
	AuthorityReference  string
	ExternalRunID       string
	GrantDigest         string
	AgentID             string
	AgentVersionID      string
	AgentEvidence       []byte
	AgentEvidenceDigest string
}

// TerminalResult is a bounded immutable reference to the terminal outcome evidence.
type TerminalResult struct {
	Reference string
	Digest    string
}

// Execution is one bounded authorized operation. Private fields prevent mutation
// outside lifecycle and optimistic-version rules.
type Execution struct {
	tenantID       primitives.ID
	id             primitives.ID
	binding        Binding
	deadline       time.Time
	state          State
	stateVersion   uint64
	terminalResult *TerminalResult
	createdAt      time.Time
	updatedAt      time.Time
	terminalAt     *time.Time
}

// New creates a QUEUED Execution bound to a non-expired grant and Session generation.
func New(tenantID, executionID primitives.ID, binding Binding, deadline, now time.Time) (*Execution, error) {
	if !validID(tenantID) || !validID(executionID) || !validID(binding.SessionID) ||
		binding.SessionGeneration == 0 || now.IsZero() || deadline.IsZero() || !deadline.After(now) ||
		!validBinding(binding) {
		return nil, ErrInvalidExecution
	}
	now, deadline = now.UTC(), deadline.UTC()
	return &Execution{
		tenantID: tenantID, id: executionID, binding: cloneBinding(binding), deadline: deadline,
		state: Queued, createdAt: now, updatedAt: now,
	}, nil
}

func validID(id primitives.ID) bool {
	_, err := primitives.ParseID(string(id))
	return err == nil
}

func validBinding(binding Binding) bool {
	for _, value := range []string{
		binding.AuthorityMode, binding.AuthorityNamespace, binding.AuthorityReference,
		binding.AgentID, binding.AgentVersionID,
	} {
		if _, err := primitives.BoundedString(value, 1, 255, 255); err != nil {
			return false
		}
	}
	if binding.ExternalRunID != "" {
		if _, err := primitives.BoundedString(binding.ExternalRunID, 1, 255, 255); err != nil {
			return false
		}
	}
	return (binding.AuthorityMode == "LOCAL" || binding.AuthorityMode == "THINKPIXEL_AG") &&
		digestPattern.MatchString(binding.GrantDigest) && validJSONObject(binding.AgentEvidence) &&
		digestPattern.MatchString(binding.AgentEvidenceDigest)
}

func validJSONObject(value []byte) bool {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) < 2 || len(trimmed) > 65536 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(trimmed, &object) == nil && object != nil
}

// Transition applies one legal lifecycle edge using optimistic versioning.
// A terminal transition requires the bounded evidence reference that won.
func (e *Execution) Transition(next State, expectedVersion uint64, result *TerminalResult, now time.Time) error {
	if e == nil {
		return ErrInvalidExecution
	}
	if expectedVersion != e.stateVersion {
		return ErrVersionConflict
	}
	if _, err := ParseState(string(next)); err != nil || now.IsZero() || now.UTC().Before(e.updatedAt) {
		return ErrInvalidExecution
	}
	if next == e.state {
		if isTerminal(next) && sameResult(e.terminalResult, result) {
			return nil
		}
		return ErrIllegalTransition
	}
	if !legal[e.state][next] {
		return ErrIllegalTransition
	}
	if isTerminal(next) != (result != nil) || (result != nil && !validResult(*result)) {
		return ErrInvalidExecution
	}

	e.state = next
	e.stateVersion++
	e.updatedAt = now.UTC()
	if result != nil {
		copy := *result
		e.terminalResult = &copy
		terminalAt := now.UTC()
		e.terminalAt = &terminalAt
	}
	return nil
}

var legal = map[State]map[State]bool{
	Queued:        {Materializing: true, Cancelling: true, TimingOut: true, Failed: true},
	Materializing: {Running: true, Cancelling: true, TimingOut: true, Failed: true},
	Running:       {Materializing: true, Cancelling: true, TimingOut: true, Succeeded: true, Failed: true},
	Cancelling:    {Cancelled: true, Failed: true},
	TimingOut:     {TimedOut: true, Failed: true},
}

func isTerminal(state State) bool {
	return state == Succeeded || state == Failed || state == Cancelled || state == TimedOut
}

func validResult(result TerminalResult) bool {
	_, err := primitives.BoundedString(result.Reference, 1, 2048, 2048)
	return err == nil && digestPattern.MatchString(result.Digest)
}

func sameResult(left, right *TerminalResult) bool {
	return left != nil && right != nil && *left == *right
}

// Accessors expose immutable aggregate state.
func (e *Execution) TenantID() primitives.ID { return e.tenantID }
func (e *Execution) ID() primitives.ID       { return e.id }
func (e *Execution) Binding() Binding        { return cloneBinding(e.binding) }
func (e *Execution) Deadline() time.Time     { return e.deadline }
func (e *Execution) State() State            { return e.state }
func (e *Execution) StateVersion() uint64    { return e.stateVersion }
func (e *Execution) CreatedAt() time.Time    { return e.createdAt }
func (e *Execution) UpdatedAt() time.Time    { return e.updatedAt }
func (e *Execution) TerminalResult() (TerminalResult, bool) {
	if e.terminalResult == nil {
		return TerminalResult{}, false
	}
	return *e.terminalResult, true
}
func (e *Execution) TerminalAt() (time.Time, bool) {
	if e.terminalAt == nil {
		return time.Time{}, false
	}
	return *e.terminalAt, true
}

func cloneBinding(binding Binding) Binding {
	binding.AgentEvidence = append([]byte(nil), binding.AgentEvidence...)
	return binding
}
