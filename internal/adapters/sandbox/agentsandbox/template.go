package agentsandbox

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"

	"github.com/bdobrica/ThinkPixelAR/internal/config/runtimeprofiles"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	core "sigs.k8s.io/agent-sandbox/api/v1beta1"
	extensions "sigs.k8s.io/agent-sandbox/extensions/api/v1beta1"
)

// CodingTemplateConfig is trusted adapter configuration, never workload input.
// QualificationDigest identifies externally reviewed runtime, node/process,
// storage and network evidence. ValidateQualification must check that evidence;
// merely setting the digest is not sufficient to admit a profile.
type CodingTemplateConfig struct {
	References          runtimeprofile.Implementation `json:"references"`
	RuntimeClass        string                        `json:"runtime_class"`
	NodeSelector        map[string]string             `json:"node_selector"`
	UserID              int64                         `json:"user_id"`
	GroupID             int64                         `json:"group_id"`
	TempBytes           int64                         `json:"temp_bytes"`
	QualificationDigest string                        `json:"qualification_digest"`
}
type ValidateQualification func(runtimeprofile.Profile, CodingTemplateConfig) error

// CodingVolumes contains names resolved by trusted Workspace/bootstrap adapters.
// The corresponding references must match the persisted acquisition request.
// Rendering never creates PVCs, reads Secret contents or grants storage ownership.
type CodingVolumes struct {
	AttachmentReference string
	BootstrapReference  string
	WorkspaceClaim      string
	StateClaim          string
	BootstrapSecret     string
}

type CodingTemplate struct {
	registry *runtimeprofiles.Registry
	config   CodingTemplateConfig
	name     string
}

func NewCodingTemplate(document []byte, config CodingTemplateConfig, qualify ValidateQualification) (*CodingTemplate, error) {
	if qualify == nil || !dnsName(config.RuntimeClass) || config.UserID <= 0 || config.GroupID <= 0 || config.TempBytes <= 0 || !shaDigest.MatchString(config.QualificationDigest) {
		return nil, sandbox.ErrInvalid
	}
	config.NodeSelector = maps.Clone(config.NodeSelector)
	if len(config.NodeSelector) == 0 {
		return nil, sandbox.ErrInvalid
	}
	for key, value := range config.NodeSelector {
		if len(validation.IsQualifiedName(key)) != 0 || len(validation.IsValidLabelValue(value)) != 0 {
			return nil, sandbox.ErrInvalid
		}
	}
	registry, err := runtimeprofiles.New()
	if err != nil {
		return nil, sandbox.ErrInvalid
	}
	var name string
	err = registry.Reload([][]byte{document}, func(p runtimeprofile.Profile) ([]byte, error) {
		if p.Implementation != config.References || p.Implementation.ProviderKind != "kubernetes-agent-sandbox" || p.IsolationClass != "microvm-strong" || p.Platform.OS != "linux" || p.Platform.GPUAllowed || p.Resources.GPU.Count != 0 || p.Security.SeccompClass != "runtime-default" || p.Lifecycle.WarmPoolEligible || p.Storage.WorkspaceMount != "/workspace" || p.Storage.VendorStateRoot != "/state" || config.TempBytes > p.Resources.EphemeralStorage.Limit {
			return nil, sandbox.ErrUnsupported
		}
		// Pass independent values: qualification may not mutate the saved mapping.
		copyConfig := config
		copyConfig.NodeSelector = maps.Clone(config.NodeSelector)
		raw, e := json.Marshal(p)
		if e != nil {
			return nil, sandbox.ErrInvalid
		}
		var copyProfile runtimeprofile.Profile
		if json.Unmarshal(raw, &copyProfile) != nil || qualify(copyProfile, copyConfig) != nil {
			return nil, sandbox.ErrUnsupported
		}
		name = p.Name
		return json.Marshal(config)
	})
	if err != nil {
		return nil, sandbox.ErrUnsupported
	}
	return &CodingTemplate{registry: registry, config: config, name: name}, nil
}

// Resolution returns independent snapshots for authoritative persistence before
// Acquire. It does not intersect Run authority; admission must do that first.
func (t *CodingTemplate) Resolution() (runtimeprofile.Profile, []byte, string, []byte, string) {
	p, raw, digest, implementation, implementationDigest, _ := t.registry.Lookup(t.name)
	return p, raw, digest, implementation, implementationDigest
}

// Render is pure and deterministic. Neither a template nor successful rendering
// is effective-state evidence. The provider's separate verification gate applies.
func (t *CodingTemplate) Render(r sandbox.AcquireRequest, volumes CodingVolumes) (*extensions.SandboxTemplate, error) {
	p, _, digest, _, implementationDigest := t.Resolution()
	if !validAcquire(r) || r.ProfileDigest != digest || r.ImplementationDigest != implementationDigest || !reflect.DeepEqual(r.Profile, p) {
		return nil, sandbox.ErrConflict
	}
	if !slices.Contains(p.Platform.Architectures, r.Runtime.Architecture) {
		return nil, sandbox.ErrUnsupported
	}
	if volumes.AttachmentReference != r.Workspace.Reference || volumes.BootstrapReference != r.BootstrapReference || !dnsName(volumes.WorkspaceClaim) || !dnsName(volumes.StateClaim) || !dnsName(volumes.BootstrapSecret) || volumes.StateClaim == volumes.WorkspaceClaim || r.Workspace.MountPath != p.Storage.WorkspaceMount || r.Workspace.ReadOnly {
		return nil, sandbox.ErrIntegrity
	}
	selectors := maps.Clone(t.config.NodeSelector)
	for key, value := range map[string]string{"kubernetes.io/os": "linux", "kubernetes.io/arch": r.Runtime.Architecture} {
		if old, ok := selectors[key]; ok && old != value {
			return nil, sandbox.ErrUnsupported
		}
		selectors[key] = value
	}
	f, tr := false, true
	uid, gid, grace, mode := t.config.UserID, t.config.GroupID, p.Lifecycle.TerminationGraceSeconds, int32(0440)
	runtimeClass := t.config.RuntimeClass
	seccomp := &v1.SeccompProfile{Type: v1.SeccompProfileTypeRuntimeDefault}
	resources := func(limit bool) v1.ResourceList {
		cpu, memory, ephemeral := p.Resources.CPU.Request, p.Resources.Memory.Request, p.Resources.EphemeralStorage.Request
		if limit {
			cpu, memory, ephemeral = p.Resources.CPU.Limit, p.Resources.Memory.Limit, p.Resources.EphemeralStorage.Limit
		}
		return v1.ResourceList{v1.ResourceCPU: *resource.NewMilliQuantity(cpu, resource.DecimalSI), v1.ResourceMemory: *resource.NewQuantity(memory, resource.BinarySI), v1.ResourceEphemeralStorage: *resource.NewQuantity(ephemeral, resource.BinarySI)}
	}
	labels := map[string]string{"thinkpixel.io/sandbox": string(r.Scope.SandboxID), "thinkpixel.io/attempt": string(r.Scope.AttemptID)}
	spec := v1.PodSpec{
		RuntimeClassName: &runtimeClass, NodeSelector: selectors, AutomountServiceAccountToken: &f, EnableServiceLinks: &f,
		RestartPolicy: v1.RestartPolicyNever, TerminationGracePeriodSeconds: &grace,
		SecurityContext: &v1.PodSecurityContext{RunAsNonRoot: &tr, RunAsUser: &uid, RunAsGroup: &gid, FSGroup: &gid, SeccompProfile: seccomp},
		Containers: []v1.Container{{Name: "agent", Image: r.Runtime.Image, ImagePullPolicy: v1.PullIfNotPresent, Command: slices.Clone(r.Runtime.Entrypoint), WorkingDir: "/workspace",
			SecurityContext: &v1.SecurityContext{Privileged: &f, AllowPrivilegeEscalation: &f, ReadOnlyRootFilesystem: &tr, RunAsNonRoot: &tr, RunAsUser: &uid, RunAsGroup: &gid, Capabilities: &v1.Capabilities{Drop: []v1.Capability{"ALL"}}, SeccompProfile: seccomp},
			Resources:       v1.ResourceRequirements{Requests: resources(false), Limits: resources(true)},
			VolumeMounts:    []v1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}, {Name: "state", MountPath: "/state"}, {Name: "tmp", MountPath: "/tmp"}, {Name: "bootstrap", MountPath: "/run/thinkpixel/bootstrap", ReadOnly: true}},
		}},
		Volumes: []v1.Volume{
			{Name: "workspace", VolumeSource: v1.VolumeSource{PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{ClaimName: volumes.WorkspaceClaim}}},
			{Name: "state", VolumeSource: v1.VolumeSource{PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{ClaimName: volumes.StateClaim}}},
			{Name: "tmp", VolumeSource: v1.VolumeSource{EmptyDir: &v1.EmptyDirVolumeSource{SizeLimit: resource.NewQuantity(t.config.TempBytes, resource.BinarySI)}}},
			{Name: "bootstrap", VolumeSource: v1.VolumeSource{Secret: &v1.SecretVolumeSource{SecretName: volumes.BootstrapSecret, DefaultMode: &mode, Optional: &f}}},
		},
	}
	// The explicit empty policy avoids upstream's public-Internet default. Exact
	// platform/profile exceptions are owned by the separate network enforcer.
	return &extensions.SandboxTemplate{TypeMeta: metav1.TypeMeta{APIVersion: extensions.GroupVersion.String(), Kind: "SandboxTemplate"}, ObjectMeta: metav1.ObjectMeta{Name: "ar-" + string(r.Scope.SandboxID)}, Spec: extensions.SandboxTemplateSpec{
		SandboxBlueprint:        core.SandboxBlueprint{Service: &f, PodTemplate: core.PodTemplate{ObjectMeta: core.PodMetadata{Labels: labels}, Spec: spec}},
		NetworkPolicyManagement: extensions.NetworkPolicyManagementManaged, NetworkPolicy: &extensions.NetworkPolicySpec{}, EnvVarsInjectionPolicy: extensions.EnvVarsInjectionPolicyDisallowed, VolumeClaimTemplatesPolicy: extensions.VolumeClaimTemplatesPolicyDisallowed,
	}}, nil
}
func dnsName(s string) bool { return len(s) > 0 && len(validation.IsDNS1123Subdomain(s)) == 0 }
