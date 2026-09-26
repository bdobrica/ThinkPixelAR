package postgres_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/harness/codex"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentd"
	transport "github.com/bdobrica/ThinkPixelAR/internal/ports/sandboxtransport"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"google.golang.org/protobuf/proto"
)

func TestCodexTurnDispatch(t *testing.T) {
	id := func() string { v, _ := primitives.NewID(time.Now()); return string(v) }
	payload := []byte(`{"input_id":"01950000-0000-7000-8000-000000000088","classification":"Confidential","text":"prompt-canary-do-not-persist"}`)
	db, p, m, intent, connection := policyFixtureConfigured(t, false, func(c *agentd.Config) {
		c.AdapterKind = codex.Kind
		c.Harness.Argv = codex.Command()
		c.Capabilities = append(c.Capabilities, control.ThreadCapability, control.TurnCapability)
		c.RequiredCapabilities = append(c.RequiredCapabilities, control.ThreadCapability, control.TurnCapability)
	}, func(m *postgres.AgentdMaterialization) {
		m.ExecuteOperationID = id()
		m.ExecuteDigest = control.TurnDigest(agentd.ConfigurationDigest(m.Config), m.HarnessHandle, payload)
	})
	ctx := t.Context()
	start := policyCommand(t, m, connection)
	execute := proto.Clone(start).(*agentdv1.Envelope)
	execute.OperationId = m.ExecuteOperationID
	execute.RequestDigest = m.ExecuteDigest
	execute.MessageId = id()
	execute.GetCommand().Kind = agentdv1.Command_EXECUTE
	execute.GetCommand().PayloadSchema = control.TurnCapability
	execute.GetCommand().Payload = payload
	if p.AuthorizeFrame(ctx, intent, connection, execute) == nil {
		t.Fatal("turn before thread accepted")
	}
	if e := p.AuthorizeFrame(ctx, intent, connection, start); e != nil {
		t.Fatal(e)
	}
	report := proto.Clone(start).(*agentdv1.Envelope)
	report.MessageId = id()
	report.Sequence = 1
	raw, _ := json.Marshal(control.ThreadStarted{ProcessID: primitives.ID(id()), ThreadID: "01950000-0000-7000-8000-000000000099"})
	report.Body = &agentdv1.Envelope_Observation{Observation: &agentdv1.Observation{Kind: agentdv1.Observation_PROCESS_STATUS, PayloadSchema: control.ThreadCapability, Payload: raw}}
	if e := p.AuthorizeFrame(ctx, intent, connection, report); e != nil {
		t.Fatal(e)
	}
	ack := proto.Clone(report).(*agentdv1.Envelope)
	ack.MessageId = id()
	ack.Sequence = 2
	ack.Body = &agentdv1.Envelope_Acknowledgement{Acknowledgement: &agentdv1.Acknowledgement{MessageId: start.MessageId, AcceptedSequence: start.Sequence, RequestDigest: start.RequestDigest}}
	if e := p.AuthorizeFrame(ctx, intent, connection, ack); e != nil {
		t.Fatal(e)
	}
	execute.Sequence = 2
	for _, mode := range []string{"input", "operation", "handle", "epoch", "binding"} {
		bad := proto.Clone(execute).(*agentdv1.Envelope)
		switch mode {
		case "input":
			bad.GetCommand().Payload = []byte(strings.Replace(string(payload), "prompt-canary-do-not-persist", "tampered", 1))
			bad.RequestDigest = control.TurnDigest(agentd.ConfigurationDigest(m.Config), m.HarnessHandle, bad.GetCommand().Payload)
		case "operation":
			bad.OperationId = id()
		case "handle":
			bad.HarnessHandle = id()
		case "epoch":
			bad.ConnectionEpoch++
		case "binding":
			bad.Binding.AttemptId = id()
		}
		if p.AuthorizeFrame(ctx, intent, connection, bad) == nil {
			t.Fatal("invalid turn admitted", mode)
		}
	}
	if e := p.AuthorizeFrame(ctx, intent, connection, execute); e != nil {
		t.Fatal("turn dispatch", e)
	}
	if p.AuthorizeFrame(ctx, intent, connection, execute) == nil {
		t.Fatal("ambiguous delivery retried")
	}
	rebuilt, e := postgres.NewAgentdLocalPolicy(db, "local", m.Revision, "thinkpixelar/local")
	if e != nil {
		t.Fatal(e)
	}
	scope := intent.Binding.Request.Scope
	outcome, e := rebuilt.CommandOutcome(ctx, scope.TenantID, scope.SandboxID, primitives.ID(m.ExecuteOperationID), m.ExecuteDigest)
	if e != nil || outcome != transport.DispatchPending {
		t.Fatal("lost durable claim", e)
	}
	ack.OperationId = execute.OperationId
	ack.RequestDigest = execute.RequestDigest
	ack.MessageId = id()
	ack.Sequence = 3
	ack.Body = &agentdv1.Envelope_Acknowledgement{Acknowledgement: &agentdv1.Acknowledgement{MessageId: execute.MessageId, AcceptedSequence: execute.Sequence, RequestDigest: execute.RequestDigest}}
	if e := rebuilt.AuthorizeFrame(ctx, intent, connection, ack); e != nil {
		t.Fatal("turn acknowledgement", e)
	}
	outcome, e = rebuilt.CommandOutcome(ctx, scope.TenantID, scope.SandboxID, primitives.ID(m.ExecuteOperationID), m.ExecuteDigest)
	if e != nil || outcome != transport.DispatchAcknowledged {
		t.Fatal("lost acceptance", e)
	}
	// Neither snapshot nor dispatch journal/sequence persistence retains prompt text.
	for _, table := range []string{"agentd_admission", "agentd_commands", "agentd_frame_sequences"} {
		var leaked bool
		if e := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM `+table+` t WHERE tenant_id=$1 AND row_to_json(t)::text LIKE '%prompt-canary-do-not-persist%')`, scope.TenantID).Scan(&leaked); e != nil || leaked {
			t.Fatal("input persisted", table, e)
		}
	}
	if e := p.Revoke(ctx, scope.TenantID, scope.SandboxID); e != nil {
		t.Fatal(e)
	}
	if p.AuthorizeFrame(ctx, intent, connection, execute) == nil {
		t.Fatal("revoked dispatch")
	}
}
