package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

const turnOperation primitives.ID = "01950000-0000-7000-8000-000000000001"
const turnInput primitives.ID = "01950000-0000-7000-8000-000000000002"
const testTurnID = "01950000-0000-7000-8000-000000000003"

func TestStartTurn(t *testing.T) {
	good := `{"id":3,"result":{"turn":{"id":"` + testTurnID + `","status":"inProgress","items":[],"error":null}}}` + "\n"
	for name, response := range map[string]string{
		"accepted":          good,
		"hint":              "{\"method\":\"thread/status/changed\",\"params\":{}}\n" + good,
		"wrong rpc":         strings.Replace(good, `"id":3`, `"id":4`, 1),
		"duplicate rpc":     strings.Replace(good, `"id":3`, `"id":3,"id":3`, 1),
		"vendor error":      "{\"id\":3,\"error\":{\"message\":\"secret-canary\"}}\n",
		"bad turn":          strings.Replace(good, testTurnID, "/private/secret", 1),
		"already completed": strings.Replace(good, "inProgress", "completed", 1),
		"server request":    "{\"id\":5,\"method\":\"item/permissions/requestApproval\",\"params\":{}}\n",
		"truncated":         good[:20],
		"oversize":          strings.Repeat("x", maxHandshakeBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			input, in := io.Pipe()
			out, output := io.Pipe()
			c := NewClient(in, out)
			c.threadID, c.threadCWD = testThreadID, "/workspace"
			defer c.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer input.Close()
				defer output.Close()
				line, e := bufio.NewReader(input).ReadBytes('\n')
				if e != nil {
					t.Error("missing turn")
					return
				}
				var request struct {
					ID     int
					Method string
					Params struct {
						ThreadID string `json:"threadId"`
						CWD      string `json:"cwd"`
						Approval string `json:"approvalPolicy"`
						Input    []struct{ Type, Text string }
						Sandbox  struct {
							Type    string
							Network bool `json:"networkAccess"`
						} `json:"sandboxPolicy"`
					}
				}
				if json.Unmarshal(line, &request) != nil || request.ID != 3 || request.Method != "turn/start" || request.Params.ThreadID != testThreadID || request.Params.CWD != "/workspace" || request.Params.Approval != "never" || request.Params.Sandbox.Type != "readOnly" || request.Params.Sandbox.Network || len(request.Params.Input) != 1 || request.Params.Input[0].Type != "text" || request.Params.Input[0].Text != "unicode λ\n\"prompt\"" {
					t.Error("input/policy mapping changed")
				}
				_, _ = io.WriteString(output, response)
			}()
			id, err := c.StartTurn(t.Context(), turnOperation, turnInput, "unicode λ\n\"prompt\"")
			if name == "accepted" || name == "hint" {
				if err != nil || id != testTurnID {
					t.Fatal("turn rejected", err)
				}
				if again, e := c.StartTurn(t.Context(), turnOperation, turnInput, "unicode λ\n\"prompt\""); e != nil || again != id {
					t.Fatal("replay failed", e)
				}
			} else if err == nil || id != "" {
				t.Fatal("malformed response accepted")
			}
			if _, e := c.StartTurn(t.Context(), turnOperation, turnInput, "changed"); e != harness.ErrConflict {
				t.Fatal("changed input accepted")
			}
			c.Close()
			<-done
		})
	}
}

func TestTurnCancellation(t *testing.T) {
	input, in := io.Pipe()
	out, output := io.Pipe()
	defer input.Close()
	defer output.Close()
	c := NewClient(in, out)
	defer c.Close()
	c.threadID, c.threadCWD = testThreadID, "/workspace"
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); _, _ = bufio.NewReader(input).ReadBytes('\n'); cancel() }()
	if _, e := c.StartTurn(ctx, turnOperation, turnInput, "prompt"); e != harness.ErrOutcomeUnknown {
		t.Fatal(e)
	}
	if _, e := c.StartTurn(t.Context(), turnOperation, turnInput, "prompt"); e != harness.ErrOutcomeUnknown {
		t.Fatal("ambiguous retry", e)
	}
	<-done
}
