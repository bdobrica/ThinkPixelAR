package sandbox

import (
	"context"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// ComputeProvider is the lifecycle subset used by infrastructure reconciliation.
// Infrastructure observations never grant authority or complete an Execution.
type ComputeProvider interface {
	Acquire(context.Context, AcquireRequest) (Handle, error)
	Get(context.Context, primitives.ID, primitives.ID) (Status, error)
	Release(context.Context, primitives.ID, primitives.ID, Operation) error
}

type DesiredCompute string

const (
	ComputeRunning  DesiredCompute = "RUNNING"
	ComputeReleased DesiredCompute = "RELEASED"
)

// ComputeIntent comes only from authoritative AR storage. Version and Revision
// fence observations against changes while the provider call is in flight.
// ReleaseAuthorized means an exact durable cleanup intent, not a stale lease.
type ComputeIntent struct {
	Binding           Binding
	Desired           DesiredCompute
	Operation         Operation
	Version           uint64
	Revision          uint64
	Current           bool
	ReleaseAuthorized bool
}

type ComputeObservation struct {
	State            State
	Code             string
	Converged        bool
	RecoveryRequired bool
}

// ComputeStore revalidates the complete aggregate/operation fence when recording
// results. It must not hold a database transaction across provider calls.
type ComputeStore interface {
	LoadCompute(context.Context, primitives.ID, primitives.ID) (ComputeIntent, error)
	RecordCompute(context.Context, ComputeIntent, ComputeObservation) error
}

// ComputeAuthority checks current grant/revocation/lease state using trusted
// persisted identity. This is separate from infrastructure readiness and from
// worker coordination leases. Cleanup never requires renewed execution authority.
type ComputeAuthority interface {
	CheckCompute(context.Context, Scope) error
}
