package workspace

import (
	"context"
	"encoding/json"
	"math"
	"regexp"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// CheckpointRequest identifies a trusted, quiesced standalone Workspace boundary.
// Operation.ID also identifies the resulting generation and publication event.
// An empty ExecutionID requires an idle Session with no current writer.
type CheckpointRequest struct {
	TenantID, SessionID, WorkspaceID     primitives.ID
	Operation                            Operation
	ParentID                             primitives.ID
	ParentGeneration, SessionGeneration  int64
	ExecutionID, AttemptID, AttachmentID primitives.ID
	ConfigurationDigest                  string
}

type CheckpointProof struct {
	SnapshotReference                                 string
	IntegrityAlgorithm, IntegrityRoot, ManifestDigest string
	LogicalBytes, LogicalFiles                        int64
	// Evidence must cover Workspace AND vendor-state durability, source identity,
	// consistency, configuration, and runtime compatibility. No credentials.
	Evidence json.RawMessage
}

type CheckpointResult struct {
	GenerationID primitives.ID
	Generation   int64
	State        string // PREPARED, COMMITTED, or ABORTED
}

type CheckpointPublisher interface {
	Prepare(context.Context, CheckpointRequest) (CheckpointResult, error)
	Publish(context.Context, CheckpointRequest, CheckpointProof) (CheckpointResult, error)
	Abort(context.Context, CheckpointRequest) error
}

func CheckpointDigest(r CheckpointRequest) string {
	r.Operation.Digest = ""
	b, _ := json.Marshal(r)
	return EvidenceDigest(b)
}

func (r CheckpointRequest) Validate() error {
	for _, id := range []primitives.ID{r.TenantID, r.SessionID, r.WorkspaceID, r.Operation.ID, r.ParentID} {
		if _, err := primitives.ParseID(string(id)); err != nil {
			return ErrInvalid
		}
	}
	if r.ParentGeneration < 0 || r.ParentGeneration == math.MaxInt64 || r.SessionGeneration < 0 || !checkpointDigest.MatchString(r.ConfigurationDigest) || r.Operation.Digest != CheckpointDigest(r) {
		return ErrInvalid
	}
	if r.ExecutionID == "" {
		if r.AttemptID != "" || r.AttachmentID != "" {
			return ErrInvalid
		}
	} else {
		if r.SessionGeneration == 0 {
			return ErrInvalid
		}
		for _, id := range []primitives.ID{r.ExecutionID, r.AttemptID, r.AttachmentID} {
			if _, err := primitives.ParseID(string(id)); err != nil {
				return ErrInvalid
			}
		}
	}
	return nil
}

var checkpointDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (p CheckpointProof) Validate() error {
	var object map[string]json.RawMessage
	if p.SnapshotReference == "" || len(p.SnapshotReference) > 2048 || p.IntegrityAlgorithm == "" || len(p.IntegrityAlgorithm) > 255 || !checkpointDigest.MatchString(p.IntegrityRoot) || !checkpointDigest.MatchString(p.ManifestDigest) || p.LogicalBytes < 0 || p.LogicalFiles < 0 || len(p.Evidence) > 32768 || json.Unmarshal(p.Evidence, &object) != nil || len(object) == 0 {
		return ErrInvalid
	}
	return nil
}
