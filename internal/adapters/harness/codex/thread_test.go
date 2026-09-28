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

func TestOpenThread(t *testing.T) {
	for _, resume := range []bool{false, true} {
		name := "start"
		if resume {
			name = "resume"
		}
		t.Run(name, func(t *testing.T) { testOpenThread(t, resume) })
	}
}

func testOpenThread(t *testing.T, resume bool) {
	open := func(c *Client, cwd string) (string, error) {
		if resume {
			return c.ResumeThread(t.Context(), cwd, testThreadID)
		}
		return c.StartThread(t.Context(), cwd)
	}
	method := "thread/start"
	if resume {
		method = "thread/resume"
	}
	thread := `{"id":"` + testThreadID + `","cwd":"/workspace","cliVersion":"0.155.0","ephemeral":false,"status":{"type":"idle"}}`
	response := `{"id":2,"result":{"thread":` + thread + `,"cwd":"/workspace","approvalPolicy":"never","sandbox":{"type":"readOnly","networkAccess":false}}}` + "\n"
	notification := `{"method":"thread/started","emittedAtMs":1770000000000,"params":{"thread":` + thread + `}}` + "\n"
	if resume {
		notification = `{"method":"thread/status/changed","emittedAtMs":1770000000000,"params":{"threadId":"` + testThreadID + `","status":{"type":"idle"}}}` + "\n"
	}
	for name, frames := range map[string]string{
		"success":                  response + notification,
		"notification first":       notification + response,
		"startup hint":             "{\"method\":\"remoteControl/status/changed\",\"params\":{\"secret\":\"canary\"}}\n" + response + notification,
		"conflict":                 response + strings.Replace(notification, testThreadID, "01950000-0000-7000-8000-000000000098", 1),
		"wrong resumed identity":   strings.ReplaceAll(response+notification, testThreadID, "01950000-0000-7000-8000-000000000098"),
		"vendor error":             "{\"id\":2,\"error\":{\"code\":-32600,\"message\":\"secret\"}}\n",
		"wrong id":                 strings.Replace(response, `"id":2`, `"id":1`, 1),
		"duplicate id":             strings.Replace(response, `"id":2`, `"id":0,"id":2`, 1),
		"missing notification":     response,
		"ephemeral":                strings.Replace(response, `"ephemeral":false`, `"ephemeral":true`, 1),
		"permissions":              strings.Replace(response, "readOnly", "dangerFullAccess", 1),
		"active thread":            strings.Replace(response, `"type":"idle"`, `"type":"active"`, 1) + notification,
		"network":                  strings.Replace(response, `"networkAccess":false`, `"networkAccess":true`, 1),
		"missing network policy":   strings.Replace(response, `,"networkAccess":false`, "", 1),
		"duplicate network policy": strings.Replace(response, `"networkAccess":false`, `"networkAccess":true,"networkAccess":false`, 1),
		"bad timestamp":            response + strings.Replace(notification, "1770000000000", `"invalid"`, 1),
		"path as id":               strings.Replace(response, testThreadID, "/state/codex/secret", 1),
		"wrong workspace":          strings.Replace(response, "/workspace", "/elsewhere", 1),
		"flood":                    strings.Repeat("{\"method\":\"warning\",\"params\":{}}\n", 33),
		"server request":           "{\"id\":99,\"method\":\"item/permissions/requestApproval\",\"params\":{}}\n",
	} {
		if resume {
			switch name {
			case "conflict":
				frames = strings.Replace(notification, testThreadID, "01950000-0000-7000-8000-000000000098", 1) + response
			case "missing notification":
				frames = notification // Resume requires its result, not thread/started.
			case "bad timestamp":
				frames = strings.Replace(notification, "1770000000000", `"invalid"`, 1) + response
			}
		}
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
						CWD          string `json:"cwd"`
						Approval     string `json:"approvalPolicy"`
						Sandbox      string `json:"sandbox"`
						Ephemeral    bool   `json:"ephemeral"`
						ThreadID     string `json:"threadId"`
						ExcludeTurns bool   `json:"excludeTurns"`
					} `json:"params"`
				}
				if json.Unmarshal(s.Bytes(), &request) != nil || request.ID != 2 || request.Method != method || request.Params.CWD != "/workspace" || request.Params.Approval != "never" || request.Params.Sandbox != "read-only" || request.Params.Ephemeral {
					t.Error("unsafe thread request")
					return
				}
				if resume && (request.Params.ThreadID != testThreadID || !request.Params.ExcludeTurns) {
					t.Error("unsafe resume request")
					return
				}
				var envelope map[string]json.RawMessage
				_ = json.Unmarshal(s.Bytes(), &envelope)
				var params map[string]json.RawMessage
				_ = json.Unmarshal(envelope["params"], &params)
				expected := 4
				if resume {
					expected = 5
				}
				if len(params) != expected {
					t.Error("unexpected thread parameters")
					return
				}
				_, _ = io.WriteString(output, frames)
			}()
			id, err := open(client, "/workspace")
			success := name == "success" || name == "notification first" || name == "startup hint" || ((name == "wrong resumed identity" || name == "active thread") && !resume)
			if success {
				expectedID := testThreadID
				if name == "wrong resumed identity" {
					expectedID = "01950000-0000-7000-8000-000000000098"
				}
				if err != nil || id != expectedID {
					t.Fatal("thread selection failed", err)
				}
				if resume {
					if _, e := client.StartThread(t.Context(), "/workspace"); e != harness.ErrConflict {
						t.Fatal("resume changed to creation")
					}
				} else {
					if _, e := client.ResumeThread(t.Context(), "/workspace", id); e != harness.ErrConflict {
						t.Fatal("creation changed to resume")
					}
				}
				if again, e := open(client, "/workspace"); e != nil || again != id {
					t.Fatal("replay changed identity", e)
				}
			} else if err == nil || id != "" {
				t.Fatal("invalid reply accepted")
			}
			if _, e := open(client, "/elsewhere"); e != harness.ErrConflict {
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

func TestResumeValidationAndReplay(t *testing.T) {
	input, in := io.Pipe()
	out, output := io.Pipe()
	defer input.Close()
	defer output.Close()
	c := NewClient(in, out)
	defer c.Close()
	for _, id := range []string{"", "/state/rollout.jsonl", "../secret", strings.ToUpper(testThreadID[:8]) + "-invalid"} {
		if _, err := c.ResumeThread(t.Context(), "/workspace", id); err != harness.ErrInvalid {
			t.Fatal("invalid identity accepted")
		}
	}
	if _, err := c.ResumeThread(t.Context(), "/workspace", testThreadID); err != harness.ErrInvalid {
		t.Fatal("resume before initialization")
	}
	c.initialized = true
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.ResumeThread(ctx, "/workspace", testThreadID); done <- err }()
	if !bufio.NewScanner(input).Scan() {
		t.Fatal("request absent")
	}
	cancel()
	if err := <-done; err != harness.ErrOutcomeUnknown {
		t.Fatal(err)
	}
	if _, err := c.ResumeThread(t.Context(), "/workspace", testThreadID); err != harness.ErrOutcomeUnknown {
		t.Fatal("ambiguous resume retried")
	}
	if _, err := c.StartThread(t.Context(), "/workspace"); err != harness.ErrConflict {
		t.Fatal("resume fell back to creation")
	}
	if _, err := c.ResumeThread(t.Context(), "/workspace", "01950000-0000-7000-8000-000000000098"); err != harness.ErrConflict {
		t.Fatal("changed resume target accepted")
	}
}

func TestInfrastructureThreadRejectsForwardWorkAndModeChange(t *testing.T) {
	c := &Client{initialized: true, threadAttempted: true, threadInfrastructure: true, threadID: testThreadID, threadResumeID: testThreadID, threadCWD: "/workspace"}
	if _, err := c.StartTurn(t.Context(), turnOperation, turnInput, "must not execute"); err != harness.ErrInvalid {
		t.Fatal("infrastructure probe accepted a turn", err)
	}
	if _, err := c.ResumeThread(t.Context(), "/workspace", testThreadID); err != harness.ErrConflict {
		t.Fatal("probe changed to executable thread", err)
	}
	if id, err := c.ResumeThreadForInfrastructure(t.Context(), "/workspace", testThreadID); err != nil || id != testThreadID {
		t.Fatal("probe replay", err)
	}
}
