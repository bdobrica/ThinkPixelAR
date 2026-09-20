package kubernetes

import (
	"context"
	"strings"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
)

// VolumeNames are adapter-local Kubernetes inputs, never domain/API fields.
type VolumeNames struct{ WorkspaceClaim, StateClaim string }

// StorageQualification verifies the immutable storage mapping and independent
// encryption/snapshot/access/capacity evidence. A PVC's Bound condition alone
// cannot establish those guarantees. It may not mutate storage or grant access.
type StorageQualification func(context.Context, workspace.Attachment, runtimeprofile.Profile) error
type AttachmentResolver struct {
	client    dynamic.Interface
	namespace string
	qualify   StorageQualification
}

func NewAttachmentResolver(client dynamic.Interface, namespace string, qualify StorageQualification) (*AttachmentResolver, error) {
	if client == nil || qualify == nil || len(validation.IsDNS1123Label(namespace)) != 0 || namespace == "" {
		return nil, sandbox.ErrInvalid
	}
	return &AttachmentResolver{client: client, namespace: namespace, qualify: qualify}, nil
}

// Resolve inspects only exact UID-bound existing claims. Creation, source
// materialization, snapshot, detach and deletion remain Workspace responsibilities.
func (r *AttachmentResolver) Resolve(ctx context.Context, a workspace.Attachment, p runtimeprofile.Profile) (VolumeNames, error) {
	fail := VolumeNames{}
	if (a.State != "PREPARED" && a.State != "ATTACHED") || a.ProviderKind != "kubernetes" || a.MountPath != "/workspace" || a.ReadOnly || a.StorageProfileReference != p.Implementation.StorageProfileRef || a.CapacityBytes != p.Storage.WorkspaceBytes || a.StateCapacityBytes <= 0 || a.StateCapacityBytes > p.Storage.WorkspaceBytes || a.AccessMode != p.Storage.AccessMode || p.Storage.EncryptionRequired && !a.Encrypted || p.Storage.SnapshotClass == "required" && !a.SnapshotCapable {
		return fail, sandbox.ErrIntegrity
	}
	if err := r.qualify(ctx, a, p); err != nil {
		return fail, sandbox.ErrUnsupported
	}
	names := VolumeNames{}
	for i, ref := range []string{a.WorkspaceVolumeReference, a.StateVolumeReference} {
		parts := strings.Split(ref, "/")
		if len(parts) != 3 || parts[0] != r.namespace || len(validation.IsDNS1123Subdomain(parts[1])) != 0 || parts[1] == "" || parts[2] == "" {
			return fail, sandbox.ErrIntegrity
		}
		obj, err := r.client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}).Namespace(r.namespace).Get(ctx, parts[1], metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return fail, sandbox.ErrNotFound
			}
			return fail, sandbox.ErrUnavailable
		}
		var pvc v1.PersistentVolumeClaim
		if runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &pvc) != nil || string(pvc.UID) != parts[2] || pvc.Namespace != r.namespace || pvc.DeletionTimestamp != nil || pvc.Status.Phase != v1.ClaimBound || pvc.Spec.VolumeName == "" || pvc.Spec.VolumeMode != nil && *pvc.Spec.VolumeMode != v1.PersistentVolumeFilesystem {
			return fail, sandbox.ErrIntegrity
		}
		mode := v1.ReadWriteOnce
		if a.AccessMode == "single-pod-writer" {
			mode = v1.ReadWriteOncePod
		} else if a.AccessMode != "single-writer" {
			return fail, sandbox.ErrUnsupported
		}
		if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != mode {
			return fail, sandbox.ErrIntegrity
		}
		capacity := a.CapacityBytes
		if i == 1 {
			capacity = a.StateCapacityBytes
		}
		// Both allocation and actual bound capacity must match the durable reservation.
		if pvc.Spec.Resources.Requests.Storage().Value() != capacity || pvc.Status.Capacity.Storage().Value() != capacity {
			return fail, sandbox.ErrIntegrity
		}
		if i == 0 {
			names.WorkspaceClaim = pvc.Name
		} else {
			names.StateClaim = pvc.Name
		}
	}
	if names.WorkspaceClaim == names.StateClaim {
		return fail, sandbox.ErrIntegrity
	}
	return names, nil
}
