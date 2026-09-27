package kubernetes

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
)

// EmptyConfig is operator-approved and credential-free. The image must provide
// /bin/sh, find and sync. Qualification must cover the image, runtime, namespace
// network isolation, UID/GID and storage topology, independently of Pod status.
type EmptyConfig struct {
	Image, RuntimeClass string
	UID, GID            int64
}
type QualifyEmpty func(context.Context, EmptyConfig) error
type EmptyInitializer struct {
	provider *Provider
	config   EmptyConfig
	qualify  QualifyEmpty
}

var podsResource = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

func NewEmptyInitializer(p *Provider, c EmptyConfig, q QualifyEmpty) (*EmptyInitializer, error) {
	parts := strings.Split(c.Image, "@")
	if p == nil || q == nil || len(parts) != 2 || parts[0] == "" || !storageDigest.MatchString(parts[1]) || c.RuntimeClass == "" || len(validation.IsDNS1123Subdomain(c.RuntimeClass)) != 0 || c.UID <= 0 || c.GID <= 0 {
		return nil, workspace.ErrInvalid
	}
	return &EmptyInitializer{p, c, q}, nil
}
func (i *EmptyInitializer) InitializeEmpty(ctx context.Context, r workspace.CreateRequest) (bool, error) {
	if !validStorageRequest(r) {
		return false, workspace.ErrInvalid
	}
	p := i.provider
	ready := false
	err := p.operations.Do(ctx, workspace.Command{Kind: "initialize", TenantID: r.TenantID, WorkspaceID: r.WorkspaceID, Operation: r.Operation, Create: r}, func(ctx context.Context, s workspace.Reservation, bind func(string, string) error) error {
		if s.Request != r || s.WorkspaceReference == "" || s.StateReference == "" {
			return workspace.ErrIntegrity
		}
		if s.Ready {
			ready = true
			return nil
		}
		if p.qualify(ctx, p.config) != nil || i.qualify(ctx, i.config) != nil || r.StorageProfile != p.config.StorageProfile || r.ConfigurationDigest != p.config.ConfigurationDigest || r.EncryptionRequired && !p.config.Encrypted || r.AccessMode == "single-pod-writer" && !p.config.SinglePodWriter {
			return workspace.ErrUnsupported
		}
		names := []string{}
		bound := true
		for n, ref := range []string{s.WorkspaceReference, s.StateReference} {
			role := "workspace"
			capacity := r.CapacityBytes
			if n == 1 {
				role = "state"
				capacity = r.StateCapacityBytes
			}
			name := "ar-" + role + "-" + string(r.WorkspaceID)
			obj, err := p.client.Resource(claimsResource).Namespace(p.config.Namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return storageError(err)
			}
			var claim v1.PersistentVolumeClaim
			if runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &claim) != nil || !p.matches(&claim, r, role, name, capacity) || ref != p.config.Namespace+"/"+name+"/"+string(claim.UID) || claim.DeletionTimestamp != nil || claim.Status.Phase == v1.ClaimLost {
				return workspace.ErrIntegrity
			}
			if claim.Status.Phase != v1.ClaimBound {
				bound = false
			} else if claim.Spec.VolumeName == "" || claim.Status.Capacity.Storage().Value() != capacity {
				return workspace.ErrIntegrity
			}
			names = append(names, name)
		}
		want := i.pod(r, names)
		raw, _ := json.Marshal(want.Spec)
		specDigest := workspace.EvidenceDigest(raw)
		want.Annotations["thinkpixel.io/initializer-spec"] = specDigest
		if s.InitializerSpec != "" && s.InitializerSpec != specDigest {
			return workspace.ErrConflict
		}
		if s.InitializerSpec == "" {
			if err := bind("initializer-spec", specDigest); err != nil {
				return err
			}
		}
		api := p.client.Resource(podsResource).Namespace(p.config.Namespace)
		obj, err := api.Get(ctx, want.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			if s.EmptyProof != nil {
				if s.EmptyProof.InitializerReference != s.InitializerReference || s.EmptyProof.SpecDigest != specDigest || s.EmptyProof.WorkspaceReference != s.WorkspaceReference || s.EmptyProof.StateReference != s.StateReference || !bound {
					return workspace.ErrIntegrity
				}
				if err = bind("empty-ready", "ready"); err != nil {
					return err
				}
				ready = true
				return nil
			}
			if s.InitializerReference != "" {
				return workspace.ErrIntegrity
			}
			raw, e := runtime.DefaultUnstructuredConverter.ToUnstructured(want)
			if e != nil {
				return workspace.ErrInvalid
			}
			obj, err = api.Create(ctx, &unstructured.Unstructured{Object: raw}, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(err) {
				obj, err = api.Get(ctx, want.Name, metav1.GetOptions{})
			}
		}
		if err != nil {
			return storageError(err)
		}
		var pod v1.Pod
		if runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &pod) != nil || !matchesEmptyPod(want, &pod) {
			return workspace.ErrIntegrity
		}
		ref := pod.Namespace + "/" + pod.Name + "/" + string(pod.UID)
		if s.InitializerReference != "" && s.InitializerReference != ref {
			return workspace.ErrIntegrity
		}
		if s.InitializerReference == "" {
			if err = bind("initializer", ref); err != nil {
				return err
			}
		}
		if pod.Status.Phase == v1.PodFailed {
			return workspace.ErrIntegrity
		}
		if s.EmptyProof == nil {
			if pod.DeletionTimestamp != nil {
				return workspace.ErrConflict
			}
			if pod.Status.Phase != v1.PodSucceeded {
				return nil
			}
			if !bound || pod.Spec.NodeName == "" || len(pod.Status.ContainerStatuses) != 1 {
				return workspace.ErrIntegrity
			}
			status := pod.Status.ContainerStatuses[0]
			if status.Name != "initialize" || status.State.Terminated == nil || status.State.Terminated.ExitCode != 0 || status.RestartCount != 0 {
				return workspace.ErrIntegrity
			}
			proof := workspace.EmptyProof{WorkspaceReference: s.WorkspaceReference, StateReference: s.StateReference, InitializerReference: ref, SpecDigest: specDigest}
			raw, _ := json.Marshal(proof)
			if err = bind("empty-proof", string(raw)); err != nil {
				return err
			}
		}
		// Persist success before deleting compute. Readiness waits for exact Pod absence,
		// allowing delayed binding without leaving an initialization writer mounted.
		if pod.DeletionTimestamp == nil {
			uid, rv := types.UID(pod.UID), pod.ResourceVersion
			if rv == "" {
				return workspace.ErrIntegrity
			}
			if err = api.Delete(ctx, pod.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}}); err != nil && !apierrors.IsNotFound(err) {
				return storageError(err)
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return ready, nil
}
func (i *EmptyInitializer) pod(r workspace.CreateRequest, names []string) *v1.Pod {
	no, yes := false, true
	deadline := int64(300)
	runtimeClass := i.config.RuntimeClass
	return &v1.Pod{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"}, ObjectMeta: metav1.ObjectMeta{Name: "ar-init-" + string(r.WorkspaceID), Namespace: i.provider.config.Namespace, Annotations: map[string]string{"thinkpixel.io/workspace": string(r.WorkspaceID), "thinkpixel.io/create-digest": r.Operation.Digest}}, Spec: v1.PodSpec{
		RuntimeClassName: &runtimeClass, RestartPolicy: v1.RestartPolicyNever, ActiveDeadlineSeconds: &deadline, AutomountServiceAccountToken: &no, EnableServiceLinks: &no,
		SecurityContext: &v1.PodSecurityContext{RunAsNonRoot: &yes, RunAsUser: &i.config.UID, RunAsGroup: &i.config.GID, FSGroup: &i.config.GID, SeccompProfile: &v1.SeccompProfile{Type: v1.SeccompProfileTypeRuntimeDefault}},
		Containers: []v1.Container{{Name: "initialize", Image: i.config.Image, ImagePullPolicy: v1.PullIfNotPresent, Command: []string{"/bin/sh", "-ec", `for dir in /workspace /state; do test -d "$dir" && test -w "$dir"; entries="$(find "$dir" -mindepth 1 -maxdepth 1 -print -quit)" || exit 1; test -z "$entries"; done; sync`},
			SecurityContext: &v1.SecurityContext{AllowPrivilegeEscalation: &no, ReadOnlyRootFilesystem: &yes, Capabilities: &v1.Capabilities{Drop: []v1.Capability{"ALL"}}}, Resources: v1.ResourceRequirements{Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("10m"), v1.ResourceMemory: resource.MustParse("16Mi")}, Limits: v1.ResourceList{v1.ResourceCPU: resource.MustParse("100m"), v1.ResourceMemory: resource.MustParse("32Mi")}}, VolumeMounts: []v1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}, {Name: "state", MountPath: "/state"}}}},
		Volumes: []v1.Volume{{Name: "workspace", VolumeSource: v1.VolumeSource{PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{ClaimName: names[0]}}}, {Name: "state", VolumeSource: v1.VolumeSource{PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{ClaimName: names[1]}}}},
	}}
}
func matchesEmptyPod(want, got *v1.Pod) bool {
	if got.Name != want.Name || got.Namespace != want.Namespace || got.UID == "" || len(got.OwnerReferences) != 0 || len(got.Spec.Containers) != 1 || len(got.Spec.InitContainers) != 0 || len(got.Spec.EphemeralContainers) != 0 || len(got.Spec.Volumes) != 2 || got.Spec.HostNetwork || got.Spec.HostPID || got.Spec.HostIPC {
		return false
	}
	for k, v := range want.Annotations {
		if got.Annotations[k] != v {
			return false
		}
	}
	c := got.Spec.Containers[0]
	if !equality.Semantic.DeepEqual(want.Spec.SecurityContext, got.Spec.SecurityContext) || !equality.Semantic.DeepEqual(want.Spec.Containers[0].SecurityContext, c.SecurityContext) || c.SecurityContext == nil || len(c.Args) != 0 || len(c.Ports) != 0 || c.LivenessProbe != nil || c.ReadinessProbe != nil || c.StartupProbe != nil || !equality.Semantic.DeepEqual(want.Spec.Volumes, got.Spec.Volumes) || !equality.Semantic.DeepEqual(want.Spec.Containers[0].VolumeMounts, c.VolumeMounts) || len(c.Env) != 0 || len(c.EnvFrom) != 0 || len(c.VolumeMounts) != 2 || len(c.VolumeDevices) != 0 || c.Lifecycle != nil || c.SecurityContext.Privileged != nil && *c.SecurityContext.Privileged {
		return false
	}
	// API-defaulted scheduling fields are allowed; required mounts, command,
	// resources and security settings must remain exactly as requested.
	return equality.Semantic.DeepDerivative(want.Spec, got.Spec)
}
