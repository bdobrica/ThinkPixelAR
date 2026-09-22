package agentd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
)

func TestProcessStatusDuringStopAndRestart(t *testing.T) {
	p, root := processFixture(t, "ignore")
	p.config.StopGraceMS = 200
	if p.Status().State != agentdv1.Heartbeat_ABSENT {
		t.Fatal("new process not absent")
	}
	id, err := p.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(root, "ignore-ready"))
	if s := p.Status(); s.State != agentdv1.Heartbeat_RUNNING || s.ProcessID != id || s.ExitObserved {
		t.Fatal("invalid running status")
	}
	done := make(chan error, 1)
	go func() { done <- p.Stop(context.Background(), id) }()
	deadline := time.Now().Add(time.Second)
	for p.Status().State != agentdv1.Heartbeat_STOPPING {
		if time.Now().After(deadline) {
			t.Fatal("stop status blocked")
		}
		time.Sleep(time.Millisecond)
	}
	l := protocol.HardLimits()
	l.HeartbeatIntervalMs = 10
	l.LivenessWindowMs = 30
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := RunHeartbeats(ctx, l, p.Heartbeat, func(_ context.Context, h *agentdv1.Heartbeat) error {
		if h.ProcessState != agentdv1.Heartbeat_STOPPING {
			t.Error("wrong heartbeat during stop")
		}
		cancel()
		return nil
	}); err != context.Canceled {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s := p.Status()
	if s.State != agentdv1.Heartbeat_EXITED || !s.ExitObserved || s.Signal != 9 || s.Failure != ProcessOK {
		t.Fatal("missing termination observation", s)
	}
	s.State = agentdv1.Heartbeat_RUNNING
	if p.Status().State != agentdv1.Heartbeat_EXITED {
		t.Fatal("status aliased")
	}
	next, err := p.Restart(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if s := p.Status(); s.ProcessID != next || next == id || s.State != agentdv1.Heartbeat_RUNNING || s.ExitObserved {
		t.Fatal("stale restart status")
	}
}
func TestProcessFailureStatus(t *testing.T) {
	p, _ := processFixture(t, "ignore")
	p.config.Argv = []string{"/missing/restricted-canary"}
	if _, err := p.Start(context.Background()); err != ErrProcess {
		t.Fatal(err)
	}
	if s := p.Status(); s.State != agentdv1.Heartbeat_FAILED || s.Failure != ProcessLaunchFailed || s.ExitObserved {
		t.Fatal("launch status missing")
	}
	q, _ := processFixture(t, "flood")
	l := protocol.HardLimits()
	l.BufferedEvents = 1
	l.LivenessWindowMs = 20
	q.captureLimits = l
	if _, err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-q.current.done:
	case <-time.After(3 * time.Second):
		t.Fatal("output failure not reaped")
	}
	if s := q.Status(); s.State != agentdv1.Heartbeat_FAILED || s.Failure != ProcessOutputFailed || !s.ExitObserved {
		t.Fatal("capture failure not visible")
	}
}
func TestProcessNaturalExitStatus(t *testing.T) {
	p, root := processFixture(t, "parent")
	if _, err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(root, "descendant-ready"))
	if err := os.WriteFile(filepath.Join(root, "exit"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.current.done:
	case <-time.After(3 * time.Second):
		t.Fatal("child not reaped")
	}
	s := p.Status()
	if s.State != agentdv1.Heartbeat_EXITED || !s.ExitObserved || s.ExitCode != 0 || s.Signal != 0 {
		t.Fatal("natural exit status", s)
	}
	// Late stop notification cannot regress a terminal observation.
	p.observation.publish(ProcessStatus{ProcessID: s.ProcessID, State: agentdv1.Heartbeat_STOPPING})
	if p.Status() != s {
		t.Fatal("terminal observation regressed")
	}
}

func TestProcessUnexpectedExitStatus(t *testing.T) {
	for _, mode := range []string{"exit-error", "ignore"} {
		t.Run(mode, func(t *testing.T) {
			p, root := processFixture(t, mode)
			if _, err := p.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			if mode == "ignore" {
				waitFile(t, filepath.Join(root, "ignore-ready"))
				if err := p.current.cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-p.current.done:
			case <-time.After(3 * time.Second):
				t.Fatal("exit not observed")
			}
			s := p.Status()
			if s.State != agentdv1.Heartbeat_FAILED || s.Failure != ProcessExitFailed || !s.ExitObserved {
				t.Fatal("unexpected exit not classified", s)
			}
			if mode == "exit-error" && (s.ExitCode != 7 || s.Signal != 0) {
				t.Fatal("exit code lost")
			}
			if mode == "ignore" && s.Signal != 9 {
				t.Fatal("signal lost")
			}
		})
	}
}
