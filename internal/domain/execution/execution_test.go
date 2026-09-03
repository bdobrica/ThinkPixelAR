package execution

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
				e := executionInState(t, from)
				version := e.StateVersion()
				var result *TerminalResult
				if isTerminal(to) {
					value := validResultValue()
					result = &value
				}
				err := e.Transition(to, version, result, e.UpdatedAt().Add(time.Minute))
				if from == to && isTerminal(from) {
					if err != nil || e.StateVersion() != version {
						t.Fatalf("idempotent terminal replay: version=%d err=%v", e.StateVersion(), err)
					}
					return
				}
				if legal[from][to] && err != nil {
					t.Fatalf("legal transition failed: %v", err)
				}
				if !legal[from][to] && !errors.Is(err, ErrIllegalTransition) {
					t.Fatalf("illegal transition error = %v", err)
				}
				if !legal[from][to] && e.State() != from {
					t.Fatalf("illegal transition mutated state to %s", e.State())
				}
			})
		}
	}
}

func TestVersionConflictDoesNotMutate(t *testing.T) {
	e := newExecution(t)
	if err := e.Transition(Materializing, 1, nil, baseTime.Add(time.Minute)); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("Transition() error = %v", err)
	}
	if e.State() != Queued || e.StateVersion() != 0 || !e.UpdatedAt().Equal(baseTime) {
		t.Fatal("version conflict mutated execution")
	}
}

func FuzzExecutionTransitionSequences(f *testing.F) {
	f.Add([]byte{1, 2, 5})
	f.Add([]byte{3, 7, 2})
	f.Add([]byte{4, 8, 0})

	f.Fuzz(func(t *testing.T, commands []byte) {
		e := newExecution(t)
		for step, command := range commands {
			before := executionTransitionSnapshotOf(e)
			next := states[int(command)%len(states)]
			now := e.UpdatedAt().Add(time.Duration(step+1) * time.Nanosecond)
			wantLegal := executionTransitionAllowed(before.state, next)
			var result *TerminalResult
			if isTerminal(next) {
				value := validResultValue()
				result = &value
			}

			err := e.Transition(next, before.version, result, now)
			if !wantLegal {
				if !errors.Is(err, ErrIllegalTransition) {
					t.Fatalf("step %d: %s -> %s error = %v", step, before.state, next, err)
				}
				if got := executionTransitionSnapshotOf(e); got != before {
					t.Fatalf("step %d: illegal transition mutated aggregate: before=%+v after=%+v", step, before, got)
				}
				continue
			}
			if err != nil {
				t.Fatalf("step %d: legal %s -> %s transition: %v", step, before.state, next, err)
			}
			wantVersion := before.version + 1
			if next == before.state {
				wantVersion = before.version
			}
			if e.StateVersion() != wantVersion {
				t.Fatalf("step %d: state version = %d, want %d", step, e.StateVersion(), wantVersion)
			}
			if isTerminal(before.state) && e.State() != before.state {
				t.Fatalf("step %d: terminal execution changed from %s to %s", step, before.state, e.State())
			}
		}
	})
}

type executionTransitionSnapshot struct {
	state       State
	version     uint64
	updatedAt   time.Time
	result      TerminalResult
	hasResult   bool
	terminalAt  time.Time
	hasTerminal bool
}

func executionTransitionSnapshotOf(e *Execution) executionTransitionSnapshot {
	result, hasResult := e.TerminalResult()
	terminalAt, hasTerminal := e.TerminalAt()
	return executionTransitionSnapshot{
		state: e.State(), version: e.StateVersion(), updatedAt: e.UpdatedAt(),
		result: result, hasResult: hasResult, terminalAt: terminalAt, hasTerminal: hasTerminal,
	}
}

func executionTransitionAllowed(from, to State) bool {
	if from == to {
		return isTerminal(from)
	}
	return map[State]map[State]bool{
		Queued:        {Materializing: true, Cancelling: true, TimingOut: true, Failed: true},
		Materializing: {Running: true, Cancelling: true, TimingOut: true, Failed: true},
		Running:       {Materializing: true, Cancelling: true, TimingOut: true, Succeeded: true, Failed: true},
		Cancelling:    {Cancelled: true, Failed: true},
		TimingOut:     {TimedOut: true, Failed: true},
	}[from][to]
}

func TestTerminalResultRequiredAndImmutable(t *testing.T) {
	e := executionInState(t, Running)
	if err := e.Transition(Succeeded, e.StateVersion(), nil, e.UpdatedAt().Add(time.Minute)); !errors.Is(err, ErrInvalidExecution) {
		t.Fatalf("missing result error = %v", err)
	}
	result := validResultValue()
	when := e.UpdatedAt().Add(2 * time.Minute)
	if err := e.Transition(Succeeded, e.StateVersion(), &result, when); err != nil {
		t.Fatal(err)
	}
	got, ok := e.TerminalResult()
	terminalAt, atOK := e.TerminalAt()
	if !ok || !atOK || got != result || !terminalAt.Equal(when) {
		t.Fatalf("terminal result=%+v,%v terminalAt=%v,%v", got, ok, terminalAt, atOK)
	}
	conflict := result
	conflict.Digest = "sha256:" + strings.Repeat("b", 64)
	if err := e.Transition(Succeeded, e.StateVersion(), &conflict, when.Add(time.Minute)); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("conflicting replay error = %v", err)
	}
}

func TestNewValidatesImmutableBindingAndDeadline(t *testing.T) {
	binding := validBindingFixture()
	binding.SessionGeneration = 0
	if _, err := New(testID(1), testID(2), binding, baseTime.Add(time.Hour), baseTime); !errors.Is(err, ErrInvalidExecution) {
		t.Fatalf("zero generation error = %v", err)
	}
	binding = validBindingFixture()
	if _, err := New(testID(1), testID(2), binding, baseTime, baseTime); !errors.Is(err, ErrInvalidExecution) {
		t.Fatalf("elapsed deadline error = %v", err)
	}
}

func TestBindingEvidenceIsDefensivelyCopied(t *testing.T) {
	binding := validBindingFixture()
	e, err := New(testID(1), testID(2), binding, baseTime.Add(time.Hour), baseTime)
	if err != nil {
		t.Fatal(err)
	}
	binding.AgentEvidence[1] = 'x'
	got := e.Binding()
	got.AgentEvidence[1] = 'y'
	if string(e.Binding().AgentEvidence) != `{}` {
		t.Fatal("agent evidence was mutable through caller-owned memory")
	}
}

func executionInState(t *testing.T, state State) *Execution {
	t.Helper()
	e := newExecution(t)
	paths := map[State][]State{
		Queued: nil, Materializing: {Materializing}, Running: {Materializing, Running},
		Cancelling: {Cancelling}, TimingOut: {TimingOut},
		Succeeded: {Materializing, Running, Succeeded}, Failed: {Failed},
		Cancelled: {Cancelling, Cancelled}, TimedOut: {TimingOut, TimedOut},
	}[state]
	for _, next := range paths {
		var result *TerminalResult
		if isTerminal(next) {
			value := validResultValue()
			result = &value
		}
		if err := e.Transition(next, e.StateVersion(), result, e.UpdatedAt().Add(time.Minute)); err != nil {
			t.Fatalf("prepare %s: %v", state, err)
		}
	}
	return e
}

func newExecution(t *testing.T) *Execution {
	t.Helper()
	e, err := New(testID(1), testID(2), validBindingFixture(), baseTime.Add(time.Hour), baseTime)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func validBindingFixture() Binding {
	digest := "sha256:" + strings.Repeat("a", 64)
	return Binding{
		SessionID: testID(3), SessionGeneration: 1, AuthorityMode: "LOCAL",
		AuthorityNamespace: "thinkpixelar/local", AuthorityReference: "grant-1",
		GrantDigest: digest, AgentID: "agent", AgentVersionID: "v1",
		AgentEvidence: []byte(`{}`), AgentEvidenceDigest: digest,
	}
}

func validResultValue() TerminalResult {
	return TerminalResult{Reference: "result:1", Digest: "sha256:" + strings.Repeat("a", 64)}
}

func testID(last byte) primitives.ID {
	id, err := primitives.ParseID("01890f3a-5b7c-7def-8000-00000000000" + string(rune('0'+last)))
	if err != nil {
		panic(err)
	}
	return id
}
