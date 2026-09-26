package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
)

const testThreadID = "01950000-0000-7000-8000-000000000099"

func TestStartThread(t *testing.T) {
	thread := `{"id":"` + testThreadID + `","cwd":"/workspace","cliVersion":"0.155.0","ephemeral":false}`
	response := `{"id":2,"result":{"thread":` + thread + `,"cwd":"/workspace","approvalPolicy":"never","sandbox":{"type":"readOnly","networkAccess":false}}}` + "\n"
	notification := `{"method":"thread/started","emittedAtMs":1770000000000,"params":{"thread":` + thread + `}}` + "\n"
	for name, frames := range map[string]string{
		"success":                  response + notification,
		"notification first":       notification + response,
		"startup hint":             "{\"method\":\"remoteControl/status/changed\",\"params\":{\"secret\":\"canary\"}}\n" + response + notification,
		"conflict":                 response + strings.Replace(notification, testThreadID, "01950000-0000-7000-8000-000000000098", 1),
		"wrong id":                 strings.Replace(response, `"id":2`, `"id":1`, 1),
		"duplicate id":             strings.Replace(response, `"id":2`, `"id":0,"id":2`, 1),
		"missing notification":     response,
		"ephemeral":                strings.Replace(response, `"ephemeral":false`, `"ephemeral":true`, 1),
		"permissions":              strings.Replace(response, "readOnly", "dangerFullAccess", 1),
		"network":                  strings.Replace(response, `"networkAccess":false`, `"networkAccess":true`, 1),
		"missing network policy":   strings.Replace(response, `,"networkAccess":false`, "", 1),
		"duplicate network policy": strings.Replace(response, `"networkAccess":false`, `"networkAccess":true,"networkAccess":false`, 1),
		"bad timestamp":            response + strings.Replace(notification, "1770000000000", `"invalid"`, 1),
		"path as id":               strings.Replace(response, testThreadID, "/state/codex/secret", 1),
		"wrong workspace":          strings.Replace(response, "/workspace", "/elsewhere", 1),
		"flood":                    strings.Repeat("{\"method\":\"warning\",\"params\":{}}\n", 33),
		"server request":           "{\"id\":99,\"method\":\"item/permissions/requestApproval\",\"params\":{}}\n",
	} {
		t.Run(name, func(t *testing.T) {
			input, in := io.Pipe()
			out, output := io.Pipe()
			client := NewClient(in, out)
			client.initialized = true
			defer client.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer input.Close()
				defer output.Close()
				s := bufio.NewScanner(input)
				if !s.Scan() {
					t.Error("missing request")
					return
				}
				var request struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
					Params struct {
						CWD       string `json:"cwd"`
						Approval  string `json:"approvalPolicy"`
						Sandbox   string `json:"sandbox"`
						Ephemeral bool   `json:"ephemeral"`
					} `json:"params"`
				}
				if json.Unmarshal(s.Bytes(), &request) != nil || request.ID != 2 || request.Method != "thread/start" || request.Params.CWD != "/workspace" || request.Params.Approval != "never" || request.Params.Sandbox != "read-only" || request.Params.Ephemeral {
					t.Error("unsafe thread request")
					return
				}
				_, _ = io.WriteString(output, frames)
			}()
			id, err := client.StartThread(t.Context(), "/workspace")
			success := name == "success" || name == "notification first" || name == "startup hint"
			if success {
				if err != nil || id != testThreadID {
					t.Fatal("thread creation failed", err)
				}
				if again, e := client.StartThread(t.Context(), "/workspace"); e != nil || again != id {
					t.Fatal("replay changed identity", e)
				}
			} else if err == nil || id != "" {
				t.Fatal("invalid reply accepted")
			}
			if _, e := client.StartThread(t.Context(), "/elsewhere"); e != harness.ErrConflict {
				t.Fatal("changed request accepted")
			}
			client.Close()
			<-done
		})
	}
}

func TestThreadStartCancelledOutcomeNotRetried(t *testing.T) {
	input, in := io.Pipe()
	out, output := io.Pipe()
	defer input.Close()
	defer output.Close()
	c := NewClient(in, out)
	defer c.Close()
	if _, err := c.StartThread(t.Context(), "/workspace"); err != harness.ErrInvalid {
		t.Fatal("thread started before initialization")
	}
	c.initialized = true
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.StartThread(ctx, "/workspace"); done <- err }()
	if !bufio.NewScanner(input).Scan() {
		t.Fatal("request absent")
	}
	cancel()
	if err := <-done; err != harness.ErrOutcomeUnknown {
		t.Fatal(err)
	}
	if _, err := c.StartThread(t.Context(), "/workspace"); err != harness.ErrOutcomeUnknown {
		t.Fatal("ambiguous creation retried")
	}
}
