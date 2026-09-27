package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var (
	ErrInvalid     = errors.New("workspace INVALID_REQUEST")
	ErrConflict    = errors.New("workspace CONFLICT")
	ErrIntegrity   = errors.New("workspace INTEGRITY")
	ErrNotFound    = errors.New("workspace NOT_FOUND")
	ErrUnavailable = errors.New("workspace UNAVAILABLE")
	ErrUnsupported = errors.New("workspace UNSUPPORTED")
)

type Operation struct {
	ID     primitives.ID
	Digest string
}
type CreateRequest struct {
	TenantID, SessionID, WorkspaceID    primitives.ID
	Operation                           Operation
	StorageProfile, ConfigurationDigest string
	CapacityBytes, StateCapacityBytes   int64
	AccessMode                          string
	EncryptionRequired                  bool
}
type Command struct {
	Kind                  string // create, get, delete
	TenantID, WorkspaceID primitives.ID
	Operation             Operation
	Create                CreateRequest // set only for create
}

// Reservation is durable owner metadata, never supplied by the workload.
type Reservation struct {
	Request                            CreateRequest
	WorkspaceReference, StateReference string
}

// Operations is the Workspace owner's admission and durable reconciliation seam.
// Do serializes the entire callback with create/delete/attach/snapshot operations
// on this Workspace (including across processes). Before invoking it, commit the
// immutable request/operation reservation, check tenant/Session/profile ownership,
// and reject digest reuse or create after deletion intent. Delete must durably
// reserve its operation, prove no writer/retention conflict, and reject WS-owned
// storage. The callback's bind function durably pins each immutable reference
// before returning. Failures retain reservations/references for retry; absence
// after a bound create never permits reallocation. Get enforces disclosure.
// Production composition must supply a durable implementation, not a memory map.
type Operations interface {
	Do(context.Context, Command, func(context.Context, Reservation, func(string, string) error) error) error
}
type Handle struct {
	WorkspaceID                                      primitives.ID
	ProviderKind, WorkspaceReference, StateReference string
	State                                            string // PENDING, BOUND, DELETING, ABSENT; observations, not AR lifecycle
}
type Capabilities struct {
	Create, Delete, SingleWriter, SinglePodWriter bool
	Snapshot, Clone, Attach, Detach               bool
}

// Provider implements the storage lifecycle subset. Materializer and
// AttachmentReader remain the separate fenced attachment seam. Snapshot and
// generation publication are capability-gated, separate operations.
type Provider interface {
	Capabilities(context.Context) (Capabilities, error)
	Create(context.Context, CreateRequest) (Handle, error)
	Get(context.Context, primitives.ID, primitives.ID) (Handle, error)
	Delete(context.Context, primitives.ID, primitives.ID, Operation) (Handle, error)
}

func CreateDigest(r CreateRequest) string {
	r.Operation.Digest = ""
	b, _ := json.Marshal(r)
	return digest(b)
}
func DeleteDigest(tenant, id primitives.ID, op Operation) string {
	b, _ := json.Marshal([]string{"delete", string(tenant), string(id), string(op.ID)})
	return digest(b)
}
func digest(b []byte) string { h := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(h[:]) }
