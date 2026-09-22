package harnessfixture

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdapterCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stall.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	received := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var b [64]byte
		_, _ = c.Read(b[:])
		close(received)
		_, _ = io.Copy(io.Discard, c)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- (Adapter{Path: path}).Expect(ctx, "status", "idle") }()
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("request not received")
	}
	cancel()
	select {
	case err := <-result:
		if err != ErrFixture {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt read")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("connection leaked")
	}
}
func TestFixtureRejectsUnclassifiedOutputAndOversizedCommand(t *testing.T) {
	for _, raw := range []string{"secret-canary", "fixture.v1 ready secret", strings.Repeat("x", 4096)} {
		if _, err := Sanitize([]byte(raw)); err != ErrFixture {
			t.Fatal("unclassified output accepted")
		}
	}
	if err := (Adapter{}).Expect(context.Background(), strings.Repeat("x", 65), "idle"); err != ErrFixture {
		t.Fatal("oversized command accepted")
	}
}
