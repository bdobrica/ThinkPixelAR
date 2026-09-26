package agentdserver

import (
	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/control"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/harness"
	"time"
)

// ExecutionCommand maps admitted application input to a closed wire command.
// Send still requires live authority, a matching immutable materialization and a
// durable dispatch claim. This constructor cannot authorize an Execution.
func ExecutionCommand(configuration string, h harness.HarnessHandle, r harness.ExecuteRequest) (Command, error) {
	raw, err := control.ExecutionInput(h, r)
	if err != nil {
		return Command{}, err
	}
	digest := control.TurnDigest(configuration, string(h.ID), raw)
	f := &agentdv1.Envelope{OperationId: string(r.Operation.ID), HarnessHandle: string(h.ID), RequestDigest: r.Operation.RequestDigest, SentUnixMs: time.Now().UnixMilli(), DeadlineUnixMs: r.Deadline.UnixMilli(), Body: &agentdv1.Envelope_Command{Command: &agentdv1.Command{Kind: agentdv1.Command_EXECUTE, ConfigurationDigest: configuration, PayloadSchema: control.TurnCapability, Payload: raw}}}
	if r.Operation.RequestDigest != digest || control.Command(f, configuration) != nil {
		return Command{}, control.ErrControl
	}
	return Command{OperationID: string(r.Operation.ID), ConfigurationDigest: configuration, HarnessHandle: string(h.ID), Kind: agentdv1.Command_EXECUTE, Payload: raw, Deadline: r.Deadline}, nil
}
func (Command) String() string     { return "[restricted agentd command]" }
func (c Command) GoString() string { return c.String() }
