package control

import (
	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"google.golang.org/protobuf/proto"
	"testing"
	"time"
)

func validCommand() *agentdv1.Envelope {
	f := &agentdv1.Envelope{HarnessHandle: "01950000-0000-7000-8000-000000000001", OperationId: "01950000-0000-7000-8000-000000000002", SentUnixMs: time.Now().UnixMilli(), DeadlineUnixMs: time.Now().Add(time.Minute).UnixMilli(), Sequence: 1, Body: &agentdv1.Envelope_Command{Command: &agentdv1.Command{Kind: agentdv1.Command_START, ConfigurationDigest: "configured", PayloadSchema: Capability}}}
	f.RequestDigest = Digest(f.GetCommand().Kind, "configured", f.HarnessHandle)
	return f
}
func TestClosedCommands(t *testing.T) {
	if Command(validCommand(), "configured") != nil {
		t.Fatal("valid command rejected")
	}
	for _, mode := range []string{"argv", "schema", "artifact", "config", "handle", "operation", "digest", "expired", "execute"} {
		t.Run(mode, func(t *testing.T) {
			f := validCommand()
			switch mode {
			case "argv":
				f.GetCommand().Payload = []byte("untrusted")
			case "schema":
				f.GetCommand().PayloadSchema = "unknown"
			case "artifact":
				f.GetCommand().ArtifactReference = "untrusted"
			case "config":
				f.GetCommand().ConfigurationDigest = "other"
			case "handle":
				f.HarnessHandle = "other"
			case "operation":
				f.OperationId = "other"
			case "digest":
				f.RequestDigest = "other"
			case "expired":
				f.DeadlineUnixMs = time.Now().Add(-time.Second).UnixMilli()
			case "execute":
				f.GetCommand().Kind = agentdv1.Command_EXECUTE
			}
			if Command(f, "configured") == nil {
				t.Fatal("unsafe command accepted")
			}
		})
	}
}
func TestSequenceReplayAndGaps(t *testing.T) {
	var s Sequences
	f := validCommand()
	if s.Check(nil) == nil || s.Check(f) != nil || s.Check(proto.Clone(f).(*agentdv1.Envelope)) != nil {
		t.Fatal("sequence validation")
	}
	conflict := proto.Clone(f).(*agentdv1.Envelope)
	conflict.OperationId = f.HarnessHandle
	if s.Check(conflict) == nil {
		t.Fatal("conflicting replay accepted")
	}
	f.Sequence = 3
	if s.Check(f) == nil {
		t.Fatal("gap accepted")
	}
	f.Sequence = 2
	if s.Check(f) != nil {
		t.Fatal("next frame rejected")
	}
	f.Sequence = 1
	if s.Check(f) == nil {
		t.Fatal("old frame accepted")
	}
}
