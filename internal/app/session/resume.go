package session

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	checkpoint "github.com/bdobrica/ThinkPixelAR/internal/app/checkpoint"
	domain "github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// ResumeRequest is supplied by trusted authentication, never restored content.
type ResumeRequest SuspendRequest

func (r ResumeRequest) Validate() error {
	if err := SuspendRequest(r).Validate(); err != nil {
		return err
	}
	if r.ExpectedVersion > math.MaxInt64-2 {
		return workspace.ErrInvalid
	}
	return nil
}
func (r ResumeRequest) Digest() string {
	raw, _ := json.Marshal(r)
	return sandbox.Digest(append([]byte("thinkpixel.session-resume/v1\x00"), raw...))
}

// ResumeIntent is a durable infrastructure reservation, not an Execution or grant.
// BootstrapID reserves a fresh infrastructure identity. Execution Attempts
// retain their existing, separate authority and admission fences.
type ResumeIntent struct {
	Request                                           ResumeRequest
	SandboxID, BootstrapID, AttachmentID              primitives.ID
	WorkspaceID, WorkspaceGenerationID                primitives.ID
	WorkspaceGeneration, ExecutionGeneration, Version int64
	Runtime                                           domain.RuntimeBinding
	Manifest                                          []byte
	Deadline                                          time.Time
	TargetState                                       string
}

func (i ResumeIntent) Digest() string { raw, _ := json.Marshal(i); return sandbox.Digest(raw) }

type ResumeResult struct {
	OperationID, SessionID, CheckpointID primitives.ID
	SandboxID, BootstrapID, AttachmentID primitives.ID
	State                                string // RESUMING is operation progress; the Session remains SUSPENDED.
	Version, Generation                  int64
}

// ResumeObservation is untrusted until independently verified by the store's
// trusted readiness adapter. References must identify exact provider UIDs.
type ResumeObservation struct {
	SandboxReference    string
	AttachmentReference string
	EvidenceDigest      string
}

func (o ResumeObservation) Validate() error {
	if len(o.SandboxReference) == 0 || len(o.SandboxReference) > 2048 || len(o.AttachmentReference) == 0 || len(o.AttachmentReference) > 2048 || !digestPattern.MatchString(o.EvidenceDigest) {
		return workspace.ErrInvalid
	}
	return nil
}

// ResumeMaterializer must reconcile the same persisted candidate on every call,
// including concurrent calls and ambiguous timeouts. It restores the exact pinned
// generation/vendor objects using fresh bootstrap identity, no Execution secrets,
// and no forward work. It must check current operation fences before mutations.
// Cleanup fences creation first, discovers by exact intent identity/ownership,
// deletes only candidate compute/bootstrap/attachment, and proves absence; it must
// never delete checkpoint or Workspace data. Unknown outcomes return an error.
type ResumeMaterializer interface {
	Reconcile(context.Context, ResumeIntent) (ResumeObservation, error)
	Cleanup(context.Context, ResumeIntent) error
}
type ResumeStore interface {
	Prepare(context.Context, ResumeRequest) (ResumeIntent, ResumeResult, error)
	Complete(context.Context, ResumeRequest, ResumeObservation) (ResumeResult, error)
	Fail(context.Context, ResumeRequest) (ResumeResult, error)
	CleanupIntent(context.Context, primitives.ID, primitives.ID) (ResumeIntent, bool, error)
	Cleaned(context.Context, primitives.ID, primitives.ID) error
}
type Resumer struct {
	store        ResumeStore
	materializer ResumeMaterializer
}

func NewResumer(store ResumeStore, materializer ResumeMaterializer) (*Resumer, error) {
	if store == nil || materializer == nil {
		return nil, workspace.ErrInvalid
	}
	return &Resumer{store, materializer}, nil
}

// Resume performs one bounded reconciliation. A lost response is retried with
// the identical request, never a new candidate. Provider calls occur after commit.
func (s *Resumer) Resume(ctx context.Context, r ResumeRequest) (ResumeResult, error) {
	// Materialization and independent readiness each have a one-minute budget.
	// Retain a finite outer budget that permits both on the live homelab lane.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	i, result, err := s.store.Prepare(ctx, r)
	if err != nil {
		if i.SandboxID != "" && resumeTerminalError(err) {
			failed, e := s.store.Fail(ctx, r)
			if e == nil {
				return failed, nil
			}
		}
		return result, err
	}
	if result.State != "RESUMING" {
		return result, nil
	}
	if !time.Now().Before(i.Deadline) {
		return s.store.Fail(ctx, r)
	}
	observed, err := s.materializer.Reconcile(ctx, i)
	if err != nil {
		if errors.Is(err, workspace.ErrIntegrity) {
			return s.store.Fail(ctx, r)
		}
		return result, workspace.ErrUnavailable // Ambiguous: retain the exact candidate.
	}
	result, err = s.store.Complete(ctx, r, observed)
	if resumeTerminalError(err) {
		return s.store.Fail(ctx, r)
	}
	return result, err
}

// Cleanup is a trusted worker operation driven by persisted failure intent;
// expired caller authority cannot prevent cleanup of an abandoned candidate.
func (s *Resumer) Cleanup(ctx context.Context, tenant, operation primitives.ID) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	i, done, err := s.store.CleanupIntent(ctx, tenant, operation)
	if err != nil || done {
		return err
	}
	if s.materializer.Cleanup(ctx, i) != nil {
		return workspace.ErrUnavailable
	}
	return s.store.Cleaned(ctx, tenant, operation)
}

func resumeTerminalError(err error) bool {
	return errors.Is(err, workspace.ErrIntegrity) || errors.Is(err, workspace.ErrConflict) || errors.Is(err, checkpoint.ErrInvalidRestore) || errors.Is(err, checkpoint.ErrIncompatible)
}
