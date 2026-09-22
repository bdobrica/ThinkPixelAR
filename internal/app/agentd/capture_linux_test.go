package agentd

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
)

func TestCapturedProcessOutput(t *testing.T) {
	p, _ := processFixture(t, "output")
	p.captureLimits = protocol.HardLimits()
	id, err := p.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c, err := p.Output(id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	count := 0
	stdout, stderr := 0, 0
	for {
		out, err := c.Receive(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
		if out.ProcessID != id || out.Sequence != uint64(count) || string(out.Data) != "[REDACTED]" {
			t.Fatal("unsafe or unbound output")
		}
		switch out.Source {
		case OutputStdout:
			stdout++
		case OutputStderr:
			stderr++
		}
	}
	if stdout != 2 || stderr != 1 {
		t.Fatal("missing stream or EOF tail", stdout, stderr)
	}
	select {
	case <-p.current.done:
	case <-ctx.Done():
		t.Fatal("capture did not finish")
	}
	next, err := p.Restart(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Output(id); err != ErrProcessStale {
		t.Fatal("stale output accepted")
	}
	newer, err := p.Output(next)
	if err != nil || newer == c {
		t.Fatal("restart reused output")
	}
	// Drain the replacement before processFixture stops it.
	for {
		_, err = newer.Receive(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}
func TestCapturedProcessStopsOnStalledConsumer(t *testing.T) {
	p, _ := processFixture(t, "flood")
	l := protocol.HardLimits()
	l.BufferedEvents = 1
	l.BufferedBytes = 32
	l.LivenessWindowMs = 20
	p.captureLimits = l
	id, err := p.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c, err := p.Output(id)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.current.done:
	case <-time.After(3 * time.Second):
		t.Fatal("stalled capture left process alive")
	}
	if _, err := c.Receive(context.Background()); err != ErrOutputBackpressure {
		t.Fatal("missing stream failure", err)
	}
	if p.current.cmd.ProcessState == nil {
		t.Fatal("child not reaped")
	}
	if _, err := p.Start(context.Background()); err != ErrProcess {
		t.Fatal("failed stream silently restarted")
	}
}

func TestCapturedProcessStopBoundsBlockedOutput(t *testing.T) {
	p, _ := processFixture(t, "flood")
	p.config.KillWaitMS = 200
	l := protocol.HardLimits()
	l.BufferedEvents = 1
	p.captureLimits = l // 30s queue wait, 200ms exit drain.
	id, err := p.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c, err := p.Output(id)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		queued := len(c.queue)
		c.mu.Unlock()
		if queued > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no output")
		}
		time.Sleep(time.Millisecond)
	}
	began := time.Now()
	err = p.Stop(context.Background(), id)
	if err != ErrProcess && err != ErrProcessDeadline {
		t.Fatal("blocked drain not reported", err)
	}
	if time.Since(began) > time.Second {
		t.Fatal("Stop waited for the output consumer")
	}
	select {
	case <-p.current.done:
	case <-time.After(time.Second):
		t.Fatal("blocked output was not cleaned up")
	}
	if _, err := c.Receive(context.Background()); err != ErrOutputIO {
		t.Fatal("drain loss not visible", err)
	}
}

func TestCapturedProcessConfigurationIsPrivate(t *testing.T) {
	c, err := DecodeConfig(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProcessesWithCapture(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Limits.BufferedEvents = 0
	c.Harness.Argv[0] = "changed"
	if p.captureLimits.BufferedEvents == 0 || p.config.Argv[0] == "changed" {
		t.Fatal("capture configuration aliased")
	}
	if _, err := NewProcessesWithCapture(c, nil); err != ErrConfig {
		t.Fatal("invalid limits accepted")
	}
}
