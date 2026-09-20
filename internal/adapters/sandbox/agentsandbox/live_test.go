package agentsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	v1 "k8s.io/api/core/v1"
	networking "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	core "sigs.k8s.io/agent-sandbox/api/v1beta1"
)

// This explicitly opted-in fixture tests lifecycle, not full profile qualification.
// Template/network qualification callbacks are fixture-only; no effective verifier
// is supplied, so AR must withhold READY despite upstream's Ready condition.
func TestLiveColdLifecycle(t *testing.T) { runLiveLifecycle(t, false) }

func TestLiveNativeSuspendResume(t *testing.T) { runLiveLifecycle(t, true) }

func runLiveLifecycle(t *testing.T, nativeSuspend bool) {
	endpoint := os.Getenv("THINKPIXELAR_TEST_KUBE_API")
	if endpoint == "" {
		t.Skip("THINKPIXELAR_TEST_KUBE_API not set")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.User != nil {
		t.Fatal("test API must use an operator-owned loopback tunnel")
	}
	node, runtimeClass := os.Getenv("THINKPIXELAR_TEST_NODE"), os.Getenv("THINKPIXELAR_TEST_RUNTIME_CLASS")
	if node == "" || runtimeClass == "" {
		t.Fatal("explicit test node and runtime class required")
	}
	client, err := dynamic.NewForConfig(&rest.Config{Host: endpoint, Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	id, err := primitives.NewID(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	namespace := "ar-live-" + string(id)
	create := func(gvr schema.GroupVersionResource, obj any) {
		t.Helper()
		raw, e := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if e != nil {
			t.Fatal(e)
		}
		u := &unstructured.Unstructured{Object: raw}
		var e2 error
		if u.GetNamespace() == "" {
			_, e2 = client.Resource(gvr).Create(ctx, u, metav1.CreateOptions{})
		} else {
			_, e2 = client.Resource(gvr).Namespace(u.GetNamespace()).Create(ctx, u, metav1.CreateOptions{})
		}
		if e2 != nil {
			t.Fatal(e2)
		}
	}
	create(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, &v1.Namespace{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"}, ObjectMeta: metav1.ObjectMeta{Name: namespace, Labels: map[string]string{"pod-security.kubernetes.io/enforce": "restricted"}}})
	t.Log("retained namespace:", namespace)
	policy := &networking.NetworkPolicy{TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"}, ObjectMeta: metav1.ObjectMeta{Name: "deny-all", Namespace: namespace}, Spec: networking.NetworkPolicySpec{PodSelector: metav1.LabelSelector{}, PolicyTypes: []networking.PolicyType{networking.PolicyTypeIngress, networking.PolicyTypeEgress}}}
	create(schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}, policy)
	for _, name := range []string{"workspace", "state"} {
		storage := "local-path"
		create(schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}, &v1.PersistentVolumeClaim{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"}, ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, Spec: v1.PersistentVolumeClaimSpec{StorageClassName: &storage, AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce}, Resources: v1.VolumeResourceRequirements{Requests: v1.ResourceList{v1.ResourceStorage: resource.MustParse("1Gi")}}}})
	}
	immutable := true
	create(schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, &v1.Secret{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}, ObjectMeta: metav1.ObjectMeta{Name: "bootstrap", Namespace: namespace}, Immutable: &immutable})
	nodeObject, err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "nodes"}).Get(ctx, node, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	architecture, found, err := unstructured.NestedString(nodeObject.Object, "status", "nodeInfo", "architecture")
	if err != nil || !found || (architecture != "arm64" && architecture != "amd64") || nodeObject.GetLabels()["kubernetes.io/arch"] != architecture {
		t.Fatal("unsupported or inconsistent node architecture")
	}
	raw, err := os.ReadFile("../../../../docs/profiles/coding-homelab-" + architecture + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var profile runtimeprofile.Profile
	if err = json.Unmarshal(raw, &profile); err != nil {
		t.Fatal(err)
	}
	cfg := CodingTemplateConfig{References: profile.Implementation, RuntimeClass: runtimeClass, NodeSelector: map[string]string{"kubernetes.io/hostname": node}, UserID: 65532, GroupID: 65532, TempBytes: 32 << 20, QualificationDigest: "sha256:" + strings.Repeat("d", 64)}
	mapper, err := NewCodingTemplate(raw, cfg, func(runtimeprofile.Profile, CodingTemplateConfig) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	request := acquireFixture(t)
	request.Scope.SandboxID = id
	request.Scope.AttemptID = id
	request.Runtime = sandbox.Runtime{Image: "docker.io/library/busybox@sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0", Architecture: architecture, Entrypoint: []string{"sh", "-ec", "sleep 900"}}
	request.Profile, _, request.ProfileDigest, _, request.ImplementationDigest = mapper.Resolution()
	request.Workspace.MountPath = "/workspace"
	request.Deadline = time.Now().UTC().Add(10 * time.Minute).Truncate(time.Second)
	resolve := func(_ context.Context, r sandbox.AcquireRequest) (core.SandboxBlueprint, error) {
		mapped, e := mapper.Render(r, CodingVolumes{AttachmentReference: r.Workspace.Reference, BootstrapReference: r.BootstrapReference, WorkspaceClaim: "workspace", StateClaim: "state", BootstrapSecret: "bootstrap"})
		if e != nil {
			return core.SandboxBlueprint{}, e
		}
		return mapped.Spec.SandboxBlueprint, nil
	}
	policyObj, err := client.Resource(schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}).Namespace(namespace).Get(ctx, "deny-all", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.DefaultUnstructuredConverter.FromUnstructured(policyObj.Object, policy); err != nil {
		t.Fatal(err)
	}
	enforce, err := NewNamespaceNetworkEnforcer(client, NamespaceNetworkBinding{request.ProfileDigest, request.ImplementationDigest, policy}, func(context.Context, sandbox.AcquireRequest, *networking.NetworkPolicy) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	var original string
	for generation := range 2 {
		if generation > 0 {
			id, err = primitives.NewID(time.Now())
			if err != nil {
				t.Fatal(err)
			}
			request.Scope.SandboxID = id
			request.Scope.AttemptID = id
			request.Scope.AttemptOrdinal++
		}
		request.Operation.ID = string(id)
		request.Operation.Digest, _ = RequestDigest(request)
		bindings := &testBindings{}
		provider, err := New(client, bindings, namespace, resolve, WithNetworkEnforcer(enforce))
		if err != nil {
			t.Fatal(err)
		}
		handle, err := provider.Acquire(ctx, request)
		if err != nil {
			t.Fatal("acquire", err)
		}
		restarted, err := New(client, bindings, namespace, resolve, WithNetworkEnforcer(enforce))
		if err != nil {
			t.Fatal(err)
		}
		replay, err := restarted.Acquire(ctx, request)
		if err != nil || replay.ProviderReference != handle.ProviderReference {
			t.Fatal("adapter replay", err)
		}
		if generation == 1 && handle.ProviderReference == original {
			t.Fatal("replacement reused identity")
		}
		original = handle.ProviderReference
		name := "ar-" + string(id)
		liveWait(t, ctx, func() bool {
			obj, e := client.Resource(sandboxResource).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
			if e != nil {
				return false
			}
			var sb core.Sandbox
			if runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &sb) != nil {
				return false
			}
			for _, condition := range sb.Status.Conditions {
				if condition.Type == "Ready" && condition.Status == metav1.ConditionTrue && condition.ObservedGeneration == sb.Generation {
					return true
				}
			}
			return false
		})
		status, err := restarted.Get(ctx, request.Scope.TenantID, id)
		if err != nil || status.State != sandbox.Unknown || status.Reason != "EFFECTIVE_STATE_UNVERIFIED" {
			t.Fatalf("unqualified native readiness promoted: %+v %v", status, err)
		}
		t.Log("native readiness observed; secure readiness withheld:", handle.ProviderReference)

		if nativeSuspend && generation == 0 {
			pods := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).Namespace(namespace)
			before, e := pods.Get(ctx, name, metav1.GetOptions{})
			if e != nil {
				t.Fatal(e)
			}
			suspendOp := lifecycleOperation(request, "suspend", "suspend-"+string(id))
			for range 2 {
				if e = restarted.Suspend(ctx, request.Scope.TenantID, id, suspendOp); e != nil {
					t.Fatal("suspend replay", e)
				}
			}
			liveWait(t, ctx, func() bool {
				state, e := restarted.Get(ctx, request.Scope.TenantID, id)
				return e == nil && state.State == sandbox.Suspended
			})
			if _, e = pods.Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(e) {
				t.Fatal("suspended compute still exists", e)
			}
			resumeOp := lifecycleOperation(request, "resume", "resume-"+string(id))
			for range 2 {
				resumed, e := restarted.Resume(ctx, request.Scope.TenantID, id, resumeOp)
				if e != nil || resumed.ProviderReference != handle.ProviderReference {
					t.Fatal("resume identity/replay", e)
				}
			}
			liveWait(t, ctx, func() bool {
				state, e := restarted.Get(ctx, request.Scope.TenantID, id)
				return e == nil && state.State == sandbox.Unknown && state.Reason == "EFFECTIVE_STATE_UNVERIFIED"
			})
			after, e := pods.Get(ctx, name, metav1.GetOptions{})
			if e != nil || after.GetUID() == before.GetUID() {
				t.Fatal("resume did not replace process/Pod", e)
			}
			saved, e := client.Resource(sandboxResource).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
			if e != nil {
				t.Fatal(e)
			}
			var actual core.Sandbox
			if e = runtime.DefaultUnstructuredConverter.FromUnstructured(saved.Object, &actual); e != nil {
				t.Fatal(e)
			}
			if actual.Spec.ShutdownTime == nil || !actual.Spec.ShutdownTime.Time.Equal(request.Deadline) {
				t.Fatal("resume changed absolute deadline")
			}
			t.Log("native suspension removed Pod; resume kept Sandbox UID and deadline and created new Pod:", before.GetUID(), after.GetUID())
		}
		operation := lifecycleOperation(request, "release", "release-"+string(id))
		for range 2 {
			if err = restarted.Release(ctx, request.Scope.TenantID, id, operation); err != nil {
				t.Fatal("release replay", err)
			}
		}
		liveWait(t, ctx, func() bool {
			_, e := client.Resource(sandboxResource).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
			return apierrors.IsNotFound(e)
		})
		if _, err = restarted.Acquire(ctx, request); !errors.Is(err, sandbox.ErrNotFound) {
			t.Fatal("recreated released identity", err)
		}
		t.Log("release confirmed:", handle.ProviderReference)
	}
	for _, name := range []string{"workspace", "state"} {
		if _, err = client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}).Namespace(namespace).Get(ctx, name, metav1.GetOptions{}); err != nil {
			t.Fatal("release deleted Workspace", err)
		}
	}
}
func liveWait(t *testing.T, ctx context.Context, condition func() bool) {
	t.Helper()
	for {
		if condition() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("live condition timeout")
		case <-time.After(2 * time.Second):
		}
	}
}
