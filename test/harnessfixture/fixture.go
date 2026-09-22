// Package harnessfixture supplies a deterministic test-only local protocol.
// It is not a production HarnessAdapter or an authority implementation.
package harnessfixture

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

var ErrFixture = errors.New("test harness protocol failed")

// Serve accepts one bounded command per local connection, serially. State
// changes are explicit, never driven by sleeps, models, external tools or files.
func Serve(path string, events io.Writer) error {
	l, err := net.Listen("unix", path)
	if err != nil {
		return ErrFixture
	}
	defer l.Close()
	if os.Chmod(path, 0600) != nil {
		return ErrFixture
	}
	if _, err = fmt.Fprintln(events, "fixture.v1 ready"); err != nil {
		return ErrFixture
	}
	state := "idle"
	for {
		c, err := l.Accept()
		if err != nil {
			return ErrFixture
		}
		_ = c.SetDeadline(time.Now().Add(2 * time.Second))
		scan := bufio.NewScanner(c)
		scan.Buffer(make([]byte, 128), 128)
		command := ""
		if scan.Scan() {
			command = scan.Text()
		}
		reply := "rejected"
		switch command {
		case "hello fixture.v1":
			reply = "fixture.v1"
		case "status":
			reply = state
		case "execute":
			if state == "idle" {
				state = "running"
				reply = state
			}
		case "complete", "interrupt":
			if state == "running" {
				state = "idle"
				reply = state
			}
		case "prepare":
			if state == "idle" {
				state = "quiesced"
				reply = state
			}
		case "release":
			if state == "quiesced" {
				state = "idle"
				reply = state
			}
		case "close":
			reply = "closed"
		case "crash":
			_ = c.Close()
			return ErrFixture
		}
		if reply != "rejected" && command != "status" && command != "hello fixture.v1" {
			if _, err = fmt.Fprintln(events, "fixture.v1 "+reply); err != nil {
				_ = c.Close()
				return ErrFixture
			}
		}
		_, err = fmt.Fprintln(c, reply)
		_ = c.Close()
		if err != nil {
			return ErrFixture
		}
		if command == "close" {
			return nil
		}
	}
}

// Adapter is a test driver for one process-local socket. Callers supply the
// expected reply: protocol drift, rejection, EOF and timeout are fixed failures.
// Agentd's process gate supplies process-ID fencing; this socket grants none.
type Adapter struct{ Path string }

func (a Adapter) Expect(ctx context.Context, command, want string) error {
	if len(command) > 64 || len(want) > 64 {
		return ErrFixture
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(ctx, "unix", a.Path)
	if err != nil {
		return ErrFixture
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	_ = c.SetDeadline(deadline)
	if _, err = fmt.Fprintln(c, command); err != nil {
		return ErrFixture
	}
	scan := bufio.NewScanner(c)
	scan.Buffer(make([]byte, 128), 128)
	if !scan.Scan() || scan.Text() != want || ctx.Err() != nil {
		return ErrFixture
	}
	return nil
}

// Sanitize accepts only the fixture's closed event vocabulary. Tests can adapt
// this to agentd.OutputSanitizer without allowing arbitrary harness output.
func Sanitize(raw []byte) ([]byte, error) {
	switch string(raw) {
	case "fixture.v1 ready", "fixture.v1 running", "fixture.v1 idle", "fixture.v1 quiesced", "fixture.v1 closed":
		return append([]byte(nil), raw...), nil
	default:
		return nil, ErrFixture
	}
}
