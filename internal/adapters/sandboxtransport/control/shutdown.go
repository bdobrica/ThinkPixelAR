package control

import agentdv1 "github.com/bdobrica/ThinkPixelAR/api/agentd/v1"

const (
	ShutdownSchema     = Capability + "/shutdown"
	ShutdownStopped    = `{"managed_process":"stopped"}`
	ShutdownUnresolved = `{"managed_process":"unresolved"}`
)

// IsShutdownObservation recognizes a closed, non-authoritative local stop hint.
// It cannot acknowledge an operation or establish complete sandbox cleanup.
func IsShutdownObservation(f *agentdv1.Envelope) bool {
	o := f.GetObservation()
	return o != nil && f.OperationId == "" && f.RequestDigest == "" && f.HarnessHandle == "" &&
		o.Kind == agentdv1.Observation_PROCESS_STATUS && o.PayloadSchema == ShutdownSchema && o.ArtifactReference == "" &&
		(string(o.Payload) == ShutdownStopped || string(o.Payload) == ShutdownUnresolved)
}
