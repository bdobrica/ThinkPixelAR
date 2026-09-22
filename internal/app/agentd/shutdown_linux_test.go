package agentd

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"golang.org/x/sys/unix"
)

func TestShutdownGracefulAndEscalation(t *testing.T) {
	for _, mode := range []string{"shutdown", "ignore"} {
		t.Run(mode, func(t *testing.T) {
			p, id, root := controlFixture(t, mode)
			var calls atomic.Int32
			hook := func(ctx context.Context, got primitives.ID, reason InterruptReason) error {
				calls.Add(1)
				if got != id || reason != InterruptShutdown {
					t.Error("wrong shutdown binding")
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Error("unbounded close")
				}
				if _, err := p.Start(ctx); err != ErrProcessClosed {
					t.Error("admitted work during close")
				}
				return nil // Ack alone must not skip TERM/KILL.
			}
			if err := p.Shutdown(context.Background(), hook); err != nil {
				t.Fatal(err)
			}
			if err := p.Shutdown(context.Background(), hook); err != nil || calls.Load() != 1 {
				t.Fatal("shutdown not idempotent", err)
			}
			if _, err := p.Restart(context.Background(), id); err != ErrProcessClosed {
				t.Fatal("restart after shutdown", err)
			}
			s := p.Status()
			if !s.ExitObserved {
				t.Fatal("leader not reaped")
			}
			if mode == "ignore" && s.Signal != int(syscall.SIGKILL) {
				t.Fatal("missing kill escalation")
			}
			if mode == "shutdown" {
				waitFile(t, filepath.Join(root, "term-observed"))
				if s.ExitCode != 0 {
					t.Fatal("graceful exit failed")
				}
			}
		})
	}
}

func TestShutdownCancelsPendingControl(t *testing.T) {
	p, id, _ := controlFixture(t, "ignore")
	p.config.StartTimeoutMS = 20
	p.config.StopGraceMS = 20
	p.config.KillWaitMS = 200
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	c, err := NewControls(p, 16, map[string]SignalHandler{"wait": func(ctx context.Context, _ primitives.ID, _ []byte) error { close(entered); <-release; return nil }}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- c.Signal(context.Background(), id, "wait", nil) }()
	<-entered
	if err := p.Shutdown(context.Background(), nil); err != ErrProcess {
		t.Fatal("unresolved callback claimed clean", err)
	}
	if err := <-result; err != ErrProcessDeadline {
		t.Fatal("operation not cancelled", err)
	}
	select {
	case <-p.current.done:
	case <-time.After(time.Second):
		t.Fatal("pending callback kept child alive")
	}
	if _, err := p.Start(context.Background()); err != ErrProcessClosed {
		t.Fatal("late work admitted")
	}
}

func TestShutdownUnresponsiveHookAndCancelledCaller(t *testing.T) {
	p, _, _ := controlFixture(t, "ignore")
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Shutdown(ctx, func(context.Context, primitives.ID, InterruptReason) error { close(entered); <-release; return nil }); err != ErrProcessDeadline {
		t.Fatal(err)
	}
	<-entered
	if err := p.Shutdown(context.Background(), nil); err != ErrProcess {
		t.Fatal("late hook claimed clean", err)
	}
	select {
	case <-p.current.done:
	default:
		t.Fatal("cancelled caller prevented cleanup")
	}
}

// Real SIGTERM delivery exercises the same NotifyContext -> RunProcesses path
// used by main, without weakening the production bootstrap/launch admission.
func TestSupervisorSIGTERM(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cmd := exec.Command(exe, "-test.run=^TestSupervisorSignalChild$", "supervisor-signal-fixture", root)
	cmd.Env = []string{}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitFile(t, filepath.Join(root, "supervisor-ready"))
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("supervisor did not exit cleanly")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor shutdown hung")
	}
	waitFile(t, filepath.Join(root, "term-observed"))
}
func TestSupervisorSignalChild(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "supervisor-signal-fixture" {
		return
	}
	root := os.Args[len(os.Args)-1]
	exe, err := os.Executable()
	if err != nil {
		os.Exit(90)
	}
	p := &Processes{config: HarnessConfig{Argv: []string{exe, "-test.run=^TestProcessChild$", "process-fixture", "shutdown", root}, WorkingDirectory: root, StartTimeoutMS: 1000, StopGraceMS: 100, KillWaitMS: 1000}}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	if _, err := p.Start(ctx); err != nil {
		os.Exit(91)
	}
	waitFile(t, filepath.Join(root, "shutdown-ready"))
	if os.WriteFile(filepath.Join(root, "supervisor-ready"), nil, 0600) != nil {
		os.Exit(92)
	}
	if RunProcesses(ctx, p, nil, slog.New(slog.NewJSONHandler(io.Discard, nil))) != nil {
		os.Exit(93)
	}
}

func TestOrphanReaping(t *testing.T) {
	// Subreaper applies only in this isolated helper process, never the test runner.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestOrphanReaperChild$", "orphan-reaper-fixture")
	cmd.Env = []string{}
	if err := cmd.Run(); err != nil {
		t.Fatal("orphan reaping failed")
	}
}
func TestOrphanReaperChild(t *testing.T) {
	if os.Args[len(os.Args)-1] != "orphan-reaper-fixture" {
		return
	}
	if unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) != nil {
		os.Exit(94)
	}
	p, _, root := controlFixture(t, "parent")
	waitFile(t, filepath.Join(root, "descendant"))
	if err := p.Shutdown(context.Background(), nil); err != nil {
		os.Exit(95)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if reapOrphans(ctx) != nil {
		os.Exit(96)
	}
	if _, err := unix.Wait4(-1, nil, unix.WNOHANG, nil); err != unix.ECHILD {
		os.Exit(97)
	}
}

func TestShutdownRacingLaunch(t *testing.T) {
	for range 10 {
		p, _ := processFixture(t, "ignore")
		started := make(chan error, 1)
		go func() { _, err := p.Start(context.Background()); started <- err }()
		if err := p.Shutdown(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		err := <-started
		if err != nil && err != ErrProcessClosed && err != ErrProcessDeadline && err != ErrProcessBusy {
			t.Fatal(err)
		}
		if c := p.active.Load(); c != nil {
			select {
			case <-c.done:
			default:
				t.Fatal("late launch escaped shutdown")
			}
		}
	}
}

func TestShutdownHookFailureStillStops(t *testing.T) {
	for _, panics := range []bool{false, true} {
		p, _, _ := controlFixture(t, "ignore")
		err := p.Shutdown(context.Background(), func(context.Context, primitives.ID, InterruptReason) error {
			if panics {
				panic("restricted-canary")
			}
			return ErrControl
		})
		if err != nil {
			t.Fatal("fallback shutdown failed", err)
		}
		if p.Status().Signal != int(syscall.SIGKILL) {
			t.Fatal("missing fallback")
		}
	}
}
