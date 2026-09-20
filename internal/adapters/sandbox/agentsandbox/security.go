package agentsandbox

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	v1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
)

// InfrastructureVerification proves facts not established by PodSpec: the
// qualified guest/handler on the observed node, actual image manifest identity,
// effective network enforcement, process/cgroup/resource limits and mounted
// Workspace/Attempt fencing. It must use trusted observations, not agent reports.
// Missing or ambiguous evidence fails closed; a nil verifier cannot be used.
type InfrastructureVerification func(context.Context, sandbox.Binding, *v1.Pod) error

func NewSecureEffectiveVerifier(resolve BlueprintResolver, infrastructure InfrastructureVerification) (EffectiveVerifier, error) {
	if resolve == nil || infrastructure == nil {
		return nil, sandbox.ErrInvalid
	}
	return func(ctx context.Context, b sandbox.Binding, pod *v1.Pod) (sandbox.EffectiveFacts, error) {
		fail := sandbox.EffectiveFacts{}
		if pod == nil || pod.UID == "" || pod.Spec.NodeName == "" || pod.DeletionTimestamp != nil || pod.Status.Phase != v1.PodRunning || b.ProviderReference == "" || b.Request.Profile.IsolationClass != "microvm-strong" {
			return fail, sandbox.ErrIntegrity
		}
		expected, err := resolve(ctx, b.Request)
		if err != nil {
			return fail, sandbox.ErrIntegrity
		}
		desired := expected.PodTemplate.Spec
		actual := pod.Spec
		if actual.HostNetwork || actual.HostPID || actual.HostIPC || actual.ShareProcessNamespace != nil && *actual.ShareProcessNamespace || actual.AutomountServiceAccountToken == nil || *actual.AutomountServiceAccountToken || actual.EnableServiceLinks == nil || *actual.EnableServiceLinks || len(actual.InitContainers) != 0 || len(actual.EphemeralContainers) != 0 || len(actual.Containers) != 1 || len(desired.Containers) != 1 || actual.RuntimeClassName == nil || desired.RuntimeClassName == nil || *actual.RuntimeClassName != *desired.RuntimeClassName {
			return fail, sandbox.ErrIntegrity
		}
		if !safeSecurity(actual.SecurityContext, actual.Containers[0].SecurityContext) || !apiequality.Semantic.DeepEqual(actual.SecurityContext, desired.SecurityContext) || !maps.Equal(actual.NodeSelector, desired.NodeSelector) || !apiequality.Semantic.DeepEqual(actual.Volumes, desired.Volumes) || !apiequality.Semantic.DeepEqual(actual.ImagePullSecrets, desired.ImagePullSecrets) || actual.RestartPolicy != desired.RestartPolicy || !apiequality.Semantic.DeepEqual(actual.TerminationGracePeriodSeconds, desired.TerminationGracePeriodSeconds) || len(actual.HostAliases) != 0 || actual.DNSConfig != nil {
			return fail, sandbox.ErrIntegrity
		}
		dns := desired.DNSPolicy
		if dns == "" {
			dns = v1.DNSClusterFirst
		}
		if actual.DNSPolicy != dns {
			return fail, sandbox.ErrIntegrity
		}
		account := desired.ServiceAccountName
		if account == "" {
			account = "default"
		}
		if actual.ServiceAccountName != account {
			return fail, sandbox.ErrIntegrity
		}
		// Compare the entire workload container, allowing only harmless API defaults.
		observedContainer, wantedContainer := actual.Containers[0].DeepCopy(), desired.Containers[0].DeepCopy()
		defaultContainer(observedContainer)
		defaultContainer(wantedContainer)
		if !apiequality.Semantic.DeepEqual(observedContainer, wantedContainer) {
			return fail, sandbox.ErrIntegrity
		}
		for _, volume := range actual.Volumes {
			if volume.HostPath != nil || volume.CSI != nil || volume.Projected != nil || volume.Ephemeral != nil {
				return fail, sandbox.ErrIntegrity
			}
		}
		for key, value := range expected.PodTemplate.ObjectMeta.Labels {
			if pod.Labels[key] != value {
				return fail, sandbox.ErrIntegrity
			}
		}
		for key := range pod.Annotations {
			if strings.HasPrefix(key, "io.katacontainers.") || strings.HasPrefix(key, "io.containerd.") || strings.HasPrefix(key, "containerd.io/") || key == "k8s.v1.cni.cncf.io/networks" {
				return fail, sandbox.ErrIntegrity
			}
		}
		if len(pod.Status.ContainerStatuses) != 1 {
			return fail, sandbox.ErrIntegrity
		}
		status := pod.Status.ContainerStatuses[0]
		if status.Name != actual.Containers[0].Name || !status.Ready || status.State.Running == nil || status.ImageID == "" || status.ContainerID == "" {
			return fail, sandbox.ErrIntegrity
		}
		if err = infrastructure(ctx, b, pod.DeepCopy()); err != nil {
			return fail, sandbox.ErrIntegrity
		}
		resources, err := json.Marshal(b.Request.Profile.Resources)
		if err != nil {
			return fail, sandbox.ErrIntegrity
		}
		return sandbox.EffectiveFacts{IsolationClass: b.Request.Profile.IsolationClass, Image: b.Request.Runtime.Image, Architecture: b.Request.Runtime.Architecture, ResourceDigest: sandbox.Digest(resources), NetworkClass: b.Request.Profile.Network.Profile, AttachmentReference: b.Request.Workspace.Reference, Verified: true}, nil
	}, nil
}
func safeSecurity(p *v1.PodSecurityContext, c *v1.SecurityContext) bool {
	if p == nil || c == nil || p.RunAsNonRoot == nil || !*p.RunAsNonRoot || p.RunAsUser == nil || *p.RunAsUser <= 0 || p.RunAsGroup == nil || *p.RunAsGroup <= 0 || p.SeccompProfile == nil || p.SeccompProfile.Type != v1.SeccompProfileTypeRuntimeDefault {
		return false
	}
	if c.Privileged == nil || *c.Privileged || c.AllowPrivilegeEscalation == nil || *c.AllowPrivilegeEscalation || c.ReadOnlyRootFilesystem == nil || !*c.ReadOnlyRootFilesystem || c.RunAsNonRoot == nil || !*c.RunAsNonRoot || c.RunAsUser == nil || *c.RunAsUser <= 0 || c.RunAsGroup == nil || *c.RunAsGroup <= 0 || c.SeccompProfile == nil || c.SeccompProfile.Type != v1.SeccompProfileTypeRuntimeDefault || c.Capabilities == nil || len(c.Capabilities.Add) != 0 || len(c.Capabilities.Drop) != 1 || c.Capabilities.Drop[0] != "ALL" || c.ProcMount != nil && *c.ProcMount != v1.DefaultProcMount {
		return false
	}
	return true
}
func defaultContainer(c *v1.Container) {
	if c.TerminationMessagePath == "" {
		c.TerminationMessagePath = "/dev/termination-log"
	}
	if c.TerminationMessagePolicy == "" {
		c.TerminationMessagePolicy = v1.TerminationMessageReadFile
	}
}

// secureBlueprint blocks unsafe desired compute before reservation/API mutation,
// independently of whether a later READY verifier has been configured.
func secureBlueprint(r sandbox.AcquireRequest, p v1.PodSpec) bool {
	if p.HostNetwork || p.HostPID || p.HostIPC || p.ShareProcessNamespace != nil && *p.ShareProcessNamespace || p.AutomountServiceAccountToken == nil || *p.AutomountServiceAccountToken || p.EnableServiceLinks == nil || *p.EnableServiceLinks || p.RuntimeClassName == nil || !dnsName(*p.RuntimeClassName) || len(p.Containers) != 1 || len(p.InitContainers) != 0 || len(p.EphemeralContainers) != 0 {
		return false
	}
	c := p.Containers[0]
	if !safeSecurity(p.SecurityContext, c.SecurityContext) || c.Image != r.Runtime.Image || !slices.Equal(c.Command, r.Runtime.Entrypoint) || len(c.Args) != 0 || len(c.Env) != 0 || len(c.EnvFrom) != 0 || len(c.VolumeDevices) != 0 {
		return false
	}
	if len(c.Resources.Requests) != 3 || len(c.Resources.Limits) != 3 || c.Resources.Requests.Cpu().MilliValue() <= 0 || c.Resources.Requests.Memory().Value() <= 0 || c.Resources.Requests.StorageEphemeral().Value() <= 0 {
		return false
	}
	if c.Resources.Requests.Cpu().MilliValue() != r.Profile.Resources.CPU.Request || c.Resources.Limits.Cpu().MilliValue() != r.Profile.Resources.CPU.Limit || c.Resources.Requests.Memory().Value() != r.Profile.Resources.Memory.Request || c.Resources.Limits.Memory().Value() != r.Profile.Resources.Memory.Limit || c.Resources.Requests.StorageEphemeral().Value() != r.Profile.Resources.EphemeralStorage.Request || c.Resources.Limits.StorageEphemeral().Value() != r.Profile.Resources.EphemeralStorage.Limit {
		return false
	}
	if len(p.Volumes) != 4 || len(c.VolumeMounts) != 4 {
		return false
	}
	sources := map[string]v1.VolumeSource{}
	for _, volume := range p.Volumes {
		if _, exists := sources[volume.Name]; exists {
			return false
		}
		sources[volume.Name] = volume.VolumeSource
		allowed := v1.VolumeSource{PersistentVolumeClaim: volume.PersistentVolumeClaim, Secret: volume.Secret, EmptyDir: volume.EmptyDir}
		if !apiequality.Semantic.DeepEqual(volume.VolumeSource, allowed) {
			return false
		}
		switch volume.Name {
		case "workspace", "state":
			if volume.PersistentVolumeClaim == nil || volume.PersistentVolumeClaim.ReadOnly || !dnsName(volume.PersistentVolumeClaim.ClaimName) || volume.Secret != nil || volume.EmptyDir != nil {
				return false
			}
		case "bootstrap":
			if volume.Secret == nil || !dnsName(volume.Secret.SecretName) || volume.PersistentVolumeClaim != nil || volume.EmptyDir != nil {
				return false
			}
		case "tmp":
			if volume.EmptyDir == nil || volume.EmptyDir.SizeLimit == nil || volume.EmptyDir.SizeLimit.Value() <= 0 || volume.EmptyDir.SizeLimit.Value() > r.Profile.Resources.EphemeralStorage.Limit || volume.Secret != nil || volume.PersistentVolumeClaim != nil {
				return false
			}
		default:
			return false
		}
	}
	seen := map[string]bool{}
	for _, mount := range c.VolumeMounts {
		root := map[string]string{"workspace": "/workspace", "state": "/state", "tmp": "/tmp", "bootstrap": "/run/thinkpixel/bootstrap"}[mount.Name]
		if root == "" || mount.MountPath != root || mount.SubPath != "" || mount.SubPathExpr != "" || mount.MountPropagation != nil && *mount.MountPropagation != v1.MountPropagationNone || mount.ReadOnly != (mount.Name == "bootstrap") || seen[mount.Name] {
			return false
		}
		seen[mount.Name] = true
	}
	return sources["workspace"].PersistentVolumeClaim.ClaimName != sources["state"].PersistentVolumeClaim.ClaimName
}
