package agentsandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	core "sigs.k8s.io/agent-sandbox/api/v1beta1"
)

func secureFixture(t *testing.T) (BlueprintResolver, sandbox.Binding, *v1.Pod) {
	t.Helper()
	template, r, volumes := codingFixture(t)
	mapped, err := template.Render(r, volumes)
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(context.Context, sandbox.AcquireRequest) (core.SandboxBlueprint, error) {
		return *mapped.Spec.SandboxBlueprint.DeepCopy(), nil
	}
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: mapped.Name, Namespace: "agents", UID: "pod-uid", Labels: mapped.Spec.PodTemplate.ObjectMeta.Labels}, Spec: *mapped.Spec.PodTemplate.Spec.DeepCopy(), Status: v1.PodStatus{Phase: v1.PodRunning, ContainerStatuses: []v1.ContainerStatus{{Name: "agent", Ready: true, ImageID: r.Runtime.Image, ContainerID: "containerd://fixture", State: v1.ContainerState{Running: &v1.ContainerStateRunning{}}}}}}
	pod.Spec.NodeName = "qualified-node"
	pod.Spec.DNSPolicy = v1.DNSClusterFirst
	pod.Spec.ServiceAccountName = "default"
	return resolve, sandbox.Binding{Request: r, ProviderReference: "agents/name/uid"}, pod
}
func TestSecureEffectiveVerifierRequiresExternalProof(t *testing.T) {
	resolve, b, pod := secureFixture(t)
	if _, err := NewSecureEffectiveVerifier(resolve, nil); err == nil {
		t.Fatal("missing infrastructure verifier")
	}
	reject, _ := NewSecureEffectiveVerifier(resolve, func(context.Context, sandbox.Binding, *v1.Pod) error { return errors.New("no trusted evidence") })
	if facts, err := reject(context.Background(), b, pod); err == nil || facts.Verified {
		t.Fatal("desired state became proof")
	}
	calls := 0
	verify, _ := NewSecureEffectiveVerifier(resolve, func(_ context.Context, _ sandbox.Binding, copy *v1.Pod) error {
		calls++
		copy.Spec.NodeName = "mutated"
		return nil
	})
	for range 2 {
		facts, err := verify(context.Background(), b, pod)
		if err != nil || !facts.Verified || facts.Image != b.Request.Runtime.Image || facts.AttachmentReference != b.Request.Workspace.Reference || !shaDigest.MatchString(facts.ResourceDigest) {
			t.Fatal(facts, err)
		}
	}
	if calls != 2 || pod.Spec.NodeName != "qualified-node" {
		t.Fatal("mutable observed evidence")
	}
}
func TestSecureEffectiveVerifierRejectsUnsafeObservedPod(t *testing.T) {
	yes, no := true, false
	root := int64(0)
	cases := map[string]func(*v1.Pod){
		"token": func(p *v1.Pod) { p.Spec.AutomountServiceAccountToken = &yes },
		"token projection": func(p *v1.Pod) {
			p.Spec.Volumes = append(p.Spec.Volumes, v1.Volume{Name: "token", VolumeSource: v1.VolumeSource{Projected: &v1.ProjectedVolumeSource{Sources: []v1.VolumeProjection{{ServiceAccountToken: &v1.ServiceAccountTokenProjection{Path: "token"}}}}}})
		},
		"host network": func(p *v1.Pod) { p.Spec.HostNetwork = true }, "host PID": func(p *v1.Pod) { p.Spec.HostPID = true }, "host IPC": func(p *v1.Pod) { p.Spec.HostIPC = true },
		"hostPath/socket": func(p *v1.Pod) {
			p.Spec.Volumes[0].VolumeSource = v1.VolumeSource{HostPath: &v1.HostPathVolumeSource{Path: "/run/containerd/containerd.sock"}}
		},
		"privilege":     func(p *v1.Pod) { p.Spec.Containers[0].SecurityContext.Privileged = &yes },
		"escalation":    func(p *v1.Pod) { p.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation = &yes },
		"capability":    func(p *v1.Pod) { p.Spec.Containers[0].SecurityContext.Capabilities.Add = []v1.Capability{"SYS_ADMIN"} },
		"root":          func(p *v1.Pod) { p.Spec.Containers[0].SecurityContext.RunAsUser = &root },
		"writable root": func(p *v1.Pod) { p.Spec.Containers[0].SecurityContext.ReadOnlyRootFilesystem = &no },
		"seccomp": func(p *v1.Pod) {
			p.Spec.Containers[0].SecurityContext.SeccompProfile.Type = v1.SeccompProfileTypeUnconfined
		},
		"injected container": func(p *v1.Pod) { p.Spec.Containers = append(p.Spec.Containers, v1.Container{Name: "injected"}) },
		"init container":     func(p *v1.Pod) { p.Spec.InitContainers = []v1.Container{{Name: "init"}} },
		"ephemeral container": func(p *v1.Pod) {
			p.Spec.EphemeralContainers = []v1.EphemeralContainer{{EphemeralContainerCommon: v1.EphemeralContainerCommon{Name: "debug"}}}
		},
		"runtime":   func(p *v1.Pod) { s := "runc"; p.Spec.RuntimeClassName = &s },
		"image":     func(p *v1.Pod) { p.Spec.Containers[0].Image = "other:latest" },
		"resources": func(p *v1.Pod) { p.Spec.Containers[0].Resources.Limits[v1.ResourceMemory] = resource.MustParse("16Gi") },
		"extra env": func(p *v1.Pod) { p.Spec.Containers[0].Env = []v1.EnvVar{{Name: "UNREVIEWED", Value: "value"}} },
		"runtime annotation": func(p *v1.Pod) {
			p.Annotations = map[string]string{"io.katacontainers.config.hypervisor.path": "/unreviewed"}
		},
		"network annotation": func(p *v1.Pod) { p.Annotations = map[string]string{"k8s.v1.cni.cncf.io/networks": "other"} },
		"DNS override":       func(p *v1.Pod) { p.Spec.DNSConfig = &v1.PodDNSConfig{Nameservers: []string{"8.8.8.8"}} },
		"unknown image ID":   func(p *v1.Pod) { p.Status.ContainerStatuses[0].ImageID = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			resolve, b, pod := secureFixture(t)
			mutate(pod)
			verify, _ := NewSecureEffectiveVerifier(resolve, func(context.Context, sandbox.Binding, *v1.Pod) error {
				t.Fatal("unsafe Pod reached infrastructure proof")
				return nil
			})
			facts, err := verify(context.Background(), b, pod)
			if err == nil || facts.Verified {
				t.Fatal("unsafe effective Pod accepted")
			}
		})
	}
}

func TestAcquireRejectsUnsafeBlueprintBeforeReservation(t *testing.T) {
	api, bindings := &testAPI{}, &testBindings{}
	provider := providerFixture(t, api, bindings)
	original := provider.resolve
	provider.resolve = func(ctx context.Context, r sandbox.AcquireRequest) (core.SandboxBlueprint, error) {
		b, err := original(ctx, r)
		yes := true
		b.PodTemplate.Spec.Containers[0].SecurityContext.Privileged = &yes
		return b, err
	}
	if _, err := provider.Acquire(context.Background(), acquireFixture(t)); err == nil || api.creates != 0 || bindings.b != nil {
		t.Fatal("unsafe compute reserved or created")
	}
}

func TestSecureBlueprintRejectsUnboundedOrHostAccess(t *testing.T) {
	for _, mutate := range []func(*v1.PodSpec){
		func(p *v1.PodSpec) { p.HostNetwork = true },
		func(p *v1.PodSpec) { yes := true; p.AutomountServiceAccountToken = &yes },
		func(p *v1.PodSpec) { p.Containers[0].Resources.Limits = nil },
		func(p *v1.PodSpec) {
			p.Volumes[0].VolumeSource = v1.VolumeSource{HostPath: &v1.HostPathVolumeSource{Path: "/"}}
		},
		func(p *v1.PodSpec) { p.Containers[0].VolumeMounts[0].SubPath = "escape" },
		func(p *v1.PodSpec) { p.InitContainers = []v1.Container{{Name: "unexpected"}} },
		func(p *v1.PodSpec) {
			p.Containers[0].EnvFrom = []v1.EnvFromSource{{SecretRef: &v1.SecretEnvSource{LocalObjectReference: v1.LocalObjectReference{Name: "not-bootstrap"}}}}
		},
	} {
		resolve, b, _ := secureFixture(t)
		blueprint, err := resolve(context.Background(), b.Request)
		if err != nil {
			t.Fatal(err)
		}
		if !secureBlueprint(b.Request, blueprint.PodTemplate.Spec) {
			t.Fatal("baseline rejected")
		}
		mutate(&blueprint.PodTemplate.Spec)
		if secureBlueprint(b.Request, blueprint.PodTemplate.Spec) {
			t.Fatal("unsafe blueprint accepted")
		}
	}
}
