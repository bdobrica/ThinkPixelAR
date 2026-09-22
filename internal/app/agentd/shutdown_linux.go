package agentd

import (
	"context"
	"os"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"golang.org/x/sys/unix"
)

func (p *Processes) initLifetime() {
	p.lifeOnce.Do(func() {
		p.lifetime, p.cancelLifetime = context.WithCancel(context.Background())
		p.shutdownDone = make(chan struct{})
	})
}
func (p *Processes) shutdownBudget() time.Duration {
	return time.Duration(p.config.StartTimeoutMS+3*p.config.StopGraceMS+3*p.config.KillWaitMS) * time.Millisecond
}

// Shutdown permanently closes command admission. The first call selects the
// negotiated hook; all callers observe the same cleanup. Caller cancellation
// stops waiting, not cleanup. The independent cleanup budget must fit within the
// externally configured Pod termination grace. No checkpoint is attempted.
func (p *Processes) Shutdown(ctx context.Context, hook InterruptHandler) error {
	p.initLifetime()
	p.shutdownOnce.Do(func() {
		p.closing.Store(true)
		p.cancelLifetime()
		go func() {
			defer close(p.shutdownDone)
			cleanup, cancel := context.WithTimeout(context.Background(), p.shutdownBudget())
			defer cancel()
			p.shutdownErr = p.shutdown(cleanup, hook)
		}()
	})
	timer := time.NewTimer(p.shutdownBudget())
	defer timer.Stop()
	select {
	case <-p.shutdownDone:
		return p.shutdownErr
	case <-ctx.Done():
		return ErrProcessDeadline
	case <-timer.C:
		return ErrProcessDeadline
	}
}

func (p *Processes) shutdown(ctx context.Context, hook InterruptHandler) error {
	// Pending commands were cancelled above. Do not run a second adapter callback
	// while one is still active. A late launch observes closure and kills itself.
	wait, cancel := context.WithTimeout(ctx, time.Duration(p.config.StartTimeoutMS)*time.Millisecond+p.stopBudget())
	defer cancel()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !p.gate.TryLock() {
		select {
		case <-wait.Done():
			if c := p.active.Load(); c != nil {
				c.stopRequested.Store(true)
				_ = c.signal(unix.SIGKILL)
				select {
				case <-c.done:
				case <-ctx.Done():
				}
			}
			return ErrProcess
		case <-tick.C:
		}
	}
	defer p.gate.Unlock()
	c := p.current
	unresolved := false
	var stopErr error
	if c != nil {
		select {
		case <-c.done:
		default:
			c.stopRequested.Store(true)
			p.observation.publish(ProcessStatus{ProcessID: c.id, State: agentdv1.Heartbeat_STOPPING})
			if hook != nil && ctx.Err() == nil {
				grace, cancel := context.WithTimeout(ctx, time.Duration(p.config.StopGraceMS)*time.Millisecond)
				result := make(chan error, 1)
				go func() {
					result <- controlInvoke(func(ctx context.Context) error { return hook(ctx, c.id, InterruptShutdown) }, grace)
				}()
				select {
				case err := <-result:
					if err == nil {
						select {
						case <-c.done:
						case <-grace.Done():
						}
					}
				case <-grace.Done():
					unresolved = true
				}
				cancel()
			}
		}
		stopErr = p.stop(ctx, c.id, false)
		if stopErr != nil {
			select {
			case <-c.done:
			case <-ctx.Done():
				return stopErr
			}
		}
	}
	// Only PID 1 owns arbitrary adopted descendants. Never race exec.Cmd.Wait:
	// the gate is closed and the managed leader's done is observed before Wait4.
	if os.Getpid() == 1 {
		if err := reapOrphans(ctx); err != nil {
			return err
		}
	}
	if unresolved {
		return ErrProcess
	}
	return stopErr
}

func reapOrphans(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ErrProcessDeadline
		}
		pid, err := unix.Wait4(-1, nil, unix.WNOHANG, nil)
		if err == unix.ECHILD {
			return nil
		}
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return ErrProcess
		}
		if pid == 0 {
			select {
			case <-ctx.Done():
				return ErrProcessDeadline
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
}
