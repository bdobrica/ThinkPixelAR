package agentd

import (
	"sync/atomic"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type ProcessFailure uint8

const (
	ProcessOK ProcessFailure = iota
	ProcessLaunchFailed
	ProcessExitFailed
	ProcessCleanupFailed
	ProcessOutputFailed
)

// ProcessStatus is a local observation, never AR lifecycle truth or readiness.
// ExitCode and Signal are meaningful only when ExitObserved is true. No PID,
// paths, argv, environment, child output or OS error strings are exposed.
type ProcessStatus struct {
	ProcessID     primitives.ID
	State         agentdv1.Heartbeat_ProcessState
	ExitObserved  bool
	ExitCode      int
	Signal        int
	Failure       ProcessFailure
	ProtocolReady bool // Local registered-handshake observation, never Run authority.
}
type processObservation struct{ value atomic.Pointer[ProcessStatus] }

func (o *processObservation) status() ProcessStatus {
	if s := o.value.Load(); s != nil {
		return *s
	}
	return ProcessStatus{State: agentdv1.Heartbeat_ABSENT}
}
func (o *processObservation) publish(s ProcessStatus) {
	for {
		old := o.value.Load()
		// A handshake may finish while the child is already being reaped.
		if old != nil && old.ProcessID == s.ProcessID && old.State == agentdv1.Heartbeat_STOPPING && s.State == agentdv1.Heartbeat_RUNNING {
			return
		}
		// A concurrent stop/drain notification cannot overwrite terminal evidence.
		if old != nil && old.ProcessID == s.ProcessID && (old.State == agentdv1.Heartbeat_EXITED || old.State == agentdv1.Heartbeat_FAILED) && (s.State == agentdv1.Heartbeat_STOPPING || s.State == agentdv1.Heartbeat_RUNNING) {
			return
		}
		if o.value.CompareAndSwap(old, &s) {
			return
		}
	}
}
