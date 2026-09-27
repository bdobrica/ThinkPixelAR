// Package checkpoint defines trusted durable checkpoint publication.
package checkpoint

import (
	"context"
	"encoding/json"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/checkpoint"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type VersionedName struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type VendorObject struct {
	ID             string        `json:"object_id"`
	Reference      string        `json:"object_ref"`
	MediaType      string        `json:"media_type"`
	Format         VersionedName `json:"state_format"`
	Algorithm      string        `json:"digest_algorithm"`
	Digest         string        `json:"digest"`
	Size           int64         `json:"size_bytes"`
	Classification string        `json:"classification"`
	Role           string        `json:"role"`
}

// Request is a stable operation. Boundary identifies the already committed WSP-003
// generation. Binding and objects are candidates until independently verified.
type Request struct {
	ID          primitives.ID
	Boundary    workspace.CheckpointRequest
	Binding     checkpoint.Binding
	VendorState []VendorObject
}

func (r Request) Digest() string { raw, _ := json.Marshal(r); return workspace.EvidenceDigest(raw) }

type Result struct {
	ID       primitives.ID
	Manifest []byte
}
type Publisher interface {
	Publish(context.Context, Request) (Result, error)
}
