package workspace

import (
	"context"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type AttachmentScope struct {
	TenantID, SessionID, ExecutionID, AttemptID, SandboxID, WorkspaceID primitives.ID
	ExecutionGeneration, AttemptOrdinal, WorkspaceGeneration            uint64
}

// AttachRequest is admitted and durably reserved by the Workspace owner before
// any provider mutation. The request never contains provider-native manifests.
type AttachRequest struct {
	Scope                   AttachmentScope
	OperationID             primitives.ID
	RequestDigest           string
	MountPath               string
	ReadOnly                bool
	StorageProfileReference string
}
type Attachment struct {
	Scope                                          AttachmentScope
	Reference                                      string
	OperationID                                    primitives.ID
	RequestDigest                                  string
	State                                          string // PREPARED or ATTACHED; neither implies execution authority.
	ProviderKind                                   string
	WorkspaceVolumeReference, StateVolumeReference string // opaque outside adapters
	MountPath                                      string
	ReadOnly                                       bool
	StorageProfileReference, ConfigurationDigest   string
	CapacityBytes, StateCapacityBytes              int64
	AccessMode                                     string
	Encrypted, SnapshotCapable                     bool
}

// Materializer is the narrow sandbox-facing seam of WorkspaceProvider. Attach
// must use the existing single-writer reservation/fence, preserve source state and
// reconcile ambiguous outcomes by the same operation. Implementations belong to
// the Workspace owner, including a replaceable remote ThinkPixelWS adapter.
type Materializer interface {
	Attach(context.Context, AttachRequest) (Attachment, error)
}

// AttachmentReader reads authoritative reserved attachment metadata and verifies
// current tenant/Session/Attempt writer ownership. It must fail on a stale fence,
// deletion/snapshot conflict, ambiguous detach or an uncommitted materialization.
// This read-only interface is safe during pre-reservation blueprint resolution.
type AttachmentReader interface {
	GetAttachment(context.Context, primitives.ID, string) (Attachment, error)
}
