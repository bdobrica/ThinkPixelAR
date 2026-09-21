package agentsandbox

import (
	"context"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// ScratchQualification must independently prove the actual bounded backing and
// mount for these exact Pod/PVC/PV identities. API capacities are not that proof.
type ScratchQualification func(context.Context, *v1.Pod, *v1.PersistentVolumeClaim, *v1.PersistentVolume) error

// NewScratchVerifier supplies the scratch portion of InfrastructureVerification.
// It neither creates storage nor certifies unrelated runtime/Workspace facts.
func NewScratchVerifier(client dynamic.Interface, qualify ScratchQualification) (func(context.Context, *v1.Pod) error, error) {
	if client == nil || qualify == nil {
		return nil, sandbox.ErrInvalid
	}
	return func(ctx context.Context, pod *v1.Pod) error {
		if pod == nil || pod.UID == "" || pod.Namespace == "" || pod.Name == "" || pod.Spec.NodeName == "" {
			return sandbox.ErrIntegrity
		}
		var volume *v1.Volume
		for i := range pod.Spec.Volumes {
			if pod.Spec.Volumes[i].Name == "tmp" {
				if volume != nil {
					return sandbox.ErrIntegrity
				}
				volume = &pod.Spec.Volumes[i]
			}
		}
		if volume == nil || volume.Ephemeral == nil || !safeScratch(volume.VolumeSource, 1<<40) {
			return sandbox.ErrIntegrity
		}
		expected := volume.Ephemeral.VolumeClaimTemplate.Spec
		u, err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}).Namespace(pod.Namespace).Get(ctx, pod.Name+"-tmp", metav1.GetOptions{})
		if err != nil {
			return sandbox.ErrUnavailable
		}
		claim := &v1.PersistentVolumeClaim{}
		if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, claim) != nil || claim.UID == "" || claim.Name != pod.Name+"-tmp" || claim.Namespace != pod.Namespace || claim.DeletionTimestamp != nil || claim.Status.Phase != v1.ClaimBound || claim.Spec.VolumeName == "" || claim.Spec.StorageClassName == nil || *claim.Spec.StorageClassName != *expected.StorageClassName || claim.Spec.Resources.Requests.Storage().Cmp(*expected.Resources.Requests.Storage()) != 0 || claim.Status.Capacity.Storage().Cmp(*expected.Resources.Requests.Storage()) != 0 {
			return sandbox.ErrIntegrity
		}
		owner := metav1.GetControllerOf(claim)
		if owner == nil || owner.APIVersion != "v1" || owner.Kind != "Pod" || owner.Name != pod.Name || owner.UID != pod.UID {
			return sandbox.ErrIntegrity
		}
		// Generic ephemeral claims must be fresh filesystem claims, never snapshots
		// or alternate attachments. VolumeName is assigned only by the binding step.
		spec := claim.Spec.DeepCopy()
		spec.VolumeName = ""
		if !safeScratch(v1.VolumeSource{Ephemeral: &v1.EphemeralVolumeSource{VolumeClaimTemplate: &v1.PersistentVolumeClaimTemplate{Spec: *spec}}}, expected.Resources.Requests.Storage().Value()) {
			return sandbox.ErrIntegrity
		}
		u, err = client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}).Get(ctx, claim.Spec.VolumeName, metav1.GetOptions{})
		if err != nil {
			return sandbox.ErrUnavailable
		}
		pv := &v1.PersistentVolume{}
		if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, pv) != nil || pv.UID == "" || pv.Name != claim.Spec.VolumeName || pv.DeletionTimestamp != nil || pv.Status.Phase != v1.VolumeBound || pv.Spec.ClaimRef == nil || pv.Spec.ClaimRef.UID != claim.UID || pv.Spec.ClaimRef.Namespace != claim.Namespace || pv.Spec.ClaimRef.Name != claim.Name || pv.Spec.StorageClassName != *expected.StorageClassName || pv.Spec.Capacity.Storage().Cmp(*expected.Resources.Requests.Storage()) != 0 {
			return sandbox.ErrIntegrity
		}
		if qualify(ctx, pod.DeepCopy(), claim.DeepCopy(), pv.DeepCopy()) != nil {
			return sandbox.ErrIntegrity
		}
		return nil
	}, nil
}
