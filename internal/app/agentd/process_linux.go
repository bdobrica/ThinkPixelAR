package agentd

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/harness/codex"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
)

var (
	ErrProcess         = errors.New("harness process operation failed")
	ErrProcessBusy     = errors.New("harness process operation busy")
	ErrProcessStale    = errors.New("harness process identity mismatch")
	ErrProcessDeadline = errors.New("harness process deadline exceeded")
)

// Processes controls one local child group. IDs are process observations, not AR
// HarnessHandles or authority. Call only after authenticated command admission.
// Generic Start means OS launch. The registered Codex path additionally requires
// its initialization handshake; neither result establishes AR Session readiness.
type Processes struct {
	gate           sync.Mutex
	lifeOnce       sync.Once
	lifetime       context.Context
	cancelLifetime context.CancelFunc
	closing        atomic.Bool
	active         atomic.Pointer[child]
	shutdownOnce   sync.Once
	shutdownDone   chan struct{}
	shutdownErr    error
	config         HarnessConfig
	codex          bool
	createThread   bool
	commandBytes   uint32
	current        *child
	captureLimits  *agentdv1.Limits
	sanitizer      OutputSanitizer
	observation    processObservation
}

type child struct {
	owner          *Processes
	stopRequested  atomic.Bool
	id             primitives.ID
	cmd            *exec.Cmd
	mu             sync.Mutex // Serialize group signals with final reaping (and PID reuse).
	reaped         bool
	done           chan struct{}
	err            error // Published by closing done.
	capture        *Capture
	stdout, stderr *captureWriter
	codex          *codex.Client
	codexHome      string
	threadID       string // Accessed only under the process operation gate.
}

// Status remains available while launch, stop or output draining holds the operation gate.
func (p *Processes) Status() ProcessStatus { return p.observation.status() }

// Heartbeat returns process state only. The admitted dispatcher supplies its own
// accepted/produced sequence counters and active operation before transmission.
func (p *Processes) Heartbeat() *agentdv1.Heartbeat {
	return &agentdv1.Heartbeat{ProcessState: p.Status().State}
}

func NewProcesses(c Config) (*Processes, error) {
	if c.Validate() != nil {
		return nil, ErrConfig
	}
	h := c.Harness
	h.Argv = slices.Clone(h.Argv)
	isCodex := c.AdapterKind == codex.Kind
	if isCodex && !slices.Equal(h.Argv, codex.Command()) {
		return nil, ErrConfig
	}
	createThread := slices.Contains(c.Capabilities, control.ThreadCapability)
	if createThread && (!isCodex || !slices.Contains(c.RequiredCapabilities, control.ThreadCapability)) {
		return nil, ErrConfig
	}
	return &Processes{config: h, codex: isCodex, createThread: createThread, commandBytes: c.Limits.CommandBytes}, nil
}

// NewProcessesWithCapture opts into bounded, redacted capture. A nil sanitizer
// suppresses content; registered adapters may supply a bounded schema sanitizer.
func NewProcessesWithCapture(c Config, s OutputSanitizer) (*Processes, error) {
	p, err := NewProcesses(c)
	if err != nil {
		return nil, err
	}
	p.captureLimits = proto.Clone(c.Limits).(*agentdv1.Limits)
	p.sanitizer = s
	return p, nil
}

// Output returns the current process's ephemeral single-consumer stream.
func (p *Processes) Output(id primitives.ID) (*Capture, error) {
	if !p.gate.TryLock() {
		return nil, ErrProcessBusy
	}
	defer p.gate.Unlock()
	if p.current == nil || id != p.current.id || p.current.capture == nil {
		return nil, ErrProcessStale
	}
	return p.current.capture, nil
}

// operation has no queue. A timed-out OS operation keeps the gate until cleanup
// completes; another command must never overlap an unresolved launch or stop.
func (p *Processes) operation(ctx context.Context, budget time.Duration, f func(context.Context) (primitives.ID, error)) (primitives.ID, error) {
	p.initLifetime()
	if p.closing.Load() {
		return "", ErrProcessClosed
	}
	if ctx.Err() != nil {
		return "", ErrProcessDeadline
	}
	if !p.gate.TryLock() {
		return "", ErrProcessBusy
	}
	if p.closing.Load() {
		p.gate.Unlock()
		return "", ErrProcessClosed
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	unwatch := context.AfterFunc(p.lifetime, cancel)
	defer unwatch()
	defer cancel()
	type result struct {
		id  primitives.ID
		err error
	}
	out := make(chan result)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer p.gate.Unlock()
		id, err := f(ctx)
		cleanup := func() {
			if id != "" && err == nil {
				_ = p.stop(context.Background(), id, true)
			}
		}
		if ctx.Err() != nil {
			cleanup()
			return
		}
		select {
		case out <- result{id, err}:
		case <-ctx.Done():
			// Successful process work whose result was not delivered must be stopped.
			cleanup()
		}
	}()
	select {
	case r := <-out:
		<-finished // A completed call releases the gate before its caller continues.
		return r.id, r.err
	case <-ctx.Done():
		return "", ErrProcessDeadline
	}
}

func (p *Processes) Start(ctx context.Context) (primitives.ID, error) {
	return p.operation(ctx, time.Duration(p.config.StartTimeoutMS)*time.Millisecond, p.start)
}
func (p *Processes) Stop(ctx context.Context, id primitives.ID) error {
	_, err := p.operation(ctx, p.stopBudget(), func(ctx context.Context) (primitives.ID, error) { return "", p.stop(ctx, id, false) })
	return err
}
func (p *Processes) Restart(ctx context.Context, id primitives.ID) (primitives.ID, error) {
	// A new thread would silently break Session continuity. Resume is CDX-005.
	if p.createThread {
		return "", ErrControl
	}
	return p.operation(ctx, p.stopBudget()+time.Duration(p.config.StartTimeoutMS)*time.Millisecond, func(ctx context.Context) (primitives.ID, error) {
		if err := p.stop(ctx, id, false); err != nil {
			return "", err
		}
		launch, cancel := context.WithTimeout(ctx, time.Duration(p.config.StartTimeoutMS)*time.Millisecond)
		defer cancel()
		return p.start(launch)
	})
}
func (p *Processes) stopBudget() time.Duration {
	return time.Duration(p.config.StopGraceMS+p.config.KillWaitMS) * time.Millisecond
}
func (p *Processes) start(ctx context.Context) (primitives.ID, error) {
	if p.closing.Load() {
		return "", ErrProcessClosed
	}
	if ctx.Err() != nil {
		return "", ErrProcessDeadline
	}
	if p.current != nil {
		select {
		case <-p.current.done:
			if p.current.err != nil {
				return "", ErrProcess
			}
		default:
			return "", ErrProcessBusy
		}
	}
	id, err := primitives.NewID(time.Now())
	if err != nil {
		return "", ErrProcess
	}
	cmd := exec.Command(p.config.Argv[0], p.config.Argv[1:]...)
	cmd.Dir = p.config.WorkingDirectory
	cmd.Env = []string{} // Never inherit agentd's environment or credentials.
	// No shell, extra descriptors, terminal or output logging.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c := &child{owner: p, id: id, cmd: cmd, done: make(chan struct{})}
	if p.captureLimits != nil {
		c.capture, err = newCapture(id, p.captureLimits, p.sanitizer)
		if err != nil {
			return "", err
		}
		c.stdout = &captureWriter{capture: c.capture, source: OutputStdout}
		c.stderr = &captureWriter{capture: c.capture, source: OutputStderr}
		cmd.Stdout = c.stdout
		cmd.Stderr = c.stderr
		cmd.WaitDelay = time.Duration(p.config.KillWaitMS) * time.Millisecond
	}
	closeChildPipes := func() {}
	if p.codex {
		closeChildPipes, err = c.prepareCodex()
		if err != nil {
			if c.capture != nil {
				c.capture.Close()
			}
			return "", ErrProcess
		}
	}
	p.observation.publish(ProcessStatus{ProcessID: id, State: agentdv1.Heartbeat_STARTING})
	if cmd.Start() != nil {
		closeChildPipes()
		_ = c.closeCodex()
		p.observation.publish(ProcessStatus{ProcessID: id, State: agentdv1.Heartbeat_FAILED, Failure: ProcessLaunchFailed})
		if c.capture != nil {
			c.capture.Close()
		}
		return "", ErrProcess
	}
	closeChildPipes()
	p.current = c
	p.active.Store(c)
	go c.reap()
	if c.capture != nil {
		go func() {
			select {
			case <-c.capture.failed:
				_ = c.signal(unix.SIGKILL)
			case <-c.done:
			}
		}()
	}
	if c.codex != nil {
		if err := c.codex.Initialize(ctx); err != nil {
			_ = p.stop(context.Background(), id, true)
			return "", err
		}
		if p.createThread {
			c.threadID, err = c.codex.StartThread(ctx, p.config.WorkingDirectory)
			if err != nil {
				_ = p.stop(context.Background(), id, true)
				return "", err
			}
		}
	}
	if ctx.Err() != nil || p.closing.Load() {
		_ = p.stop(context.Background(), id, true)
		return "", ErrProcessDeadline
	}
	if c.codex != nil {
		select {
		case <-c.done:
			return "", ErrProcess
		default:
		}
	}
	p.observation.publish(ProcessStatus{ProcessID: id, State: agentdv1.Heartbeat_RUNNING, ProtocolReady: c.codex != nil})
	return id, nil
}
func (c *child) reap() {
	// Retain the exited leader as a zombie until group cleanup. Its PID/PGID cannot
	// be reused between observing exit and signalling remaining group members.
	var info unix.Siginfo
	var err error
	for {
		err = unix.Waitid(unix.P_PID, c.cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err != unix.EINTR {
			break
		}
	}
	c.owner.observation.publish(ProcessStatus{ProcessID: c.id, State: agentdv1.Heartbeat_STOPPING})
	c.mu.Lock()
	if err == nil {
		if e := unix.Kill(-c.cmd.Process.Pid, unix.SIGKILL); e != nil && e != unix.ESRCH {
			c.err = ErrProcess
		}
	} else {
		c.err = ErrProcess
	} // Do not signal a possibly reused process group.
	// Bound pipe draining as well as queue waits after leader exit. WaitDelay
	// closes pipes held by escaped descendants; the timer also wakes blocked writers.
	var drain *time.Timer
	if c.capture != nil {
		drain = time.AfterFunc(c.cmd.WaitDelay, func() { c.capture.fail(ErrOutputIO) })
	}
	waitErr := c.cmd.Wait() // Exit code is not Execution success.
	c.reaped = true
	c.mu.Unlock()
	if c.closeCodex() != nil {
		c.err = ErrProcess
	}
	if c.capture != nil {
		var exit *exec.ExitError
		if waitErr != nil && !errors.As(waitErr, &exit) {
			c.capture.fail(ErrOutputIO)
		}
		c.stdout.finish()
		c.stderr.finish()
		if !drain.Stop() {
			c.capture.fail(ErrOutputIO)
		}
		if c.capture.finish() != nil {
			c.err = ErrProcess
		}
	}
	s := ProcessStatus{ProcessID: c.id, State: agentdv1.Heartbeat_EXITED}
	if state := c.cmd.ProcessState; state != nil {
		s.ExitObserved = true
		s.ExitCode = state.ExitCode()
		if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			s.Signal = int(ws.Signal())
		}
		if state.ExitCode() != 0 && (s.Signal == 0 || !c.stopRequested.Load()) {
			s.State = agentdv1.Heartbeat_FAILED
			s.Failure = ProcessExitFailed
		}
	}
	if c.err != nil {
		s.State = agentdv1.Heartbeat_FAILED
		s.Failure = ProcessCleanupFailed
	}
	if c.capture != nil {
		c.capture.mu.Lock()
		failed := c.capture.err != nil
		c.capture.mu.Unlock()
		if failed {
			s.State = agentdv1.Heartbeat_FAILED
			s.Failure = ProcessOutputFailed
		}
	}
	c.owner.observation.publish(s)
	close(c.done)
}
func (c *child) signal(sig unix.Signal) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reaped {
		return nil
	}
	if err := unix.Kill(-c.cmd.Process.Pid, sig); err != nil && err != unix.ESRCH {
		return ErrProcess
	}
	return nil
}
func (p *Processes) stop(ctx context.Context, id primitives.ID, force bool) error {
	c := p.current
	if c == nil || id == "" || c.id != id {
		return ErrProcessStale
	}
	select {
	case <-c.done:
		return c.err
	default:
	}
	c.stopRequested.Store(true)
	p.observation.publish(ProcessStatus{ProcessID: c.id, State: agentdv1.Heartbeat_STOPPING})
	if !force && ctx.Err() == nil {
		if err := c.signal(unix.SIGTERM); err != nil {
			return err
		}
		timer := time.NewTimer(time.Duration(p.config.StopGraceMS) * time.Millisecond)
		select {
		case <-c.done:
			timer.Stop()
			return c.err
		case <-ctx.Done():
		case <-timer.C:
		}
		timer.Stop()
	}
	if err := c.signal(unix.SIGKILL); err != nil {
		return err
	}
	// Cleanup continues for its own finite window even when the caller timed out.
	timer := time.NewTimer(time.Duration(p.config.KillWaitMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-c.done:
		return c.err
	case <-timer.C:
		return ErrProcess
	}
}
