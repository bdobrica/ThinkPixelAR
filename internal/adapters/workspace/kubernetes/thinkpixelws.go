package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"k8s.io/apimachinery/pkg/util/validation"
)

// StorageBindingLookup resolves the saved WS Materialization handle for this
// exact reserved attachment. The trusted implementation must verify current AG
// authority, whole-Workspace component scope, target/audience/expiry and WS writer
// lease/fence, and fail closed on uncertainty. It returns the JSON encoding of
// ThinkPixelWS api/storagebinding.Binding, not the public WorkspaceBinding or a
// handle supplied by the workload. It must not allocate storage or renew leases.
// This callback is separate from AttachmentReader's AR Attempt/writer fencing.
type StorageBindingLookup func(context.Context, workspace.Attachment) ([]byte, error)

// WSVolumeResolver connects WS's versioned storage instructions to AR's existing
// PVC verifier. The reservation must already pin the expected Workspace PVC and
// a distinct vendor-state PVC. WS storage never acquires a Sandbox ownerReference.
type WSVolumeResolver struct {
	volumes *AttachmentResolver
	lookup  StorageBindingLookup
}

func NewWSVolumeResolver(volumes *AttachmentResolver, lookup StorageBindingLookup) (*WSVolumeResolver, error) {
	if volumes == nil || lookup == nil {
		return nil, sandbox.ErrInvalid
	}
	return &WSVolumeResolver{volumes: volumes, lookup: lookup}, nil
}

// Local wire types intentionally depend only on the published versioned WS
// contract, never on WS internal packages or its database.
type wsStorageBinding struct {
	Handle    string          `json:"handle"`
	Kind      string          `json:"kind"`
	Reference json.RawMessage `json:"reference"`
}
type wsPVCBinding struct {
	Namespace string `json:"namespace"`
	ClaimName string `json:"claimName"`
	ClaimUID  string `json:"claimUid"`
	MountPath string `json:"mountPath"`
	ReadOnly  *bool  `json:"readOnly"`
}

func (r *WSVolumeResolver) Resolve(ctx context.Context, a workspace.Attachment, p runtimeprofile.Profile) (VolumeNames, error) {
	fail := VolumeNames{}
	if err := ctx.Err(); err != nil {
		return fail, err
	}
	raw, err := r.lookup(ctx, a)
	if err != nil {
		return fail, sandbox.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return fail, err
	}
	var b wsStorageBinding
	var pvc wsPVCBinding
	if len(raw) > 16384 || decodeWSBinding(raw, &b) != nil || b.Kind != "kubernetes-pvc-v1" || decodeWSBinding(b.Reference, &pvc) != nil {
		return fail, sandbox.ErrIntegrity
	}
	if pvc.Namespace != r.volumes.namespace || pvc.ClaimName == "" || len(validation.IsDNS1123Subdomain(pvc.ClaimName)) != 0 || pvc.ClaimUID == "" || strings.ContainsAny(pvc.ClaimUID, "/\t\r\n ") || b.Handle != "k8s-pvc-v1:"+pvc.ClaimUID || pvc.MountPath != "/workspace" || pvc.MountPath != a.MountPath || pvc.ReadOnly == nil || *pvc.ReadOnly != a.ReadOnly {
		return fail, sandbox.ErrIntegrity
	}
	// Never replace an already reserved identity with a newly resolved claim. The
	// ordinary resolver then observes the live UID, capacity, access and profile.
	if a.WorkspaceVolumeReference != pvc.Namespace+"/"+pvc.ClaimName+"/"+pvc.ClaimUID {
		return fail, sandbox.ErrIntegrity
	}
	return r.volumes.Resolve(ctx, a, p)
}

func decodeWSBinding(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return sandbox.ErrIntegrity
	}
	return nil
}
