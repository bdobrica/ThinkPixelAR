package agentdv1

import (
	"google.golang.org/protobuf/reflect/protoreflect"
	"testing"
)

// Published field numbers and the streaming service shape cannot be silently
// reassigned by regenerating the implementation from a changed schema.
func TestV1WireCompatibility(t *testing.T) {
	service := File_api_agentd_v1_agentd_proto.Services().ByName("AgentTransport")
	method := service.Methods().ByName("Connect")
	if !method.IsStreamingClient() || !method.IsStreamingServer() || method.Input().FullName() != "thinkpixel.agentd.v1.Envelope" || method.Output().FullName() != method.Input().FullName() {
		t.Fatal("Connect changed")
	}
	for name, fields := range map[protoreflect.Name]map[protoreflect.Name]protoreflect.FieldNumber{
		"Envelope": {"major": 1, "minor": 2, "connection_id": 3, "connection_epoch": 4, "message_id": 5, "sequence": 6, "binding": 7, "harness_handle": 8, "operation_id": 9, "request_digest": 10, "sent_unix_ms": 11, "deadline_unix_ms": 12, "hello": 20, "welcome": 21, "command": 22, "observation": 23, "acknowledgement": 24, "heartbeat": 25, "rotation": 26, "failure": 27},
		"Hello":    {"versions": 1, "binding": 2, "challenge": 3, "build_digest": 4, "adapter_kind": 5, "adapter_digest": 6, "supported_capabilities": 7, "required_capabilities": 8, "limits": 9, "bootstrap_proof": 10},
	} {
		for field, number := range fields {
			d := File_api_agentd_v1_agentd_proto.Messages().ByName(name).Fields().ByName(field)
			if d == nil || d.Number() != number {
				t.Fatalf("wire field changed: %s.%s", name, field)
			}
		}
	}
}
