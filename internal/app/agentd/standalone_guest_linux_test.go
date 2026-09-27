package agentd

// This guest is an operator-invoked integration fixture, compiled only into the
// test binary. Kubernetes exec is its trusted test transport, not agentd mTLS.
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

func TestStandaloneKubernetesGuest(t *testing.T) {
	phase := os.Getenv("THINKPIXELAR_E2E_GUEST")
	if phase == "" {
		t.Skip("operator-invoked Kubernetes guest")
	}
	if phase != "first" && phase != "ready" && phase != "second" {
		t.Fatal("unknown phase")
	}
	const marker = "e2e-001-durable-conversation-74239"
	const workspaceText = "E2E-001 durable workspace\n"
	raw, err := os.ReadFile(codex.Command()[0])
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != codex.LinuxARM64SHA256 {
		t.Fatal("pinned ARM64 Codex required")
	}
	if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token"); !os.IsNotExist(err) {
		t.Fatal("service-account token present")
	}
	work, err := os.ReadFile("/workspace/context.txt")
	if err != nil || string(work) != workspaceText {
		t.Fatal("workspace continuity failed")
	}
	c := restoreConfig(t)
	if err := json.Unmarshal([]byte(os.Getenv("THINKPIXELAR_E2E_BINDING")), &c.Binding); err != nil || c.Validate() != nil {
		t.Fatal("binding required")
	}
	var calls atomic.Int32
	var history atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		calls.Add(1)
		history.Store(strings.Contains(string(body), marker))
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
	var p *Processes
	var selected codex.RestoreState
	if phase == "first" {
		p, err = NewProcessesWithCapture(c, nil)
	} else {
		metadata, e := os.ReadFile("/state/restore.json")
		if e != nil || json.Unmarshal(metadata, &selected) != nil {
			t.Fatal("restore metadata")
		}
		selected.Rollout, err = os.ReadFile("/state/rollout.jsonl")
		if err != nil {
			t.Fatal(err)
		}
		p, err = NewProcessesWithCodexRestore(c, selected)
	}
	if err != nil {
		t.Fatal(err)
	}
	// Test-only deterministic model route, never read from checkpoint content.
	p.config.Argv = append(p.config.Argv, "-c", `model="fixture"`, "-c", `model_provider="fixture"`, "-c", `model_providers.fixture.name="fixture"`, "-c", "model_providers.fixture.base_url="+strconv.Quote(server.URL+"/v1"), "-c", `model_providers.fixture.wire_api="responses"`, "-c", `model_providers.fixture.requires_openai_auth=false`)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = p.Shutdown(context.Background(), nil) })
	id, err := p.Start(ctx)
	if err != nil {
		t.Fatal("start", err)
	}
	thread, home := p.current.threadID, p.current.codexHome
	if phase != "first" && thread != selected.ThreadID {
		t.Fatal("thread changed")
	}
	if calls.Load() != 0 {
		t.Fatal("restore started forward work")
	}
	for _, name := range []string{"old-credential", "codex/auth.json", "codex/config.toml"} {
		if _, e := os.Stat(filepath.Join(home, name)); !os.IsNotExist(e) {
			t.Fatal("old state imported")
		}
	}
	if len(p.current.cmd.Env) != 4 {
		t.Fatal("unexpected inherited environment")
	}
	if phase != "ready" {
		op, _ := primitives.NewID(time.Now())
		input, _ := primitives.NewID(time.Now())
		if err := p.startTurn(ctx, op, control.TurnInput{InputID: input, Classification: runtimeevent.Confidential, Text: "Continue the conversation. Do not run tools."}); err != nil {
			t.Fatal(err)
		}
		b := c.Binding
		digest := "sha256:" + codex.LinuxARM64SHA256
		h := harness.HarnessHandle{ID: input, ProcessInstanceID: id, Fence: harness.Fence{TenantID: primitives.ID(b.TenantId), SessionID: primitives.ID(b.SessionId), ExecutionID: primitives.ID(b.ExecutionId), AttemptID: primitives.ID(b.AttemptId), SandboxBindingID: primitives.ID(b.SandboxBindingId), Generation: b.SessionGeneration, AttemptOrdinal: 1}, AdapterKind: codex.Kind, AdapterBuildDigest: digest, NegotiationDigest: digest, VendorSessionReference: thread}
		stream, e := p.current.codex.Events(h, runtimebinding.OperationIdentity{ID: op, RequestDigest: digest}, harness.Capabilities{harness.StructuredEvents: harness.Supported, harness.Streaming: harness.Supported, harness.UsageObservation: harness.Supported}, harness.AdapterLimits{EventBytes: 4096, EventsPerSecond: 1000, BufferedEvents: 1}, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer stream.Close()
		completed := false
		for {
			event, e := stream.Next(ctx)
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			if event.Type == harness.ExecutionCompletionObserved {
				completed = true
			}
		}
		if !completed || calls.Load() != 1 || (phase == "second" && !history.Load()) {
			t.Fatal("completion or conversation continuity failed")
		}
	}
	if phase == "first" {
		files, e := filepath.Glob(filepath.Join(home, "codex/sessions/*/*/*/*.jsonl"))
		if e != nil || len(files) != 1 {
			t.Fatal("one rollout required")
		}
		rollout, e := os.ReadFile(files[0])
		if e != nil {
			t.Fatal(e)
		}
		selected = codex.RestoreState{ThreadID: thread, Protocol: codex.Version, SHA256: fmt.Sprintf("%x", sha256.Sum256(rollout)), Rollout: rollout}
		if _, e = selected.RestorePath("/workspace"); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(home, "old-credential"), []byte("E2E-001-old-credential-canary"), 0600); e != nil {
			t.Fatal(e)
		}
		writeGuestFile(t, "/state/rollout.jsonl", rollout)
		selected.Rollout = nil
		metadata, _ := json.Marshal(selected)
		writeGuestFile(t, "/state/restore.json", metadata)
	}
	if err = p.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("old process home retained")
	}
	result, _ := json.Marshal(struct {
		Phase, Thread, SHA256 string
		Calls                 int32
		History               bool
		Binding               *agentdv1.Binding
	}{phase, thread, selected.SHA256, calls.Load(), history.Load(), c.Binding})
	fmt.Printf("E2E_GUEST_RESULT %s\n", result)
}

func writeGuestFile(t *testing.T, name string, b []byte) {
	t.Helper()
	f, e := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil {
		t.Fatal("durable guest write", e, ce)
	}
}
