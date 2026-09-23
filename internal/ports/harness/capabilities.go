package harness

type HarnessCapability string
type CapabilityLevel string

const (
	Required    CapabilityLevel = "REQUIRED"
	Supported   CapabilityLevel = "SUPPORTED"
	Unsupported CapabilityLevel = "UNSUPPORTED"

	StructuredEvents        HarnessCapability = "structured-events"
	Streaming               HarnessCapability = "streaming"
	Resume                  HarnessCapability = "resume"
	Interrupt               HarnessCapability = "interrupt"
	Signals                 HarnessCapability = "signals"
	CheckpointPrepare       HarnessCapability = "checkpoint-prepare"
	NativeFork              HarnessCapability = "native-fork"
	StructuredToolEvents    HarnessCapability = "structured-tool-events"
	StructuredProcessEvents HarnessCapability = "structured-process-events"
	UsageObservation        HarnessCapability = "usage-observation"
	LocalApprovalEvents     HarnessCapability = "local-approval-events"
	MultiInput              HarnessCapability = "multi-input"
)

// Capabilities describes an immutable adapter build. Missing entries mean
// unsupported; callers must not treat a capability as Run or fork authorization.
type Capabilities map[HarnessCapability]CapabilityLevel

func registered(c HarnessCapability) bool {
	switch c {
	case StructuredEvents, Streaming, Resume, Interrupt, Signals, CheckpointPrepare,
		NativeFork, StructuredToolEvents, StructuredProcessEvents, UsageObservation,
		LocalApprovalEvents, MultiInput:
		return true
	}
	return false
}

// Validate rejects unknown declarations and invalid levels. Unknown optional
// request names are handled by negotiation, not by expanding this registry.
func (c Capabilities) Validate() error {
	for name, level := range c {
		if !registered(name) || (level != Required && level != Supported && level != Unsupported) {
			return ErrInvalid
		}
	}
	return nil
}

func (c Capabilities) Supports(name HarnessCapability) bool {
	return registered(name) && (c[name] == Required || c[name] == Supported)
}

// Require is the capability gate, not full compatibility negotiation. Unknown
// requirements fail closed. REQUIRED is an implementation level, not authority.
func (c Capabilities) Require(names ...HarnessCapability) error {
	if err := c.Validate(); err != nil {
		return err
	}
	for _, name := range names {
		if !c.Supports(name) {
			return ErrUnsupported
		}
	}
	return nil
}
