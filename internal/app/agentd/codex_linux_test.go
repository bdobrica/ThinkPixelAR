package agentd

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/harness/codex"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/protocol"
)

func TestCodexChild(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "codex-fixture" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if len(os.Environ()) != 4 || os.Getenv("INHERIT_CANARY") != "" || !strings.HasPrefix(os.Getenv("HOME"), "/tmp/thinkpixel-codex-") || os.Getenv("CODEX_HOME") != os.Getenv("HOME")+"/codex" {
		syscall.Exit(71)
	}
	s := bufio.NewScanner(os.Stdin)
	if !s.Scan() {
		syscall.Exit(72)
	}
	var request map[string]json.RawMessage
	if json.Unmarshal(s.Bytes(), &request) != nil || string(request["method"]) != `"initialize"` {
		syscall.Exit(73)
	}
	if mode == "stall" {
		for s.Scan() {
		}
		syscall.Exit(0)
	}
	if mode == "exit" {
		syscall.Exit(7)
	}
	version := codex.Version
	if mode == "wrong-version" {
		version += "1"
	}
	_, _ = io.WriteString(os.Stdout, `{"id":1,"result":{"userAgent":"thinkpixelar/`+version+` (Linux)"}}`+"\n")
	if !s.Scan() || string(s.Bytes()) != `{"method":"initialized","params":{}}` {
		syscall.Exit(74)
	}
	if mode == "thread" || strings.HasPrefix(mode, "turn") {
		if !s.Scan() {
			syscall.Exit(75)
		}
		var start struct {
			Method string `json:"method"`
			Params struct {
				CWD string `json:"cwd"`
			} `json:"params"`
		}
		if json.Unmarshal(s.Bytes(), &start) != nil || start.Method != "thread/start" {
			syscall.Exit(76)
		}
		thread := map[string]any{"id": "01950000-0000-7000-8000-000000000099", "cwd": start.Params.CWD, "cliVersion": codex.Version, "ephemeral": false}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": 2, "result": map[string]any{"thread": thread, "cwd": start.Params.CWD, "approvalPolicy": "never", "sandbox": map[string]any{"type": "readOnly", "networkAccess": false}}})
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"method": "thread/started", "params": map[string]any{"thread": thread}})
	}
	if strings.HasPrefix(mode, "turn") {
		if !s.Scan() {
			syscall.Exit(77)
		}
		var turn struct {
			Method string
			Params struct{ Input []struct{ Type, Text string } }
		}
		if json.Unmarshal(s.Bytes(), &turn) != nil || turn.Method != "turn/start" || len(turn.Params.Input) != 1 || turn.Params.Input[0].Text != "execution prompt" {
			syscall.Exit(78)
		}
		_, _ = io.WriteString(os.Stdout, `{"id":3,"result":{"turn":{"id":"01950000-0000-7000-8000-000000000098","status":"inProgress","items":[]}}}`+"\n")
	}
	_, _ = io.WriteString(os.Stderr, "credential-canary\n")
	for s.Scan() {
		if strings.HasPrefix(mode, "turn") {
			var interrupt struct {
				ID     int
				Method string
				Params map[string]string
			}
			if json.Unmarshal(s.Bytes(), &interrupt) != nil || interrupt.ID != 4 || interrupt.Method != "turn/interrupt" || interrupt.Params["threadId"] != "01950000-0000-7000-8000-000000000099" || interrupt.Params["turnId"] != "01950000-0000-7000-8000-000000000098" {
				syscall.Exit(79)
			}
			if mode == "turn-reject" {
				_, _ = io.WriteString(os.Stdout, "{\"id\":4,\"error\":{\"message\":\"secret-canary\"}}\n")
			} else if mode != "turn-stall" {
				_, _ = io.WriteString(os.Stdout, "{\"id\":4,\"result\":{}}\n")
			}
		}
	}
	syscall.Exit(0)
}

func codexFixture(t *testing.T, mode string) *Processes {
	t.Helper()
	p, _ := processFixture(t, "ignore")
	p.codex = true
	p.commandBytes = 1024
	p.captureLimits = protocol.HardLimits()
	p.config.Argv = []string{p.config.Argv[0], "-test.run=^TestCodexChild$", "codex-fixture", mode}
	return p
}

func TestCodexSupervisedStartup(t *testing.T) {
	t.Setenv("INHERIT_CANARY", "credential-canary")
	p := codexFixture(t, "ready")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	id, err := p.Start(ctx)
	if err != nil || !p.Status().ProtocolReady {
		t.Fatal("handshake not ready", err)
	}
	stream, err := p.Output(id)
	if err != nil {
		t.Fatal(err)
	}
	out, err := stream.Receive(ctx)
	if err != nil || out.Source != OutputStderr || string(out.Data) != "[REDACTED]" {
		t.Fatal("protocol/credential data leaked", err)
	}
	home := p.current.codexHome
	if err := p.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("ephemeral home retained")
	}
	newID, err := p.Restart(ctx, id)
	if err != nil || newID == id || !p.Status().ProtocolReady || p.current.codexHome == home {
		t.Fatal("restart did not initialize a fresh process", err)
	}
}

func TestCodexFailedHandshakeStopsChild(t *testing.T) {
	for _, mode := range []string{"wrong-version", "stall", "exit"} {
		t.Run(mode, func(t *testing.T) {
			p := codexFixture(t, mode)
			p.config.StartTimeoutMS = 300
			id, err := p.Start(t.Context())
			if err == nil || id != "" || p.Status().ProtocolReady {
				t.Fatal("failed handshake accepted")
			}
			// A timed-out operation owns cleanup until it releases the gate.
			deadline := time.Now().Add(4 * time.Second)
			for !p.gate.TryLock() {
				if time.Now().After(deadline) {
					t.Fatal("cleanup stuck")
				}
				time.Sleep(time.Millisecond)
			}
			defer p.gate.Unlock()
			if p.current == nil {
				t.Fatal("child never launched")
			}
			select {
			case <-p.current.done:
			default:
				t.Fatal("child still alive")
			}
			if _, err := os.Stat(p.current.codexHome); !os.IsNotExist(err) {
				t.Fatal("failed startup retained home")
			}
		})
	}
}

func TestCodexBootstrapCommand(t *testing.T) {
	c, err := DecodeConfig(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	c.AdapterKind = codex.Kind
	c.Harness.Argv = codex.Command()
	p, err := NewProcesses(c)
	if err != nil || !p.codex {
		t.Fatal("Codex not selected", err)
	}
	c.Capabilities = append(c.Capabilities, control.ThreadCapability)
	if _, err := NewProcesses(c); err == nil {
		t.Fatal("optional thread capability accepted")
	}
	c.RequiredCapabilities = append(c.RequiredCapabilities, control.ThreadCapability)
	p, err = NewProcesses(c)
	if err != nil || !p.createThread {
		t.Fatal("thread mode not selected", err)
	}
	c.Capabilities = append(c.Capabilities, control.TurnCapability)
	if _, err := NewProcesses(c); err == nil {
		t.Fatal("optional turn capability accepted")
	}
	c.RequiredCapabilities = append(c.RequiredCapabilities, control.TurnCapability)
	p, err = NewProcesses(c)
	if err != nil || !p.executeTurn {
		t.Fatal("turn mode not selected", err)
	}
	c.Harness.Argv = append(c.Harness.Argv, "--listen", "ws://0.0.0.0:9999")
	if _, err := NewProcesses(c); err == nil {
		t.Fatal("unregistered command accepted")
	}
}

func TestCodexReadinessCannotResurrectStoppingProcess(t *testing.T) {
	var observation processObservation
	observation.publish(ProcessStatus{ProcessID: "process", State: agentdv1.Heartbeat_STOPPING})
	observation.publish(ProcessStatus{ProcessID: "process", State: agentdv1.Heartbeat_RUNNING, ProtocolReady: true})
	if s := observation.status(); s.State != agentdv1.Heartbeat_STOPPING || s.ProtocolReady {
		t.Fatal("late handshake resurrected stopping process")
	}
}

// This exercises production startup and command replay without model access.
func TestPinnedCodexSupervisedStartup(t *testing.T) {
	t.Run("handshake", func(t *testing.T) { pinnedCodexStartup(t, false) })
	t.Run("thread", func(t *testing.T) { pinnedCodexStartup(t, true) })
}

func pinnedCodexStartup(t *testing.T, thread bool) {
	binary := os.Getenv("THINKPIXELAR_TEST_CODEX_BINARY")
	if binary == "" {
		t.Skip("set THINKPIXELAR_TEST_CODEX_BINARY for pinned supervised startup")
	}
	if (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") || !filepath.IsAbs(binary) {
		t.Fatal("absolute pinned Linux amd64/arm64 binary required")
	}
	f, err := os.Open(binary)
	if err != nil {
		t.Fatal("cannot read executable")
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	_ = f.Close()
	pin := codex.LinuxAMD64SHA256
	if runtime.GOARCH == "arm64" {
		pin = codex.LinuxARM64SHA256
	}
	if err != nil || hex.EncodeToString(h.Sum(nil)) != pin {
		t.Fatal("executable pin mismatch")
	}
	p := codexFixture(t, "ready")
	p.createThread, p.executeTurn = thread, thread
	p.config.Argv = codex.Command()
	p.config.Argv[0] = binary // Test-only path; production bootstrap requires packaged location.
	p.config.StartTimeoutMS = 10000
	c := &ProcessControl{processes: p, configuration: "test-config", operations: map[string]commandResult{}}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	fstart := controlCommand(t, agentdv1.Command_START, "01950000-0000-7000-8000-000000000001")
	r := c.execute(ctx, fstart)
	if r.failed || !r.status.ProtocolReady {
		t.Fatal("pinned supervised startup failed", r.status.State, r.status.Failure)
	}
	if thread {
		if !codex.ValidThreadID(r.thread.ThreadID) || r.thread.ProcessID != r.status.ProcessID {
			t.Fatal("thread identity missing")
		}
		if _, err := p.Restart(ctx, r.status.ProcessID); err == nil {
			t.Fatal("restart silently replaced thread")
		}
	}
	if thread {
		if result := c.execute(ctx, turnCommand(t, fstart.HarnessHandle)); result.failed {
			t.Fatal("real supervised turn rejected")
		}
	}
	if replay := c.execute(ctx, fstart); replay != r {
		t.Fatal("replay launched another process")
	}
	if err := c.Disconnected(ctx); err != nil {
		t.Fatal("disconnect cleanup failed", err)
	}
	if p.Status().ProtocolReady || !p.Status().ExitObserved {
		t.Fatal("disconnect did not reap Codex")
	}
	t.Log("pinned supervised initialize/initialized, replay and disconnect cleanup: PASS")
}

func TestCodexControlledInterrupt(t *testing.T) {
	for _, mode := range []string{"turn", "turn-reject", "turn-stall"} {
		t.Run(mode, func(t *testing.T) {
			p := codexFixture(t, mode)
			p.createThread, p.executeTurn = true, true
			ctl := &ProcessControl{processes: p, configuration: "test-config", operations: map[string]commandResult{}}
			handle := "01950000-0000-7000-8000-000000000001"
			if ctl.execute(t.Context(), controlCommand(t, agentdv1.Command_START, handle)).failed {
				t.Fatal("start")
			}
			if ctl.execute(t.Context(), turnCommand(t, handle)).failed {
				t.Fatal("turn")
			}
			c := p.current.codex
			f := controlCommand(t, agentdv1.Command_INTERRUPT, handle)
			result := ctl.execute(t.Context(), f)
			if result.failed || !result.status.ExitObserved {
				t.Fatal("interrupt did not reap", result.status)
			}
			if replay := ctl.execute(t.Context(), f); replay != result {
				t.Fatal("interrupt replay changed")
			}
			// The successful fixture's ack stays replayable even after process reaping.
			if mode == "turn" && c.InterruptTurn(t.Context()) != nil {
				t.Fatal("cooperative path not used")
			}
		})
	}
}
