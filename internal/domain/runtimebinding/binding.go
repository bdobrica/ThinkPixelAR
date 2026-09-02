// Package runtimebinding contains provider-neutral Sandbox and harness bindings.
package runtimebinding

import (
	"errors"
	"regexp"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type SandboxState string
type HarnessState string
type SandboxOperation string

const (
	SandboxRequested    SandboxState = "REQUESTED"
	SandboxProvisioning SandboxState = "PROVISIONING"
	SandboxReady        SandboxState = "READY"
	SandboxActive       SandboxState = "ACTIVE"
	SandboxSuspending   SandboxState = "SUSPENDING"
	SandboxSuspended    SandboxState = "SUSPENDED"
	SandboxResuming     SandboxState = "RESUMING"
	SandboxReleasing    SandboxState = "RELEASING"
	SandboxReleased     SandboxState = "RELEASED"
	SandboxFailed       SandboxState = "FAILED"
	SandboxUnknown      SandboxState = "UNKNOWN"

	HarnessStarting     HarnessState = "STARTING"
	HarnessReady        HarnessState = "READY"
	HarnessExecuting    HarnessState = "EXECUTING"
	HarnessInterrupting HarnessState = "INTERRUPTING"
	HarnessExited       HarnessState = "EXITED"
	HarnessFailed       HarnessState = "FAILED"
	HarnessUnknown      HarnessState = "UNKNOWN"

	OperationAcquire SandboxOperation = "acquire"
	OperationSuspend SandboxOperation = "suspend"
	OperationResume  SandboxOperation = "resume"
	OperationRelease SandboxOperation = "release"
)

var (
	ErrInvalidBinding  = errors.New("invalid runtime binding")
	ErrVersionConflict = errors.New("runtime binding state version conflict")
	digestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	sandboxStates      = []SandboxState{SandboxRequested, SandboxProvisioning, SandboxReady, SandboxActive, SandboxSuspending, SandboxSuspended, SandboxResuming, SandboxReleasing, SandboxReleased, SandboxFailed, SandboxUnknown}
	harnessStates      = []HarnessState{HarnessStarting, HarnessReady, HarnessExecuting, HarnessInterrupting, HarnessExited, HarnessFailed, HarnessUnknown}
)

type OperationIdentity struct {
	ID            primitives.ID
	RequestDigest string
}

// SandboxBinding binds one physical Attempt to an AR Sandbox identity and opaque provider evidence.
type SandboxBinding struct {
	tenantID, id, sessionID, executionID, attemptID   primitives.ID
	executionGeneration, attemptNumber                uint64
	providerKind, providerReference, resolutionDigest string
	operations                                        map[SandboxOperation]OperationIdentity
	state                                             SandboxState
	reason, effectiveFactsDigest                      string
	stateVersion                                      uint64
	createdAt, updatedAt                              time.Time
	observedAt                                        *time.Time
}

func NewSandbox(tenantID, bindingID, sessionID, executionID, attemptID primitives.ID, generation, attemptNumber uint64,
	providerKind, providerReference, resolutionDigest string, acquire OperationIdentity, now time.Time) (*SandboxBinding, error) {
	if !validIDs(tenantID, bindingID, sessionID, executionID, attemptID) || generation == 0 || attemptNumber == 0 ||
		!bounded(providerKind, 128) || !optionalBounded(providerReference, 2048) || !validDigest(resolutionDigest) || !validOperation(acquire) || now.IsZero() {
		return nil, ErrInvalidBinding
	}
	now = now.UTC()
	return &SandboxBinding{tenantID: tenantID, id: bindingID, sessionID: sessionID, executionID: executionID, attemptID: attemptID,
		executionGeneration: generation, attemptNumber: attemptNumber, providerKind: providerKind, providerReference: providerReference,
		resolutionDigest: resolutionDigest, operations: map[SandboxOperation]OperationIdentity{OperationAcquire: acquire},
		state: SandboxRequested, createdAt: now, updatedAt: now}, nil
}

// BindProviderReference records the provider result once. An empty reference at
// construction supports durable reservation before an outcome-ambiguous Acquire.
func (b *SandboxBinding) BindProviderReference(reference string, expected uint64, now time.Time) error {
	if err := b.validateMutation(expected, now); err != nil {
		return err
	}
	if !bounded(reference, 2048) {
		return ErrInvalidBinding
	}
	if b.providerReference != "" {
		if b.providerReference == reference {
			return nil
		}
		return ErrInvalidBinding
	}
	b.providerReference = reference
	b.advance(now)
	return nil
}

func (b *SandboxBinding) RecordOperation(kind SandboxOperation, operation OperationIdentity, expected uint64, now time.Time) error {
	if err := b.validateMutation(expected, now); err != nil {
		return err
	}
	if kind == OperationAcquire || (kind != OperationSuspend && kind != OperationResume && kind != OperationRelease) || !validOperation(operation) {
		return ErrInvalidBinding
	}
	if current, ok := b.operations[kind]; ok {
		if current == operation {
			return nil
		}
		return ErrInvalidBinding
	}
	b.operations[kind] = operation
	b.advance(now)
	return nil
}

func (b *SandboxBinding) Observe(state SandboxState, reason, factsDigest string, observedAt time.Time, expected uint64, now time.Time) error {
	if err := b.validateMutation(expected, now); err != nil {
		return err
	}
	if _, err := primitives.ParseEnum(string(state), sandboxStates...); err != nil || !bounded(reason, 255) ||
		(factsDigest != "" && !validDigest(factsDigest)) || observedAt.IsZero() || observedAt.After(now.UTC()) ||
		(b.observedAt != nil && observedAt.Before(*b.observedAt)) {
		return ErrInvalidBinding
	}
	at := observedAt.UTC()
	b.state, b.reason, b.effectiveFactsDigest, b.observedAt = state, reason, factsDigest, &at
	b.advance(now)
	return nil
}

func (b *SandboxBinding) validateMutation(expected uint64, now time.Time) error {
	if b == nil || now.IsZero() {
		return ErrInvalidBinding
	}
	if expected != b.stateVersion {
		return ErrVersionConflict
	}
	if now.UTC().Before(b.updatedAt) {
		return ErrInvalidBinding
	}
	return nil
}
func (b *SandboxBinding) advance(now time.Time) { b.stateVersion++; b.updatedAt = now.UTC() }

// HarnessBinding binds one harness process instance to the exact current Sandbox and Attempt.
type HarnessBinding struct {
	tenantID, id, sessionID, executionID, attemptID, sandboxBindingID       primitives.ID
	executionGeneration, attemptNumber                                      uint64
	adapterKind, adapterVersion, adapterBuildDigest, negotiationDigest      string
	protocolName, protocolVersion, processReference, vendorSessionReference string
	start                                                                   OperationIdentity
	state                                                                   HarnessState
	reason                                                                  string
	stateVersion                                                            uint64
	createdAt, updatedAt                                                    time.Time
	observedAt                                                              *time.Time
}

type HarnessSpecification struct {
	AdapterKind, AdapterVersion, AdapterBuildDigest, NegotiationDigest      string
	ProtocolName, ProtocolVersion, ProcessReference, VendorSessionReference string
}

func NewHarness(tenantID, bindingID, sessionID, executionID, attemptID, sandboxBindingID primitives.ID,
	generation, attemptNumber uint64, specification HarnessSpecification, start OperationIdentity, now time.Time) (*HarnessBinding, error) {
	if !validIDs(tenantID, bindingID, sessionID, executionID, attemptID, sandboxBindingID) || generation == 0 || attemptNumber == 0 ||
		!validHarnessSpecification(specification) || !validOperation(start) || now.IsZero() {
		return nil, ErrInvalidBinding
	}
	now = now.UTC()
	return &HarnessBinding{tenantID: tenantID, id: bindingID, sessionID: sessionID, executionID: executionID, attemptID: attemptID,
		sandboxBindingID: sandboxBindingID, executionGeneration: generation, attemptNumber: attemptNumber,
		adapterKind: specification.AdapterKind, adapterVersion: specification.AdapterVersion, adapterBuildDigest: specification.AdapterBuildDigest,
		negotiationDigest: specification.NegotiationDigest, protocolName: specification.ProtocolName, protocolVersion: specification.ProtocolVersion,
		processReference: specification.ProcessReference, vendorSessionReference: specification.VendorSessionReference,
		start: start, state: HarnessStarting, createdAt: now, updatedAt: now}, nil
}

func (b *HarnessBinding) Observe(state HarnessState, reason string, observedAt time.Time, expected uint64, now time.Time) error {
	if b == nil || now.IsZero() {
		return ErrInvalidBinding
	}
	if expected != b.stateVersion {
		return ErrVersionConflict
	}
	if _, err := primitives.ParseEnum(string(state), harnessStates...); err != nil || !bounded(reason, 255) || observedAt.IsZero() ||
		observedAt.After(now.UTC()) || now.UTC().Before(b.updatedAt) || (b.observedAt != nil && observedAt.Before(*b.observedAt)) {
		return ErrInvalidBinding
	}
	at := observedAt.UTC()
	b.state, b.reason, b.observedAt = state, reason, &at
	b.stateVersion++
	b.updatedAt = now.UTC()
	return nil
}

func validHarnessSpecification(s HarnessSpecification) bool {
	for _, value := range []string{s.AdapterKind, s.AdapterVersion, s.ProtocolName, s.ProtocolVersion} {
		if !bounded(value, 128) {
			return false
		}
	}
	return validDigest(s.AdapterBuildDigest) && validDigest(s.NegotiationDigest) && bounded(s.ProcessReference, 255) && bounded(s.VendorSessionReference, 2048)
}
func validOperation(o OperationIdentity) bool { return validIDs(o.ID) && validDigest(o.RequestDigest) }
func validDigest(value string) bool           { return digestPattern.MatchString(value) }
func bounded(value string, maximum int) bool {
	_, err := primitives.BoundedString(value, 1, maximum, maximum)
	return err == nil
}
func optionalBounded(value string, maximum int) bool { return value == "" || bounded(value, maximum) }
func validIDs(values ...primitives.ID) bool {
	for _, value := range values {
		if _, err := primitives.ParseID(string(value)); err != nil {
			return false
		}
	}
	return true
}

func (b *SandboxBinding) ID() primitives.ID { return b.id }
func (b *SandboxBinding) ProviderReference() (string, bool) {
	return b.providerReference, b.providerReference != ""
}
func (b *SandboxBinding) State() SandboxState  { return b.state }
func (b *SandboxBinding) StateVersion() uint64 { return b.stateVersion }
func (b *SandboxBinding) Operation(kind SandboxOperation) (OperationIdentity, bool) {
	value, ok := b.operations[kind]
	return value, ok
}
func (b *SandboxBinding) ObservedAt() (time.Time, bool) {
	if b.observedAt == nil {
		return time.Time{}, false
	}
	return *b.observedAt, true
}
func (b *HarnessBinding) ID() primitives.ID               { return b.id }
func (b *HarnessBinding) State() HarnessState             { return b.state }
func (b *HarnessBinding) StateVersion() uint64            { return b.stateVersion }
func (b *HarnessBinding) SandboxBindingID() primitives.ID { return b.sandboxBindingID }
func (b *HarnessBinding) ObservedAt() (time.Time, bool) {
	if b.observedAt == nil {
		return time.Time{}, false
	}
	return *b.observedAt, true
}
