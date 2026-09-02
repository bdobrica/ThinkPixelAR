// Package attempt contains the durable physical Attempt aggregate.
package attempt

import (
	"errors"
	"regexp"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// State is the closed Attempt lifecycle state.
type State string

const (
	Pending      State = "PENDING"
	Acquiring    State = "ACQUIRING"
	Starting     State = "STARTING"
	Running      State = "RUNNING"
	Interrupting State = "INTERRUPTING"
	Succeeded    State = "SUCCEEDED"
	Failed       State = "FAILED"
	Cancelled    State = "CANCELLED"
	TimedOut     State = "TIMED_OUT"
	Replaced     State = "REPLACED"
)

var (
	states               = []State{Pending, Acquiring, Starting, Running, Interrupting, Succeeded, Failed, Cancelled, TimedOut, Replaced}
	ErrInvalidAttempt    = errors.New("invalid attempt")
	ErrIllegalTransition = errors.New("illegal attempt transition")
	ErrVersionConflict   = errors.New("attempt state version conflict")
	digestPattern        = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ParseState validates a closed Attempt state value.
func ParseState(value string) (State, error) { return primitives.ParseEnum(value, states...) }

// Binding is the immutable Execution generation and ordinal fence of an Attempt.
type Binding struct {
	ExecutionID         primitives.ID
	ExecutionGeneration uint64
	Number              uint64
}

// TerminalResult is a bounded immutable reference to physical-attempt evidence.
type TerminalResult struct {
	Reference string
	Digest    string
}

// Attempt is one physical try. Terminalization removes its current designation.
type Attempt struct {
	tenantID           primitives.ID
	id                 primitives.ID
	binding            Binding
	state              State
	stateVersion       uint64
	current            bool
	sandboxReference   primitives.ID
	harnessReference   primitives.ID
	sandboxHeartbeatAt *time.Time
	harnessHeartbeatAt *time.Time
	terminalResult     *TerminalResult
	createdAt          time.Time
	updatedAt          time.Time
	terminalAt         *time.Time
}

// New creates the newly designated current Attempt in PENDING.
func New(tenantID, attemptID primitives.ID, binding Binding, now time.Time) (*Attempt, error) {
	if !validID(tenantID) || !validID(attemptID) || !validID(binding.ExecutionID) ||
		binding.ExecutionGeneration == 0 || binding.Number == 0 || now.IsZero() {
		return nil, ErrInvalidAttempt
	}
	now = now.UTC()
	return &Attempt{tenantID: tenantID, id: attemptID, binding: binding, state: Pending, current: true, createdAt: now, updatedAt: now}, nil
}

// Restore reconstructs a previously validated aggregate from authoritative storage.
func Restore(tenantID, attemptID primitives.ID, binding Binding, state State, stateVersion uint64, current bool,
	sandboxReference, harnessReference primitives.ID, sandboxHeartbeatAt, harnessHeartbeatAt *time.Time,
	result *TerminalResult, createdAt, updatedAt time.Time, terminalAt *time.Time) (*Attempt, error) {
	if !validID(tenantID) || !validID(attemptID) || !validID(binding.ExecutionID) || binding.ExecutionGeneration == 0 || binding.Number == 0 ||
		createdAt.IsZero() || updatedAt.Before(createdAt) || (sandboxReference != "" && !validID(sandboxReference)) || (harnessReference != "" && !validID(harnessReference)) {
		return nil, ErrInvalidAttempt
	}
	if _, err := ParseState(string(state)); err != nil {
		return nil, ErrInvalidAttempt
	}
	if isTerminal(state) != (result != nil && terminalAt != nil) || (isTerminal(state) && current) || (result != nil && !validResult(*result)) {
		return nil, ErrInvalidAttempt
	}
	copyTime := func(value *time.Time) *time.Time {
		if value == nil {
			return nil
		}
		v := value.UTC()
		return &v
	}
	var resultCopy *TerminalResult
	if result != nil {
		v := *result
		resultCopy = &v
	}
	return &Attempt{tenantID: tenantID, id: attemptID, binding: binding, state: state, stateVersion: stateVersion, current: current,
		sandboxReference: sandboxReference, harnessReference: harnessReference, sandboxHeartbeatAt: copyTime(sandboxHeartbeatAt), harnessHeartbeatAt: copyTime(harnessHeartbeatAt),
		terminalResult: resultCopy, createdAt: createdAt.UTC(), updatedAt: updatedAt.UTC(), terminalAt: copyTime(terminalAt)}, nil
}

// BindSandbox records the opaque AR SandboxBinding reference once.
func (a *Attempt) BindSandbox(reference primitives.ID, expectedVersion uint64, now time.Time) error {
	return a.bind(&a.sandboxReference, reference, expectedVersion, now)
}

// BindHarness records the opaque AR HarnessBinding reference once.
func (a *Attempt) BindHarness(reference primitives.ID, expectedVersion uint64, now time.Time) error {
	return a.bind(&a.harnessReference, reference, expectedVersion, now)
}

func (a *Attempt) bind(target *primitives.ID, reference primitives.ID, expectedVersion uint64, now time.Time) error {
	if err := a.validateMutation(expectedVersion, now); err != nil {
		return err
	}
	if !validID(reference) || isTerminal(a.state) {
		return ErrInvalidAttempt
	}
	if *target != "" {
		if *target == reference {
			return nil
		}
		return ErrInvalidAttempt
	}
	*target = reference
	a.advance(now)
	return nil
}

// ObserveSandboxHeartbeat records a current Attempt's monotonic sandbox liveness observation.
func (a *Attempt) ObserveSandboxHeartbeat(expectedVersion uint64, observedAt, now time.Time) error {
	return a.observeHeartbeat(&a.sandboxHeartbeatAt, expectedVersion, observedAt, now)
}

// ObserveHarnessHeartbeat records a current Attempt's monotonic harness liveness observation.
func (a *Attempt) ObserveHarnessHeartbeat(expectedVersion uint64, observedAt, now time.Time) error {
	return a.observeHeartbeat(&a.harnessHeartbeatAt, expectedVersion, observedAt, now)
}

func (a *Attempt) observeHeartbeat(target **time.Time, expectedVersion uint64, observedAt, now time.Time) error {
	if err := a.validateMutation(expectedVersion, now); err != nil {
		return err
	}
	observedAt = observedAt.UTC()
	if !a.current || isTerminal(a.state) || observedAt.IsZero() || observedAt.Before(a.createdAt) || observedAt.After(now.UTC()) ||
		(*target != nil && observedAt.Before(**target)) {
		return ErrInvalidAttempt
	}
	copy := observedAt
	*target = &copy
	a.advance(now)
	return nil
}

// Transition applies one legal lifecycle edge using optimistic versioning.
func (a *Attempt) Transition(next State, expectedVersion uint64, result *TerminalResult, now time.Time) error {
	if err := a.validateMutation(expectedVersion, now); err != nil {
		return err
	}
	if _, err := ParseState(string(next)); err != nil {
		return ErrInvalidAttempt
	}
	if next == a.state {
		if isTerminal(next) && sameResult(a.terminalResult, result) {
			return nil
		}
		return ErrIllegalTransition
	}
	if !legal[a.state][next] {
		return ErrIllegalTransition
	}
	if isTerminal(next) != (result != nil) || (result != nil && !validResult(*result)) {
		return ErrInvalidAttempt
	}
	a.state = next
	a.advance(now)
	if result != nil {
		copy := *result
		a.terminalResult = &copy
		terminalAt := now.UTC()
		a.terminalAt = &terminalAt
		a.current = false
	}
	return nil
}

func (a *Attempt) validateMutation(expectedVersion uint64, now time.Time) error {
	if a == nil || now.IsZero() {
		return ErrInvalidAttempt
	}
	if expectedVersion != a.stateVersion {
		return ErrVersionConflict
	}
	if now.UTC().Before(a.updatedAt) {
		return ErrInvalidAttempt
	}
	return nil
}

func (a *Attempt) advance(now time.Time) { a.stateVersion++; a.updatedAt = now.UTC() }

var legal = map[State]map[State]bool{
	Pending:      {Acquiring: true, Failed: true, Interrupting: true},
	Acquiring:    {Starting: true, Failed: true, Interrupting: true},
	Starting:     {Running: true, Failed: true, Interrupting: true},
	Running:      {Succeeded: true, Failed: true, Interrupting: true},
	Interrupting: {Cancelled: true, TimedOut: true, Replaced: true, Failed: true},
}

func validID(id primitives.ID) bool { _, err := primitives.ParseID(string(id)); return err == nil }
func isTerminal(state State) bool {
	return state == Succeeded || state == Failed || state == Cancelled || state == TimedOut || state == Replaced
}
func validResult(result TerminalResult) bool {
	_, err := primitives.BoundedString(result.Reference, 1, 2048, 2048)
	return err == nil && digestPattern.MatchString(result.Digest)
}
func sameResult(left, right *TerminalResult) bool {
	return left != nil && right != nil && *left == *right
}

func (a *Attempt) TenantID() primitives.ID                 { return a.tenantID }
func (a *Attempt) ID() primitives.ID                       { return a.id }
func (a *Attempt) Binding() Binding                        { return a.binding }
func (a *Attempt) State() State                            { return a.state }
func (a *Attempt) StateVersion() uint64                    { return a.stateVersion }
func (a *Attempt) IsCurrent() bool                         { return a.current }
func (a *Attempt) SandboxReference() (primitives.ID, bool) { return optionalID(a.sandboxReference) }
func (a *Attempt) HarnessReference() (primitives.ID, bool) { return optionalID(a.harnessReference) }
func (a *Attempt) CreatedAt() time.Time                    { return a.createdAt }
func (a *Attempt) UpdatedAt() time.Time                    { return a.updatedAt }
func (a *Attempt) SandboxHeartbeatAt() (time.Time, bool)   { return optionalTime(a.sandboxHeartbeatAt) }
func (a *Attempt) HarnessHeartbeatAt() (time.Time, bool)   { return optionalTime(a.harnessHeartbeatAt) }
func (a *Attempt) TerminalAt() (time.Time, bool)           { return optionalTime(a.terminalAt) }
func (a *Attempt) TerminalResult() (TerminalResult, bool) {
	if a.terminalResult == nil {
		return TerminalResult{}, false
	}
	return *a.terminalResult, true
}
func optionalTime(value *time.Time) (time.Time, bool) {
	if value == nil {
		return time.Time{}, false
	}
	return *value, true
}
func optionalID(value primitives.ID) (primitives.ID, bool) { return value, value != "" }
