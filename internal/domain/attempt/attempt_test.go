package attempt

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var baseTime = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

func TestTransitionMatrix(t *testing.T) {
	for _, from := range states {
		for _, to := range states {
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				a := attemptInState(t, from)
				version := a.StateVersion()
				var result *TerminalResult
				if isTerminal(to) {
					value := validResultValue()
					result = &value
				}
				err := a.Transition(to, version, result, a.UpdatedAt().Add(time.Minute))
				if from == to && isTerminal(from) {
					if err != nil || a.StateVersion() != version {
						t.Fatalf("idempotent replay: version=%d err=%v", a.StateVersion(), err)
					}
					return
				}
				if legal[from][to] && err != nil {
					t.Fatalf("legal transition failed: %v", err)
				}
				if !legal[from][to] && !errors.Is(err, ErrIllegalTransition) {
					t.Fatalf("illegal transition error = %v", err)
				}
				if !legal[from][to] && a.State() != from {
					t.Fatalf("illegal transition mutated state to %s", a.State())
				}
			})
		}
	}
}

func TestTerminalResultAndCurrentDesignation(t *testing.T) {
	a := attemptInState(t, Running)
	if err := a.Transition(Succeeded, a.StateVersion(), nil, a.UpdatedAt().Add(time.Minute)); !errors.Is(err, ErrInvalidAttempt) {
		t.Fatalf("missing result error = %v", err)
	}
	result := validResultValue()
	when := a.UpdatedAt().Add(2 * time.Minute)
	if err := a.Transition(Succeeded, a.StateVersion(), &result, when); err != nil {
		t.Fatal(err)
	}
	got, ok := a.TerminalResult()
	terminalAt, atOK := a.TerminalAt()
	if !ok || !atOK || got != result || !terminalAt.Equal(when) || a.IsCurrent() {
		t.Fatalf("terminal result=%+v current=%v", got, a.IsCurrent())
	}
	conflict := result
	conflict.Digest = digest("b")
	if err := a.Transition(Succeeded, a.StateVersion(), &conflict, when.Add(time.Minute)); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("conflicting replay error = %v", err)
	}
}

func TestOpaqueBindingsAreWriteOnce(t *testing.T) {
	a := newAttempt(t)
	if err := a.BindSandbox(testID(4), 0, baseTime.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := a.BindSandbox(testID(4), 1, baseTime.Add(2*time.Minute)); err != nil || a.StateVersion() != 1 {
		t.Fatalf("idempotent bind version=%d err=%v", a.StateVersion(), err)
	}
	if err := a.BindSandbox(testID(5), 1, baseTime.Add(2*time.Minute)); !errors.Is(err, ErrInvalidAttempt) {
		t.Fatalf("replacement bind error=%v", err)
	}
	if err := a.BindHarness(testID(6), 1, baseTime.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	sandbox, sandboxOK := a.SandboxReference()
	harness, harnessOK := a.HarnessReference()
	if !sandboxOK || !harnessOK || sandbox != testID(4) || harness != testID(6) {
		t.Fatal("binding reference not retained")
	}
}

func TestHeartbeatsAreCurrentBoundedAndMonotonic(t *testing.T) {
	a := newAttempt(t)
	observed := baseTime.Add(time.Minute)
	if err := a.ObserveSandboxHeartbeat(0, observed, observed); err != nil {
		t.Fatal(err)
	}
	if err := a.ObserveHarnessHeartbeat(1, observed, observed.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, ok := a.SandboxHeartbeatAt(); !ok || !got.Equal(observed) {
		t.Fatalf("sandbox heartbeat=%v,%v", got, ok)
	}
	if err := a.ObserveSandboxHeartbeat(2, baseTime, observed.Add(2*time.Minute)); !errors.Is(err, ErrInvalidAttempt) {
		t.Fatalf("regressing heartbeat error=%v", err)
	}
	a = attemptInState(t, Failed)
	if err := a.ObserveHarnessHeartbeat(a.StateVersion(), a.UpdatedAt(), a.UpdatedAt()); !errors.Is(err, ErrInvalidAttempt) {
		t.Fatalf("terminal heartbeat error=%v", err)
	}
}

func TestVersionConflictAndCreationValidation(t *testing.T) {
	a := newAttempt(t)
	if err := a.Transition(Acquiring, 1, nil, baseTime.Add(time.Minute)); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("version error=%v", err)
	}
	binding := validBinding()
	binding.Number = 0
	if _, err := New(testID(1), testID(2), binding, baseTime); !errors.Is(err, ErrInvalidAttempt) {
		t.Fatalf("ordinal error=%v", err)
	}
}

func attemptInState(t *testing.T, state State) *Attempt {
	t.Helper()
	a := newAttempt(t)
	paths := map[State][]State{
		Pending: nil, Acquiring: {Acquiring}, Starting: {Acquiring, Starting}, Running: {Acquiring, Starting, Running},
		Interrupting: {Interrupting}, Succeeded: {Acquiring, Starting, Running, Succeeded}, Failed: {Failed},
		Cancelled: {Interrupting, Cancelled}, TimedOut: {Interrupting, TimedOut}, Replaced: {Interrupting, Replaced},
	}[state]
	for _, next := range paths {
		var result *TerminalResult
		if isTerminal(next) {
			value := validResultValue()
			result = &value
		}
		if err := a.Transition(next, a.StateVersion(), result, a.UpdatedAt().Add(time.Minute)); err != nil {
			t.Fatalf("prepare %s: %v", state, err)
		}
	}
	return a
}

func newAttempt(t *testing.T) *Attempt {
	t.Helper()
	a, err := New(testID(1), testID(2), validBinding(), baseTime)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func validBinding() Binding {
	return Binding{ExecutionID: testID(3), ExecutionGeneration: 1, Number: 1}
}
func validResultValue() TerminalResult {
	return TerminalResult{Reference: "attempt-result:1", Digest: digest("a")}
}
func digest(value string) string { return "sha256:" + strings.Repeat(value, 64) }
func testID(last byte) primitives.ID {
	id, err := primitives.ParseID("01890f3a-5b7c-7def-8000-00000000000" + string(rune('0'+last)))
	if err != nil {
		panic(err)
	}
	return id
}
