package session

import (
	"context"
	"encoding/json"
	"math"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// SuspendRequest selects an already published, quiesced durable boundary.
// Caller identity is supplied by trusted authentication, never by a harness.
type SuspendRequest struct {
	Caller          Caller        `json:"caller"`
	SessionID       primitives.ID `json:"session_id"`
	OperationID     primitives.ID `json:"operation_id"`
	CheckpointID    primitives.ID `json:"checkpoint_id"`
	ExpectedVersion int64         `json:"expected_version"`
}

type SuspendResult struct {
	OperationID  primitives.ID `json:"operation_id"`
	SessionID    primitives.ID `json:"session_id"`
	CheckpointID primitives.ID `json:"checkpoint_id"`
	State        string        `json:"state"`
	Version      int64         `json:"state_version"`
	Generation   int64         `json:"execution_generation"`
}

type Suspender interface {
	Suspend(context.Context, SuspendRequest) (SuspendResult, error)
}

func (r SuspendRequest) Validate() error {
	for _, id := range []primitives.ID{r.Caller.TenantID, r.SessionID, r.OperationID, r.CheckpointID} {
		if _, err := primitives.ParseID(string(id)); err != nil {
			return workspace.ErrInvalid
		}
	}
	if !digestPattern.MatchString(r.Caller.PrincipalDigest) || r.ExpectedVersion < 0 || r.ExpectedVersion == math.MaxInt64 {
		return workspace.ErrInvalid
	}
	return nil
}

func (r SuspendRequest) Digest() string {
	raw, _ := json.Marshal(r)
	return sandbox.Digest(append([]byte("thinkpixel.session-suspend/v1\x00"), raw...))
}
