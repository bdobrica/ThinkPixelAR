package agentsandbox

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	v1 "k8s.io/api/core/v1"
	extensions "sigs.k8s.io/agent-sandbox/extensions/api/v1beta1"
)

func codingFixture(t *testing.T) (*CodingTemplate, sandbox.AcquireRequest, CodingVolumes) {
	t.Helper()
	raw, err := os.ReadFile("../../../../docs/profiles/coding-medium-secure.json")
	if err != nil {
		t.Fatal(err)
	}
	r := acquireFixture(t)
	cfg := CodingTemplateConfig{References: r.Profile.Implementation, RuntimeClass: "operator-kata", NodeSelector: map[string]string{"thinkpixel.io/pool": "qualified"}, UserID: 65532, GroupID: 65532, TempBytes: 1 << 30, QualificationDigest: "sha256:" + strings.Repeat("d", 64)}
	mapper, err := NewCodingTemplate(raw, cfg, func(runtimeprofile.Profile, CodingTemplateConfig) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	r.Profile, _, r.ProfileDigest, _, r.ImplementationDigest = mapper.Resolution()
	r.Workspace.MountPath = "/workspace"
	r.Operation.Digest, _ = RequestDigest(r)
	return mapper, r, CodingVolumes{AttachmentReference: r.Workspace.Reference, BootstrapReference: r.BootstrapReference, WorkspaceClaim: "workspace", StateClaim: "vendor-state", BootstrapSecret: "bootstrap"}
}
func TestCodingTemplateMapsSecureProfile(t *testing.T) {
	mapper, r, volumes := codingFixture(t)
	template, err := mapper.Render(r, volumes)
	if err != nil {
		t.Fatal(err)
	}
	pod := template.Spec.PodTemplate.Spec
	c := pod.Containers[0]
	if template.APIVersion != "extensions.agents.x-k8s.io/v1beta1" || template.Spec.Service == nil || *template.Spec.Service || len(template.Spec.VolumeClaimTemplates) != 0 {
		t.Fatal("unexpected API, service or PVC ownership")
	}
	if template.Spec.NetworkPolicyManagement != extensions.NetworkPolicyManagementManaged || template.Spec.NetworkPolicy == nil || len(template.Spec.NetworkPolicy.Egress) != 0 || len(template.Spec.NetworkPolicy.Ingress) != 0 || template.Spec.EnvVarsInjectionPolicy != extensions.EnvVarsInjectionPolicyDisallowed || template.Spec.VolumeClaimTemplatesPolicy != extensions.VolumeClaimTemplatesPolicyDisallowed {
		t.Fatal("permissive template policy")
	}
	if *pod.RuntimeClassName != "operator-kata" || pod.NodeSelector["kubernetes.io/arch"] != "amd64" || *pod.AutomountServiceAccountToken || pod.HostPID || pod.HostIPC || pod.HostNetwork || len(pod.InitContainers) != 0 {
		t.Fatal("unsafe pod settings")
	}
	if c.Image != r.Runtime.Image || !reflect.DeepEqual(c.Command, r.Runtime.Entrypoint) || c.Resources.Requests.Cpu().MilliValue() != 2000 || c.Resources.Limits.Cpu().MilliValue() != 4000 || c.Resources.Requests.Memory().Value() != 4294967296 || c.Resources.Limits.Memory().Value() != 8589934592 || c.Resources.Limits.StorageEphemeral().Value() != 21474836480 {
		t.Fatal("runtime/resource drift")
	}
	if *c.SecurityContext.Privileged || *c.SecurityContext.AllowPrivilegeEscalation || !*c.SecurityContext.ReadOnlyRootFilesystem || !*c.SecurityContext.RunAsNonRoot || *c.SecurityContext.RunAsUser != 65532 || !reflect.DeepEqual(c.SecurityContext.Capabilities.Drop, []v1.Capability{"ALL"}) || c.SecurityContext.SeccompProfile.Type != v1.SeccompProfileTypeRuntimeDefault {
		t.Fatal("unsafe container settings")
	}
	if len(pod.Volumes) != 4 || pod.Volumes[0].PersistentVolumeClaim.ClaimName != "workspace" || pod.Volumes[1].PersistentVolumeClaim.ClaimName != "vendor-state" || pod.Volumes[2].EmptyDir.SizeLimit.Value() != 1<<30 || pod.Volumes[3].Secret.SecretName != "bootstrap" || !c.VolumeMounts[3].ReadOnly {
		t.Fatal("attachment mapping drift")
	}
	// Returned objects and snapshots may not mutate future renders.
	before, _ := json.Marshal(template)
	pod.NodeSelector["kubernetes.io/arch"] = "arm64"
	c.Command[0] = "mutated"
	p, raw, _, impl, _ := mapper.Resolution()
	p.Platform.Architectures[0] = "arm64"
	raw[0] = '!'
	impl[0] = '!'
	again, err := mapper.Render(r, volumes)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(again)
	if string(before) != string(after) {
		t.Fatal("mutable template configuration")
	}
}
func TestCodingTemplateRejectsSubstitution(t *testing.T) {
	cases := map[string]func(*sandbox.AcquireRequest, *CodingVolumes){
		"profile digest": func(r *sandbox.AcquireRequest, _ *CodingVolumes) {
			r.ProfileDigest = "sha256:" + strings.Repeat("e", 64)
		},
		"implementation digest": func(r *sandbox.AcquireRequest, _ *CodingVolumes) {
			r.ImplementationDigest = "sha256:" + strings.Repeat("e", 64)
		},
		"resource widening": func(r *sandbox.AcquireRequest, _ *CodingVolumes) { r.Profile.Resources.Memory.Limit++ },
		"architecture":      func(r *sandbox.AcquireRequest, _ *CodingVolumes) { r.Runtime.Architecture = "arm64" },
		"mutable image":     func(r *sandbox.AcquireRequest, _ *CodingVolumes) { r.Runtime.Image = "image:latest" },
		"attachment":        func(_ *sandbox.AcquireRequest, v *CodingVolumes) { v.AttachmentReference = "other" },
		"bootstrap":         func(_ *sandbox.AcquireRequest, v *CodingVolumes) { v.BootstrapReference = "other" },
		"claim path":        func(_ *sandbox.AcquireRequest, v *CodingVolumes) { v.WorkspaceClaim = "../other" },
		"shared state":      func(_ *sandbox.AcquireRequest, v *CodingVolumes) { v.StateClaim = v.WorkspaceClaim },
		"mount path":        func(r *sandbox.AcquireRequest, _ *CodingVolumes) { r.Workspace.MountPath = "/" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			mapper, r, v := codingFixture(t)
			mutate(&r, &v)
			if _, err := mapper.Render(r, v); err == nil {
				t.Fatal("accepted substitution")
			}
		})
	}
}
func TestCodingTemplateRequiresQualification(t *testing.T) {
	mapper, _, _ := codingFixture(t)
	_, raw, _, _, _ := mapper.Resolution()
	if _, err := NewCodingTemplate(raw, mapper.config, nil); err == nil {
		t.Fatal("missing qualifier")
	}
	if _, err := NewCodingTemplate(raw, mapper.config, func(runtimeprofile.Profile, CodingTemplateConfig) error { return errors.New("unqualified") }); err == nil {
		t.Fatal("qualification ignored")
	}
	var p runtimeprofile.Profile
	_ = json.Unmarshal(raw, &p)
	p.Lifecycle.WarmPoolEligible = true
	raw, _ = json.Marshal(p)
	if _, err := NewCodingTemplate(raw, mapper.config, func(runtimeprofile.Profile, CodingTemplateConfig) error { return nil }); err == nil {
		t.Fatal("unqualified warm pool")
	}
}
