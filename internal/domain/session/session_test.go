package session

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var testTime = time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)

func TestSessionTransitionMatrix(t *testing.T) {
	legal := map[State]map[State]bool{
		Provisioning: {Ready: true, Degraded: true, Closing: true},
		Ready:        {Active: true, Suspended: true, Degraded: true, Closing: true},
		Active:       {Idle: true, Degraded: true, Closing: true},
		Idle:         {Active: true, Suspended: true, Degraded: true, Closing: true},
		Suspended:    {Ready: true, Idle: true, Degraded: true, Closing: true},
		Degraded:     {Ready: true, Closing: true},
		Closing:      {Closed: true},
	}
	for _, from := range states {
		for _, to := range states {
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				s := sessionInState(t, from)
				beforeVersion := s.StateVersion()
				err := s.Transition(to, beforeVersion, testTime.Add(24*time.Hour))
				if from == to && (from == Closing || from == Closed) {
					if err != nil || s.StateVersion() != beforeVersion {
						t.Fatalf("idempotent terminal transition: version=%d err=%v", s.StateVersion(), err)
					}
					return
				}
				wantLegal := legal[from][to]
				if wantLegal && err != nil {
					t.Fatalf("legal transition failed: %v", err)
				}
				if !wantLegal && !errors.Is(err, ErrIllegalTransition) {
					t.Fatalf("illegal transition error = %v", err)
				}
				if !wantLegal && s.State() != from {
					t.Fatalf("illegal transition mutated state to %s", s.State())
				}
			})
		}
	}
}

func TestTransitionVersionGenerationAndTimestamps(t *testing.T) {
	s := sessionInState(t, Ready)
	version := s.StateVersion()
	when := s.UpdatedAt().Add(time.Minute)
	if err := s.Transition(Active, version+1, when); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale version error = %v", err)
	}
	if s.State() != Ready || s.StateVersion() != version || s.ExecutionGeneration() != 0 {
		t.Fatal("version conflict mutated aggregate")
	}
	if err := s.Transition(Active, version, when); err != nil {
		t.Fatal(err)
	}
	if s.StateVersion() != version+1 || s.ExecutionGeneration() != 1 || !s.UpdatedAt().Equal(when) {
		t.Fatalf("state version=%d generation=%d updated=%v", s.StateVersion(), s.ExecutionGeneration(), s.UpdatedAt())
	}
}

func TestExecutionGenerationExhaustionFailsClosed(t *testing.T) {
	s := sessionInState(t, Ready)
	s.executionGeneration = math.MaxInt64
	beforeVersion := s.StateVersion()
	beforeUpdatedAt := s.UpdatedAt()

	err := s.Transition(Active, beforeVersion, beforeUpdatedAt.Add(time.Minute))
	if !errors.Is(err, ErrGenerationExhausted) {
		t.Fatalf("Transition() error = %v", err)
	}
	if s.State() != Ready || s.StateVersion() != beforeVersion ||
		s.ExecutionGeneration() != math.MaxInt64 || !s.UpdatedAt().Equal(beforeUpdatedAt) {
		t.Fatal("generation exhaustion mutated aggregate")
	}
}

func FuzzSessionTransitionSequences(f *testing.F) {
	f.Add([]byte{1, 2, 3, 2, 3})
	f.Add([]byte{1, 5, 1, 6, 7})
	f.Add([]byte{6, 7, 0, 2})

	f.Fuzz(func(t *testing.T, commands []byte) {
		s := sessionInState(t, Provisioning)
		for step, command := range commands {
			before := sessionTransitionSnapshotOf(s)
			next := states[int(command)%len(states)]
			now := s.UpdatedAt().Add(time.Duration(step+1) * time.Nanosecond)
			wantLegal := sessionTransitionAllowed(before.state, before.recoveryState, next)

			err := s.Transition(next, before.version, now)
			if !wantLegal {
				if !errors.Is(err, ErrIllegalTransition) {
					t.Fatalf("step %d: %s -> %s error = %v", step, before.state, next, err)
				}
				if got := sessionTransitionSnapshotOf(s); got != before {
					t.Fatalf("step %d: illegal transition mutated aggregate: before=%+v after=%+v", step, before, got)
				}
				continue
			}
			if err != nil {
				t.Fatalf("step %d: legal %s -> %s transition: %v", step, before.state, next, err)
			}

			idempotent := next == before.state
			wantVersion := before.version + 1
			if idempotent {
				wantVersion = before.version
			}
			if s.StateVersion() != wantVersion {
				t.Fatalf("step %d: state version = %d, want %d", step, s.StateVersion(), wantVersion)
			}
			wantGeneration := before.generation
			if (before.state == Ready || before.state == Idle) && next == Active {
				wantGeneration++
			}
			if s.ExecutionGeneration() != wantGeneration {
				t.Fatalf("step %d: execution generation = %d, want %d", step, s.ExecutionGeneration(), wantGeneration)
			}
			if before.state == Closed && s.State() != Closed {
				t.Fatalf("step %d: CLOSED regained authority as %s", step, s.State())
			}
		}
	})
}

type sessionTransitionSnapshot struct {
	state         State
	recoveryState State
	version       uint64
	generation    uint64
	updatedAt     time.Time
	closedAt      time.Time
	hasClosedAt   bool
}

func sessionTransitionSnapshotOf(s *Session) sessionTransitionSnapshot {
	closedAt, hasClosedAt := s.ClosedAt()
	return sessionTransitionSnapshot{
		state: s.State(), recoveryState: s.RecoveryState(), version: s.StateVersion(),
		generation: s.ExecutionGeneration(), updatedAt: s.UpdatedAt(), closedAt: closedAt, hasClosedAt: hasClosedAt,
	}
}

func sessionTransitionAllowed(from, recoveryState, to State) bool {
	if from == to {
		return from == Closing || from == Closed
	}
	if from != Closed && to == Closing {
		return true
	}
	if from == Degraded {
		return to == recoveryState
	}
	return map[State]map[State]bool{
		Provisioning: {Ready: true, Degraded: true},
		Ready:        {Active: true, Suspended: true, Degraded: true},
		Active:       {Idle: true, Degraded: true},
		Idle:         {Active: true, Suspended: true, Degraded: true},
		Suspended:    {Ready: true, Idle: true, Degraded: true},
		Closing:      {Closed: true},
	}[from][to]
}

func TestDegradedRecoveryReturnsOnlyToRecordedSafeState(t *testing.T) {
	for _, prior := range []State{Provisioning, Ready, Active, Idle, Suspended} {
		t.Run(string(prior), func(t *testing.T) {
			s := sessionInState(t, prior)
			if err := s.Transition(Degraded, s.StateVersion(), s.UpdatedAt().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			if s.RecoveryState() != prior {
				t.Fatalf("RecoveryState() = %s, want %s", s.RecoveryState(), prior)
			}
			for _, candidate := range []State{Provisioning, Ready, Active, Idle, Suspended} {
				copy := *s
				err := copy.Transition(candidate, copy.StateVersion(), copy.UpdatedAt().Add(time.Minute))
				if candidate == prior && err != nil {
					t.Fatalf("recover to %s: %v", candidate, err)
				}
				if candidate != prior && !errors.Is(err, ErrIllegalTransition) {
					t.Fatalf("recover to %s error = %v", candidate, err)
				}
			}
		})
	}
}

func TestBindingIsDefensivelyCopied(t *testing.T) {
	binding := validBinding()
	s, err := New(testID(1), testID(2), binding, testTime)
	if err != nil {
		t.Fatal(err)
	}
	binding.RuntimeSpec[1] = 'x'
	got := s.Binding()
	got.RuntimeProfileSnapshot[1] = 'x'
	if string(s.Binding().RuntimeSpec) != `{}` || string(s.Binding().RuntimeProfileSnapshot) != `{}` {
		t.Fatal("runtime binding was mutable through caller-owned memory")
	}
}

func TestNewRejectsInvalidBinding(t *testing.T) {
	binding := validBinding()
	binding.RuntimeSpecDigest = "sha256:" + strings.Repeat("z", 64)
	if _, err := New(testID(1), testID(2), binding, testTime); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("New() error = %v", err)
	}
}

func sessionInState(t *testing.T, state State) *Session {
	t.Helper()
	s, err := New(testID(1), testID(2), validBinding(), testTime)
	if err != nil {
		t.Fatal(err)
	}
	sequence := map[State][]State{
		Provisioning: nil,
		Ready:        {Ready},
		Active:       {Ready, Active},
		Idle:         {Ready, Active, Idle},
		Suspended:    {Ready, Suspended},
		Degraded:     {Ready, Degraded},
		Closing:      {Closing},
		Closed:       {Closing, Closed},
	}[state]
	for i, next := range sequence {
		if err := s.Transition(next, s.StateVersion(), testTime.Add(time.Duration(i+1)*time.Minute)); err != nil {
			t.Fatalf("prepare %s: %v", state, err)
		}
	}
	return s
}

func validBinding() RuntimeBinding {
	digest := "sha256:" + strings.Repeat("a", 64)
	return RuntimeBinding{
		AuthorityMode: "LOCAL", AuthorityNamespace: "operator", AgentID: "agent", AgentVersionID: "v1",
		RuntimeSpecSchemaVersion: "1", RuntimeSpec: []byte(`{}`), RuntimeSpecDigest: digest,
		RuntimeProfileSchemaVersion: "1", RuntimeProfileSnapshot: []byte(`{}`), RuntimeProfileDigest: digest,
	}
}

func testID(last byte) primitives.ID {
	value := "01890f3a-5b7c-7def-8000-00000000000" + string(rune('0'+last))
	id, err := primitives.ParseID(value)
	if err != nil {
		panic(err)
	}
	return id
}
