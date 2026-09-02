package session

import (
	"errors"
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
