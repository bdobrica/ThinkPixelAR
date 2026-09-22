package attempt

import (
	"reflect"
	"testing"
	"time"
)

func TestLivenessObservationCannotAdvanceAttemptLifecycle(t *testing.T) {
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			a := attemptInState(t, state)
			before := *a
			now := a.UpdatedAt().Add(time.Second)
			for _, observe := range []func(uint64, time.Time, time.Time) error{a.ObserveSandboxHeartbeat, a.ObserveHarnessHeartbeat} {
				err := observe(a.StateVersion(), now, now)
				if isTerminal(state) {
					if err == nil || !reflect.DeepEqual(before, *a) {
						t.Fatal("liveness resurrected terminal Attempt")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if a.State() != before.state || a.IsCurrent() != before.current || a.terminalResult != nil {
						t.Fatal("liveness changed lifecycle")
					}
				}
			}
		})
	}
}

func TestInvalidLivenessLeavesAttemptUnchanged(t *testing.T) {
	for _, mode := range []string{"future", "before-creation", "regression", "stale-version"} {
		t.Run(mode, func(t *testing.T) {
			a := attemptInState(t, Running)
			now := a.UpdatedAt().Add(time.Minute)
			if err := a.ObserveHarnessHeartbeat(a.StateVersion(), now, now); err != nil {
				t.Fatal(err)
			}
			before := *a
			observed := now
			version := a.StateVersion()
			switch mode {
			case "future":
				observed = now.Add(time.Second)
			case "before-creation":
				observed = a.CreatedAt().Add(-time.Second)
			case "regression":
				observed = now.Add(-time.Second)
			case "stale-version":
				version--
			}
			if err := a.ObserveHarnessHeartbeat(version, observed, now); err == nil || !reflect.DeepEqual(before, *a) {
				t.Fatal("invalid liveness changed aggregate")
			}
		})
	}
}
