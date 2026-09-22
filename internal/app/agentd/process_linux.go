package agentd

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"golang.org/x/sys/unix"
)

var (
	ErrProcess         = errors.New("harness process operation failed")
	ErrProcessBusy     = errors.New("harness process operation busy")
	ErrProcessStale    = errors.New("harness process identity mismatch")
	ErrProcessDeadline = errors.New("harness process deadline exceeded")
)

// Processes controls one local child group. IDs are process observations, not AR
// HarnessHandles or authority. Call only after authenticated command admission.
// Start means OS launch, not a completed adapter handshake or Ready state.
type Processes struct {
	gate    sync.Mutex
	config  HarnessConfig
	current *child
}

type child struct {
	id     primitives.ID
	cmd    *exec.Cmd
	mu     sync.Mutex // Serialize group signals with final reaping (and PID reuse).
	reaped bool
	done   chan struct{}
	err    error // Published by closing done.
}

func NewProcesses(c Config) (*Processes, error) {
	if c.Validate() != nil {
		return nil, ErrConfig
	}
	h := c.Harness
	h.Argv = slices.Clone(h.Argv)
	return &Processes{config: h}, nil
}

// operation has no queue. A timed-out OS operation keeps the gate until cleanup
// completes; another command must never overlap an unresolved launch or stop.
func (p *Processes) operation(ctx context.Context, budget time.Duration, f func(context.Context) (primitives.ID, error)) (primitives.ID, error) {
	if ctx.Err() != nil {
		return "", ErrProcessDeadline
	}
	if !p.gate.TryLock() {
		return "", ErrProcessBusy
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
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
			// A launch whose result was not delivered must not survive as hidden work.
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
	// No shell, inherited extra descriptors, terminal or output logging. Capture is AGD-007.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if cmd.Start() != nil {
		return "", ErrProcess
	}
	c := &child{id: id, cmd: cmd, done: make(chan struct{})}
	p.current = c
	go c.reap()
	if ctx.Err() != nil {
		_ = p.stop(context.Background(), id, true)
		return "", ErrProcessDeadline
	}
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
	c.mu.Lock()
	if err == nil {
		if e := unix.Kill(-c.cmd.Process.Pid, unix.SIGKILL); e != nil && e != unix.ESRCH {
			c.err = ErrProcess
		}
	} else {
		c.err = ErrProcess
	} // Do not signal a possibly reused process group.
	_ = c.cmd.Wait() // Reap the direct child; exit code is not Execution success.
	c.reaped = true
	c.mu.Unlock()
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
