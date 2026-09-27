package agentd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/harness/codex"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimebinding"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func restoreConfig(t *testing.T) Config {
	t.Helper()
	c, err := DecodeConfig(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	c.AdapterKind, c.Harness.Argv = codex.Kind, codex.Command()
	c.Capabilities = append(c.Capabilities, control.ThreadCapability, control.TurnCapability)
	c.RequiredCapabilities = append(c.RequiredCapabilities, control.ThreadCapability, control.TurnCapability)
	return c
}

func TestCodexRestoreSelection(t *testing.T) {
	c := restoreConfig(t)
	id := "01950000-0000-7000-8000-000000000099"
	raw := []byte(`{"type":"session_meta","payload":{"id":"` + id + `","cwd":"/workspace","cli_version":"` + codex.Version + `","timestamp":"2026-09-27T10:11:12Z"}}` + "\n")
	s := codex.RestoreState{ThreadID: id, Protocol: codex.Version, Rollout: raw, SHA256: fmt.Sprintf("%x", sha256.Sum256(raw))}
	p, err := NewProcessesWithCodexRestore(c, s)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = '!'
	if _, err := p.restore.RestorePath("/workspace"); err != nil {
		t.Fatal("caller changed selected bytes")
	}
	if _, err := NewProcessesWithCodexRestore(c, s); err == nil {
		t.Fatal("corruption accepted")
	}
	c.Capabilities, c.RequiredCapabilities = nil, nil
	if _, err := NewProcessesWithCodexRestore(c, *p.restore); err == nil {
		t.Fatal("restore without negotiated thread support")
	}
}

func TestCodexSupervisedRestoreFailureAndReplay(t *testing.T) {
	for _, mode := range []string{"resume", "resume-wrong"} {
		t.Run(mode, func(t *testing.T) {
			p := codexFixture(t, mode)
			p.createThread = true
			const thread = "01950000-0000-7000-8000-000000000099"
			payload, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"id": thread, "cwd": p.config.WorkingDirectory, "cli_version": codex.Version, "timestamp": "2026-09-27T10:11:12Z"}})
			payload = append(payload, '\n')
			p.restore = &codex.RestoreState{ThreadID: thread, Protocol: codex.Version, Rollout: payload, SHA256: fmt.Sprintf("%x", sha256.Sum256(payload))}
			ctl := &ProcessControl{processes: p, configuration: "test-config", operations: map[string]commandResult{}}
			command := controlCommand(t, agentdv1.Command_START, thread)
			result := ctl.execute(t.Context(), command)
			if result.failed != (mode == "resume-wrong") {
				t.Fatal("unexpected restore result")
			}
			if replay := ctl.execute(t.Context(), command); replay != result {
				t.Fatal("replay changed result")
			}
			if mode == "resume-wrong" {
				if p.Status().ProtocolReady {
					t.Fatal("bad resume became ready")
				}
				select {
				case <-p.current.done:
				default:
					t.Fatal("failed child not reaped")
				}
			} else {
				if result.thread.ThreadID != thread {
					t.Fatal("thread identity changed")
				}
				if err := p.Stop(t.Context(), p.current.id); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(p.current.codexHome); !os.IsNotExist(err) {
				t.Fatal("home not cleaned")
			}
			if _, err := p.Start(t.Context()); err == nil {
				t.Fatal("checkpoint import repeated")
			}
		})
	}
}

// Real process and protocol, deterministic loopback model; no provider secrets,
// K8s replacement, checkpoint publication or current authority proof is claimed.
func TestPinnedCodexRestoredSupervisor(t *testing.T) {
	binary := os.Getenv("THINKPIXELAR_TEST_CODEX_BINARY")
	if binary == "" {
		t.Skip("set THINKPIXELAR_TEST_CODEX_BINARY")
	}
	rawBinary, err := os.ReadFile(binary)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(rawBinary)) != codex.LinuxAMD64SHA256 {
		t.Fatal("pinned amd64 binary required")
	}
	t.Setenv("INHERIT_CANARY", "old-execution-credential-canary")
	const marker = "durable-conversation-74239"
	var calls atomic.Int32
	var history atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if calls.Add(1) == 2 {
			history.Store(strings.Contains(string(body), marker))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		item := map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": marker, "annotations": []any{}}}}
		for _, e := range []map[string]any{
			{"type": "response.created", "response": map[string]any{"id": "resp_fixture", "status": "in_progress", "output": []any{}}},
			{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}},
			{"type": "response.content_part.added", "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}},
			{"type": "response.output_text.delta", "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "delta": marker},
			{"type": "response.output_text.done", "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "text": marker},
			{"type": "response.output_item.done", "output_index": 0, "item": item},
			{"type": "response.completed", "response": map[string]any{"id": "resp_fixture", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 5, "output_tokens": 2, "total_tokens": 7}}},
		} {
			b, _ := json.Marshal(e)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e["type"], b)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	workspace := t.TempDir()
	configure := func(p *Processes) {
		p.config.WorkingDirectory = workspace // Test-only equivalent of /workspace.
		p.config.StartTimeoutMS = 10000
		p.config.Argv = append([]string{binary}, codex.Command()[1:]...)
		p.config.Argv = append(p.config.Argv, "-c", `model="fixture"`, "-c", `model_provider="fixture"`, "-c", `model_providers.fixture.name="fixture"`, "-c", "model_providers.fixture.base_url="+strconv.Quote(server.URL+"/v1"), "-c", `model_providers.fixture.wire_api="responses"`, "-c", `model_providers.fixture.requires_openai_auth=false`)
		t.Cleanup(func() { _ = p.Shutdown(context.Background(), nil) })
	}
	complete := func(p *Processes, generation uint64) {
		t.Helper()
		op, _ := primitives.NewID(time.Now())
		input, _ := primitives.NewID(time.Now())
		if err := p.startTurn(ctx, op, control.TurnInput{InputID: input, Classification: runtimeevent.Confidential, Text: "Continue the conversation. Do not run tools."}); err != nil {
			t.Fatal(err)
		}
		digest := "sha256:" + strings.Repeat("a", 64)
		h := harness.HarnessHandle{ID: input, ProcessInstanceID: p.current.id, Fence: harness.Fence{TenantID: input, SessionID: input, ExecutionID: input, AttemptID: input, SandboxBindingID: input, Generation: generation, AttemptOrdinal: 1}, AdapterKind: codex.Kind, AdapterBuildDigest: digest, NegotiationDigest: digest, VendorSessionReference: p.current.threadID}
		stream, err := p.current.codex.Events(h, runtimebinding.OperationIdentity{ID: op, RequestDigest: digest}, harness.Capabilities{harness.StructuredEvents: harness.Supported, harness.Streaming: harness.Supported, harness.UsageObservation: harness.Supported}, harness.AdapterLimits{EventBytes: 4096, EventsPerSecond: 1000, BufferedEvents: 1}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		completed := false
		for {
			e, err := stream.Next(ctx)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if e.Type == harness.ExecutionCompletionObserved {
				completed = true
			}
		}
		if !completed {
			t.Fatal("turn not completed")
		}
	}
	first, err := NewProcessesWithCapture(restoreConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	configure(first)
	id, err := first.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	complete(first, 1)
	thread, home := first.current.threadID, first.current.codexHome
	files, err := filepath.Glob(filepath.Join(home, "codex/sessions/*/*/*/*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatal("one rollout required")
	}
	rollout, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "old-credential"), []byte("old-execution-credential-canary"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := first.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("old home retained")
	}
	state := codex.RestoreState{ThreadID: thread, Protocol: codex.Version, Rollout: rollout, SHA256: fmt.Sprintf("%x", sha256.Sum256(rollout))}
	// Production constructor requires canonical /workspace; this local test uses
	// the same validated restore path with a temporary workspace and real binary.
	second, err := NewProcessesWithCapture(restoreConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	configure(second)
	if _, err := state.RestorePath(workspace); err != nil {
		t.Fatal("real rollout rejected", err)
	}
	second.restore = &state
	newID, err := second.Start(ctx)
	if err != nil {
		t.Fatal("restored startup", err)
	}
	if newID == id || second.current.codexHome == home || second.current.threadID != thread || !second.Status().ProtocolReady {
		t.Fatal("replacement continuity")
	}
	if calls.Load() != 1 {
		t.Fatal("resume initiated forward work")
	}
	for _, name := range []string{"old-credential", "codex/auth.json", "codex/config.toml"} {
		if _, err := os.Stat(filepath.Join(second.current.codexHome, name)); !os.IsNotExist(err) {
			t.Fatal("unexpected restored file")
		}
	}
	if len(second.current.cmd.Env) != 4 || strings.Contains(strings.Join(second.current.cmd.Env, "\n"), "canary") {
		t.Fatal("environment inherited")
	}
	complete(second, 2)
	if calls.Load() != 2 || !history.Load() {
		t.Fatal("durable conversation absent")
	}
	if err := second.Stop(ctx, newID); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Start(ctx); err == nil {
		t.Fatal("checkpoint silently replayed")
	}
	if _, err := second.Restart(ctx, newID); err == nil {
		t.Fatal("restart rewound state")
	}
	t.Log("fresh supervised process/home/pipes restored exact rollout and conversation; no automatic turn, old home deleted, repeat import rejected")
}
