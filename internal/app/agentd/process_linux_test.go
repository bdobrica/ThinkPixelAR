package agentd

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// The test binary is a deterministic child, selected only by test argv. There
// is no production executable, environment or workspace override.
func TestProcessChild(t *testing.T) {
	if len(os.Args) < 4 || os.Args[len(os.Args)-3] != "process-fixture" {
		return
	}
	mode, root := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
	if len(os.Environ()) != 0 {
		os.Exit(71)
	}
	if mode == "control" {
		signals := make(chan os.Signal, 2)
		signal.Notify(signals, syscall.SIGUSR1, syscall.SIGINT)
		if os.WriteFile(filepath.Join(root, "control-ready"), nil, 0600) != nil {
			os.Exit(76)
		}
		for sig := range signals {
			name := "notified"
			if sig == syscall.SIGINT {
				name = "interrupted"
			}
			if os.WriteFile(filepath.Join(root, name), nil, 0600) != nil {
				os.Exit(77)
			}
		}
	}
	if mode == "exit-error" {
		os.Exit(7)
	}
	if mode == "output" {
		_, _ = os.Stdout.Write([]byte("split-secret-"))
		_, _ = os.Stdout.Write([]byte("canary\nunterminated"))
		_, _ = os.Stderr.Write([]byte("private-key-canary\n"))
		os.Exit(0)
	}
	if mode == "flood" {
		for {
			if _, err := os.Stdout.Write([]byte("sensitive-canary\n")); err != nil {
				os.Exit(75)
			}
		}
	}
	if mode == "ignore" || mode == "descendant" {
		signal.Ignore(syscall.SIGTERM)
	}
	if mode == "parent" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestProcessChild$", "process-fixture", "descendant", root)
		cmd.Env = []string{}
		if cmd.Start() != nil {
			os.Exit(72)
		}
		if os.WriteFile(filepath.Join(root, "descendant"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600) != nil {
			os.Exit(73)
		}
	}
	if os.WriteFile(filepath.Join(root, mode+"-ready"), []byte("ready"), 0600) != nil {
		os.Exit(74)
	}
	for {
		if mode == "parent" {
			if _, err := os.Stat(filepath.Join(root, "exit")); err == nil {
				os.Exit(0)
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func processFixture(t *testing.T, mode string) (*Processes, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	p := &Processes{config: HarnessConfig{Argv: []string{exe, "-test.run=^TestProcessChild$", "process-fixture", mode, root}, WorkingDirectory: root, StartTimeoutMS: 2000, StopGraceMS: 100, KillWaitMS: 2000}}
	t.Cleanup(func() {
		// A deadline may leave a bounded cleanup worker briefly holding the gate.
		deadline := time.Now().Add(4 * time.Second)
		for !p.gate.TryLock() {
			if time.Now().After(deadline) {
				t.Error("process worker leaked")
				return
			}
			time.Sleep(time.Millisecond)
		}
		defer p.gate.Unlock()
		if p.current != nil {
			if err := p.stop(context.Background(), p.current.id, true); err != nil {
				// Captured-stream failures remain terminal even after reaping.
				if p.current.capture == nil {
					t.Error(err)
				} else {
					select {
					case <-p.current.done:
					default:
						t.Error("capture cleanup leaked", err)
					}
				}
			}
		}
	})
	return p, root
}
func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("child readiness timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func waitGate(t *testing.T, p *Processes) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if p.gate.TryLock() {
			p.gate.Unlock()
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("operation did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestProcessStartStopRestart(t *testing.T) {
	t.Setenv("THINKPIXEL_TEST_SECRET", "must-not-reach-child")
	p, root := processFixture(t, "ignore")
	id, err := p.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(root, "ignore-ready"))
	if _, err := p.Start(context.Background()); err != ErrProcessBusy {
		t.Fatal("concurrent process allowed", err)
	}
	if err := p.Stop(context.Background(), primitives.ID("stale")); err != ErrProcessStale {
		t.Fatal("stale stop accepted")
	}
	old := p.current
	next, err := p.Restart(context.Background(), id)
	if err != nil || next == id || next == "" {
		t.Fatal("restart failed", err)
	}
	select {
	case <-old.done:
	default:
		t.Fatal("replacement launched before old child reaped")
	}
	if old.cmd.ProcessState == nil || !old.cmd.ProcessState.Exited() && old.cmd.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatal("old process not killed")
	}
	if err := p.Stop(context.Background(), id); err != ErrProcessStale {
		t.Fatal("old identity stopped replacement")
	}
	if err := p.Stop(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if err := p.Stop(context.Background(), next); err != nil {
		t.Fatal("stop not idempotent", err)
	}
}
func TestProcessDeadlineAndConcurrentCommands(t *testing.T) {
	p, root := processFixture(t, "ignore")
	id, err := p.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(root, "ignore-ready"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Restart(ctx, id); err != ErrProcessDeadline {
		t.Fatal("cancelled restart accepted")
	}
	p.config.StopGraceMS = 1000
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	began := time.Now()
	// Timeout forces kill and prohibits a replacement, regardless of result race.
	if _, err := p.Restart(ctx, id); err != ErrProcessDeadline {
		t.Fatal("restart outlived deadline", err)
	}
	if time.Since(began) > time.Second {
		t.Fatal("unbounded caller wait")
	}
	waitGate(t, p)
	select {
	case <-p.current.done:
	default:
		t.Fatal("timed-out restart left child alive")
	}
	if p.current.id != id {
		t.Fatal("cancelled restart launched replacement")
	}
	var wg sync.WaitGroup
	results := make(chan primitives.ID, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := p.Start(context.Background())
			if err == nil {
				results <- id
			} else if err != ErrProcessBusy {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	close(results)
	count := 0
	for range results {
		count++
	}
	if count != 1 {
		t.Fatal("overlapping launches", count)
	}
}
func TestProcessExitCleansGroup(t *testing.T) {
	p, root := processFixture(t, "parent")
	if _, err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(root, "descendant-ready"))
	raw, err := os.ReadFile(filepath.Join(root, "descendant"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "exit"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.current.done:
	case <-time.After(3 * time.Second):
		t.Fatal("leader not reaped")
	}
	deadline := time.Now().Add(time.Second)
	for {
		raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if os.IsNotExist(err) {
			break
		}
		// Orphan reaping belongs to sandbox PID 1; a zombie is no longer executing.
		if err == nil && strings.Contains(string(raw), ") Z ") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("descendant survived leader exit")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestProcessRejectsConfigAndLaunchFailure(t *testing.T) {
	if _, err := NewProcesses(Config{}); err != ErrConfig {
		t.Fatal("invalid config accepted")
	}
	c, err := DecodeConfig(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProcesses(c)
	if err != nil {
		t.Fatal(err)
	}
	c.Harness.Argv[0] = "mutated"
	if p.config.Argv[0] == "mutated" {
		t.Fatal("configuration aliased")
	}
	p, _ = processFixture(t, "ignore")
	p.config.Argv = []string{"/does-not-exist/restricted-detail"}
	if _, err := p.Start(context.Background()); err != ErrProcess {
		t.Fatal("launch error not sanitized", err)
	}
	waitGate(t, p)
	if p.current != nil {
		t.Fatal("failed launch retained a child")
	}
}

func TestProcessUndeliveredLaunchIsCleaned(t *testing.T) {
	p, _ := processFixture(t, "ignore")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := p.operation(ctx, time.Second, func(ctx context.Context) (primitives.ID, error) {
			id, err := p.start(ctx)
			close(started)
			<-ctx.Done() // Simulate cancellation before the launch result is delivered.
			return id, err
		})
		result <- err
	}()
	<-started
	cancel()
	if err := <-result; err != ErrProcessDeadline {
		t.Fatal("cancelled result accepted", err)
	}
	waitGate(t, p)
	if p.current == nil {
		t.Fatal("fixture did not launch")
	}
	select {
	case <-p.current.done:
	default:
		t.Fatal("undelivered child survived")
	}
}
