package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/harness/codex"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	"github.com/bdobrica/ThinkPixelAR/internal/app/agentd"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"google.golang.org/protobuf/proto"
)

func TestCodexThreadPersistence(t *testing.T) {
	verifyThreadPersistence(t, "01950000-0000-7000-8000-000000000099", nil)
}

func verifyThreadPersistence(t *testing.T, threadID string, afterPersist func()) {
	t.Helper()
	db, p, m, intent, connection := policyFixtureConfigured(t, false, func(c *agentd.Config) {
		c.AdapterKind = codex.Kind
		c.Harness.Argv = codex.Command()
		c.Capabilities = append(c.Capabilities, control.ThreadCapability)
		c.RequiredCapabilities = append(c.RequiredCapabilities, control.ThreadCapability)
	})
	ctx := t.Context()
	command := policyCommand(t, m, connection)
	if err := p.AuthorizeFrame(ctx, intent, connection, command); err != nil {
		t.Fatal("dispatch", err)
	}
	processID, _ := primitives.NewID(time.Now())
	payload, _ := json.Marshal(control.ThreadStarted{ProcessID: processID, ThreadID: threadID})
	report := proto.Clone(command).(*agentdv1.Envelope)
	mid, _ := primitives.NewID(time.Now())
	report.MessageId = string(mid)
	report.Sequence = 1
	report.Body = &agentdv1.Envelope_Observation{Observation: &agentdv1.Observation{Kind: agentdv1.Observation_PROCESS_STATUS, PayloadSchema: control.ThreadCapability, Payload: payload}}
	ack := proto.Clone(report).(*agentdv1.Envelope)
	mid, _ = primitives.NewID(time.Now())
	ack.MessageId = string(mid)
	ack.Body = &agentdv1.Envelope_Acknowledgement{Acknowledgement: &agentdv1.Acknowledgement{MessageId: command.MessageId, AcceptedSequence: command.Sequence, RequestDigest: command.RequestDigest}}
	if p.AuthorizeFrame(ctx, intent, connection, ack) == nil {
		t.Fatal("START acknowledged without durable thread identity")
	}
	for _, mode := range []string{"epoch", "operation", "handle", "digest", "payload"} {
		bad := proto.Clone(report).(*agentdv1.Envelope)
		switch mode {
		case "epoch":
			bad.ConnectionEpoch++
		case "operation":
			id, _ := primitives.NewID(time.Now())
			bad.OperationId = string(id)
		case "handle":
			bad.HarnessHandle = string(processID)
		case "digest":
			bad.RequestDigest = testDigest('a')
		case "payload":
			bad.GetObservation().Payload = []byte(`{"process_id":"bad","thread_id":"/private/secret"}`)
		}
		if p.AuthorizeFrame(ctx, intent, connection, bad) == nil {
			t.Fatal("invalid identity report accepted", mode)
		}
	}
	if err := p.AuthorizeFrame(ctx, intent, connection, report); err != nil {
		t.Fatal("persist", err)
	}
	if err := p.AuthorizeFrame(ctx, intent, connection, report); err != nil {
		t.Fatal("identical replay", err)
	}
	conflict := proto.Clone(report).(*agentdv1.Envelope)
	conflict.Sequence = 2
	mid, _ = primitives.NewID(time.Now())
	conflict.MessageId = string(mid)
	conflict.GetObservation().Payload, _ = json.Marshal(control.ThreadStarted{ProcessID: processID, ThreadID: "01950000-0000-7000-8000-000000000098"})
	if p.AuthorizeFrame(ctx, intent, connection, conflict) == nil {
		t.Fatal("immutable thread changed")
	}
	ack.Sequence = 2
	if err := p.AuthorizeFrame(ctx, intent, connection, ack); err != nil {
		t.Fatal("ack after persistence", err)
	}
	if afterPersist != nil {
		afterPersist()
	}
	// A new reader instance has no process or protocol memory.
	store, err := postgres.NewHarnessBindings(db)
	if err != nil {
		t.Fatal(err)
	}
	s := intent.Binding.Request.Scope
	h, err := store.Load(ctx, s.TenantID, primitives.ID(m.HarnessHandle))
	if err != nil || h.VendorSessionReference != threadID || h.ProcessInstanceID != processID || h.Fence.SessionID != s.SessionID || h.Fence.AttemptID != s.AttemptID || h.Fence.Generation != s.Generation || h.AdapterBuildDigest != m.Config.AdapterDigest || h.NegotiationDigest != m.HarnessNegotiationDigest {
		t.Fatal("durable identity mismatch", err)
	}
	other, _ := primitives.NewID(time.Now())
	if _, err := store.Load(ctx, other, h.ID); err == nil {
		t.Fatal("cross-tenant read")
	}
	var count int
	var linked string
	if err := db.QueryRow(`SELECT count(*) FROM harness_bindings WHERE tenant_id=$1 AND harness_binding_id=$2`, s.TenantID, h.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate binding", err)
	}
	if err := db.QueryRow(`SELECT harness_binding_reference FROM attempts WHERE tenant_id=$1 AND attempt_id=$2`, s.TenantID, s.AttemptID).Scan(&linked); err != nil || linked != string(h.ID) {
		t.Fatal("Attempt linkage absent", err)
	}
	for _, kind := range []agentdv1.Command_Kind{agentdv1.Command_START, agentdv1.Command_RESTART} {
		second := policyCommand(t, m, connection)
		second.Sequence = 2
		second.GetCommand().Kind = kind
		second.RequestDigest = control.Digest(kind, second.GetCommand().ConfigurationDigest, m.HarnessHandle)
		if p.AuthorizeFrame(ctx, intent, connection, second) == nil {
			t.Fatal("thread silently replaced")
		}
	}
	if err := p.Revoke(ctx, s.TenantID, s.SandboxID); err != nil {
		t.Fatal(err)
	}
	report.Sequence = 3
	if p.AuthorizeFrame(ctx, intent, connection, report) == nil {
		t.Fatal("revoked identity report accepted")
	}
	if again, err := store.Load(ctx, s.TenantID, h.ID); err != nil || again != h {
		t.Fatal("retained identity changed after revocation", err)
	}
}

// Real thread creation feeds the same fenced persistence path used by Recv.
// Provider/connection authority is the existing database fixture, not live Kata.
func TestPinnedCodexThreadPersistence(t *testing.T) {
	binary := os.Getenv("THINKPIXELAR_TEST_CODEX_BINARY")
	if binary == "" || os.Getenv("THINKPIXELAR_TEST_DATABASE_URL") == "" {
		t.Skip("set pinned Codex binary and test database URL")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("absolute executable required")
	}
	f, err := os.Open(binary)
	if err != nil {
		t.Fatal("read executable")
	}
	hash := sha256.New()
	_, err = io.Copy(hash, f)
	_ = f.Close()
	if err != nil || hex.EncodeToString(hash.Sum(nil)) != codex.LinuxAMD64SHA256 {
		t.Fatal("executable pin mismatch")
	}
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, codex.Command()[1:]...)
	cmd.Env = []string{"HOME=" + home, "CODEX_HOME=" + home, "PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	cmd.Dir = home
	cmd.Stderr = io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Start() != nil {
		t.Fatal("launch")
	}
	client := codex.NewClient(in, out)
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		client.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	defer stop()
	if err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := client.StartThread(ctx, home)
	if err != nil {
		t.Fatal("real thread creation", err)
	}
	verifyThreadPersistence(t, id, stop)
	t.Log("real pinned thread identity retained after process termination: PASS")
}
