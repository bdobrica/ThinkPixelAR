package control

import (
	agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"
	"testing"
)

func TestShutdownObservationIsClosedHint(t *testing.T) {
	for _, payload := range []string{ShutdownStopped, ShutdownUnresolved, `{"managed_process":"running"}`, `{"managed_process":"stopped","execution":"complete"}`} {
		f := &agentdv1.Envelope{Body: &agentdv1.Envelope_Observation{Observation: &agentdv1.Observation{Kind: agentdv1.Observation_PROCESS_STATUS, PayloadSchema: ShutdownSchema, Payload: []byte(payload)}}}
		if IsShutdownObservation(f) != (payload == ShutdownStopped || payload == ShutdownUnresolved) {
			t.Fatal("unexpected schema admission")
		}
		f.OperationId = "operation"
		if IsShutdownObservation(f) {
			t.Fatal("final hint claimed operation correlation")
		}
	}
}
