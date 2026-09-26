package agentdserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func TestExecutionCommand(t *testing.T) {
	id := func() primitives.ID { v, _ := primitives.NewID(time.Now()); return v }
	h := harness.HarnessHandle{ID: id(), Fence: harness.Fence{TenantID: id(), SessionID: id(), ExecutionID: id(), AttemptID: id(), SandboxBindingID: id(), Generation: 1, AttemptOrdinal: 1}}
	r := harness.ExecuteRequest{InputID: id(), Input: harness.Content{Schema: "text/plain", Classification: runtimeevent.Confidential, Inline: []byte("prompt λ")}}
	r.Fence = h.Fence
	r.Operation.ID = id()
	r.Deadline = time.Now().Add(time.Minute)
	raw, _ := control.ExecutionInput(h, r)
	r.Operation.RequestDigest = control.TurnDigest("config", string(h.ID), raw)
	c, e := ExecutionCommand("config", h, r)
	if e != nil {
		t.Fatal(e)
	}
	if c.OperationID != string(r.Operation.ID) || c.HarnessHandle != string(h.ID) {
		t.Fatal("correlation lost")
	}
	v, e := control.DecodeTurnInput(c.Payload)
	if e != nil || v.Text != "prompt λ" || v.InputID != r.InputID {
		t.Fatal("input lost")
	}
	r.Input.Inline[0] = 'x'
	v, _ = control.DecodeTurnInput(c.Payload)
	if v.Text != "prompt λ" {
		t.Fatal("payload not copied")
	}
	r.Input.Inline[0] = 'p'
	for _, mode := range []string{"fence", "options", "artifact", "classification", "oversize", "utf8", "schema", "digest", "deadline"} {
		bad := r
		bad.Input = r.Input
		switch mode {
		case "fence":
			bad.Fence.ExecutionID = id()
		case "options":
			bad.Options = map[string]string{"sandbox": "danger-full-access"}
		case "artifact":
			bad.Input.ArtifactReference = "private://input"
		case "classification":
			bad.Input.Classification = "Secret"
		case "oversize":
			bad.Input.Inline = []byte(strings.Repeat("x", control.MaxTurnTextBytes+1))
		case "utf8":
			bad.Input.Inline = []byte{0xff}
		case "schema":
			bad.Input.Schema = "application/json"
		case "digest":
			bad.Operation.RequestDigest = "wrong"
		case "deadline":
			bad.Deadline = time.Now().Add(-time.Second)
		}
		if _, e := ExecutionCommand("config", h, bad); e == nil {
			t.Fatal("invalid request accepted", mode)
		}
	}
	var obj map[string]any
	_ = json.Unmarshal(c.Payload, &obj)
	obj["model"] = "override"
	bad, _ := json.Marshal(obj)
	if _, e := control.DecodeTurnInput(bad); e == nil {
		t.Fatal("option smuggling")
	}
	if _, e := control.DecodeTurnInput(append(c.Payload, []byte(" ")...)); e == nil {
		t.Fatal("noncanonical payload")
	}
}
