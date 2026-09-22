package agentd

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"github.com/bdobrica/ThinkPixelAR/test/harnessfixture"
)

func TestStructuredHarnessChild(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "structured-fixture" {
		return
	}
	if harnessfixture.Serve(os.Args[len(os.Args)-1], os.Stdout) != nil {
		syscall.Exit(7)
	}
	syscall.Exit(0)
}

func TestStructuredHarnessLifecycle(t *testing.T) {
	p, root := processFixture(t, "ignore")
	socket := filepath.Join(root, "h.sock")
	p.config.Argv = []string{p.config.Argv[0], "-test.run=^TestStructuredHarnessChild$", "structured-fixture", socket}
	p.config.StopGraceMS = 1000
	p.commandBytes = 1024
	p.captureLimits = protocol.HardLimits()
	p.sanitizer = func(_ OutputSource, raw []byte) ([]byte, error) { return harnessfixture.Sanitize(raw) }
	a := harnessfixture.Adapter{Path: socket}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := p.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	output, err := p.Output(id)
	if err != nil {
		t.Fatal(err)
	}
	next := func(want string) {
		t.Helper()
		o, err := output.Receive(ctx)
		if err != nil || string(o.Data) != want || o.ProcessID != id {
			t.Fatal("unexpected fixture observation", err)
		}
	}
	next("fixture.v1 ready")
	expect := func(command, want string) {
		t.Helper()
		if err := a.Expect(ctx, command, want); err != nil {
			t.Fatal(command, err)
		}
	}
	expect("hello fixture.v2", "rejected")
	expect("hello fixture.v1", "fixture.v1")
	expect("status", "idle")
	expect("execute", "running")
	next("fixture.v1 running")
	expect("execute", "rejected")
	controls, err := NewControls(p, 1024, nil, func(ctx context.Context, _ primitives.ID, _ InterruptReason) error {
		return a.Expect(ctx, "interrupt", "idle")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = controls.Interrupt(ctx, id, InterruptCancel); err != nil {
		t.Fatal(err)
	}
	next("fixture.v1 idle")
	checkpoints, err := NewCheckpoints(p, []string{"/state/fixture"}, "fixture/v1", func(ctx context.Context, _ primitives.ID, _ []string, ready func(StateManifest) error) error {
		if err := a.Expect(ctx, "prepare", "quiesced"); err != nil {
			return err
		}
		result := ready(StateManifest{VendorIdentity: "fixture-session", StateFormat: "fixture/v1"})
		if err := a.Expect(ctx, "release", "idle"); err != nil {
			return err
		}
		return result
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = checkpoints.Prepare(ctx, id, func(ctx context.Context, _ StateManifest) error {
		if err := a.Expect(ctx, "status", "quiesced"); err != nil {
			return err
		}
		return a.Expect(ctx, "execute", "rejected")
	}); err != nil {
		t.Fatal(err)
	}
	next("fixture.v1 quiesced")
	next("fixture.v1 idle")
	expect("execute", "running")
	next("fixture.v1 running")
	expect("complete", "idle")
	next("fixture.v1 idle")
	expect("close", "closed")
	next("fixture.v1 closed")
	select {
	case <-p.current.done:
	case <-ctx.Done():
		t.Fatal("close did not exit")
	}
	old := id
	id, err = p.Restart(ctx, old)
	if err != nil || id == old {
		t.Fatal("restart identity", err)
	}
	output, err = p.Output(id)
	if err != nil {
		t.Fatal(err)
	}
	next("fixture.v1 ready")
	if err = controls.Interrupt(ctx, old, InterruptCancel); err != ErrProcessStale {
		t.Fatal("stale process accepted", err)
	}
	expect("status", "idle")
	if err = a.Expect(ctx, "crash", "closed"); err == nil {
		t.Fatal("crash acknowledged")
	}
	select {
	case <-p.current.done:
	case <-ctx.Done():
		t.Fatal("crash did not exit")
	}
	if status := p.Status(); !status.ExitObserved || status.ExitCode != 7 || status.Failure != ProcessExitFailed {
		t.Fatal("crash observation missing", status)
	}
}
