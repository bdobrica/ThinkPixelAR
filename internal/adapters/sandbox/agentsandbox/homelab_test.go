package agentsandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func TestHomelabFreshEvidenceAndLiveIdentity(t *testing.T) {
	_, b, pod := secureFixture(t)
	b.Request.Profile.Name = "coding-homelab-arm64"
	b.Request.Runtime.Architecture = "arm64"
	b.Request.Profile.Resources.CPU.Limit = 1000
	b.Request.Profile.Resources.Memory.Limit = 536870912
	b.Request.Profile.Resources.MaxProcesses = 128
	pod.Spec.NodeName = "k3spi-02"
	class := "kata-qemu-runtime-rs-ar331-bounded"
	pod.Spec.RuntimeClassName = &class
	digest := "sha256:" + strings.Repeat("a", 64)
	pod.Status.ContainerStatuses[0].ImageID = digest
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == "tmp" {
			pod.Spec.Volumes[i].VolumeSource = v1.VolumeSource{Ephemeral: &v1.EphemeralVolumeSource{VolumeClaimTemplate: &v1.PersistentVolumeClaimTemplate{Spec: scratchClaim("ar-bounded-scratch-v1", 33554432)}}}
		}
	}
	pin := HomelabPin{Node: "k3spi-02", NodeUID: "node-uid", RuntimeClassUID: "class-uid", Artifacts: map[string]string{}}
	for _, key := range []string{"qemu", "kernel", "image", "runtime", "configuration", "cni"} {
		pin.Artifacts[key] = digest
	}
	now := time.Now()
	e := HomelabEvidence{Version: 1, ObservedAt: now.Add(-time.Second), ExpiresAt: now.Add(20 * time.Second), Scope: b.Request.Scope, RequestDigest: b.Request.Operation.Digest, ProviderReference: b.ProviderReference, PodUID: string(pod.UID), ContainerID: pod.Status.ContainerStatuses[0].ContainerID, NodeUID: pin.NodeUID, RuntimeClassUID: pin.RuntimeClassUID, Handler: class, Artifacts: pin.Artifacts, CRISandboxID: "cri-instance", QEMUPID: 12, QEMUStartTicks: 123, KVMDescriptor: "anon_inode:kvm-vm", ImageReference: b.Request.Runtime.Image, ImageManifest: digest, Architecture: "arm64", CPUQuota: 100000, CPUPeriod: 100000, MemoryMax: 536870912, HardNProc: 128, ScratchBytes: 33554432, WorkspaceReference: b.Request.Workspace.Reference, NetworkMeasurement: digest, MountMeasurement: digest, ResourceMeasurement: digest}
	objects := map[string]any{
		"/api/v1/nodes/k3spi-02":                       &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: pin.Node, UID: types.UID(pin.NodeUID)}, Status: v1.NodeStatus{NodeInfo: v1.NodeSystemInfo{Architecture: "arm64"}, Conditions: []v1.NodeCondition{{Type: v1.NodeReady, Status: v1.ConditionTrue}}}},
		"/apis/node.k8s.io/v1/runtimeclasses/" + class: &nodev1.RuntimeClass{ObjectMeta: metav1.ObjectMeta{Name: class, UID: types.UID(pin.RuntimeClassUID)}, Handler: class, Overhead: &nodev1.Overhead{PodFixed: v1.ResourceList{v1.ResourceCPU: resource.MustParse("250m"), v1.ResourceMemory: resource.MustParse("2304Mi")}}},
	}
	add := func(resourceName, namespace, name string, obj any) {
		prefix := "/api/v1/"
		if resourceName == "networkpolicies" {
			prefix = "/apis/networking.k8s.io/v1/"
		}
		if namespace != "" {
			prefix += "namespaces/" + namespace + "/"
		}
		objects[prefix+resourceName+"/"+name] = obj
		e.LiveObjects = append(e.LiveObjects, HomelabObject{Resource: resourceName, Namespace: namespace, Name: name, UID: name + "-uid", ResourceVersion: "1"})
	}
	add("namespaces", "", pod.Namespace, map[string]any{"metadata": map[string]string{"name": pod.Namespace, "uid": pod.Namespace + "-uid", "resourceVersion": "1"}})
	add("networkpolicies", pod.Namespace, "isolation", map[string]any{"metadata": map[string]string{"name": "isolation", "namespace": pod.Namespace, "uid": "isolation-uid", "resourceVersion": "1"}})
	for _, volume := range pod.Spec.Volumes {
		name := ""
		if volume.PersistentVolumeClaim != nil {
			name = volume.PersistentVolumeClaim.ClaimName
		}
		if volume.Name == "tmp" {
			name = pod.Name + "-tmp"
		}
		if name == "" {
			continue
		}
		claim := &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: pod.Namespace, UID: types.UID(name + "-uid"), ResourceVersion: "1"}, Spec: scratchClaim("ar-bounded-scratch-v1", 33554432), Status: v1.PersistentVolumeClaimStatus{Phase: v1.ClaimBound, Capacity: v1.ResourceList{v1.ResourceStorage: resource.MustParse("32Mi")}}}
		claim.Spec.VolumeName = name + "-pv"
		if volume.Name == "tmp" {
			yes := true
			claim.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: pod.Name, UID: pod.UID, Controller: &yes}}
		}
		pv := &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: claim.Spec.VolumeName, UID: types.UID(claim.Spec.VolumeName + "-uid"), ResourceVersion: "1"}, Spec: v1.PersistentVolumeSpec{ClaimRef: &v1.ObjectReference{Name: name, Namespace: pod.Namespace, UID: claim.UID}, StorageClassName: "ar-bounded-scratch-v1", Capacity: v1.ResourceList{v1.ResourceStorage: resource.MustParse("32Mi")}}, Status: v1.PersistentVolumeStatus{Phase: v1.VolumeBound}}
		add("persistentvolumeclaims", pod.Namespace, name, claim)
		add("persistentvolumes", "", pv.Name, pv)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o, ok := objects[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		raw, _ := json.Marshal(o)
		var object map[string]any
		_ = json.Unmarshal(raw, &object)
		object["apiVersion"] = "v1"
		for resource, kind := range map[string]string{"nodes": "Node", "runtimeclasses": "RuntimeClass", "persistentvolumeclaims": "PersistentVolumeClaim", "persistentvolumes": "PersistentVolume", "networkpolicies": "NetworkPolicy"} {
			if strings.Contains(r.URL.Path, "/"+resource+"/") {
				object["kind"] = kind
			}
		}
		if object["kind"] == nil {
			object["kind"] = "Namespace"
		}
		if strings.Contains(r.URL.Path, "/apis/node.k8s.io/") {
			object["apiVersion"] = "node.k8s.io/v1"
		}
		if strings.Contains(r.URL.Path, "/apis/networking.k8s.io/") {
			object["apiVersion"] = "networking.k8s.io/v1"
		}
		_ = json.NewEncoder(w).Encode(object)
	}))
	defer server.Close()
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	verifier, err := NewHomelabVerifier(client, root, pin)
	if err != nil {
		t.Fatal(err)
	}
	defer verifier.Close()
	ctx := context.Background()
	if verifier.Verify(ctx, b, pod) == nil {
		t.Fatal("missing evidence admitted")
	}
	publish := func(value HomelabEvidence) {
		raw, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(root, string(pod.UID)+".json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	publish(e)
	if err := verifier.check(b, pod, e, time.Now()); err != nil {
		t.Fatal("receipt tuple", err)
	}
	if err := verifier.Verify(ctx, b, pod); err != nil {
		t.Fatal("valid fixture evidence rejected", err)
	}
	for name, mutate := range map[string]func(*HomelabEvidence){
		"expired":               func(e *HomelabEvidence) { e.ExpiresAt = now.Add(-time.Second) },
		"future":                func(e *HomelabEvidence) { e.ObservedAt = now.Add(time.Minute) },
		"unbounded freshness":   func(e *HomelabEvidence) { e.ExpiresAt = now.Add(time.Hour) },
		"pod replacement":       func(e *HomelabEvidence) { e.PodUID = "other" },
		"container replacement": func(e *HomelabEvidence) { e.ContainerID = "other" },
		"binding":               func(e *HomelabEvidence) { e.Scope.Generation++ },
		"node":                  func(e *HomelabEvidence) { e.NodeUID = "other" },
		"image":                 func(e *HomelabEvidence) { e.ImageManifest = "sha256:" + strings.Repeat("b", 64) },
		"runtime":               func(e *HomelabEvidence) { e.Handler = "runc" },
		"resource":              func(e *HomelabEvidence) { e.HardNProc = 0 },
		"mount proof":           func(e *HomelabEvidence) { e.MountMeasurement = "" },
		"missing api proof":     func(e *HomelabEvidence) { e.LiveObjects = e.LiveObjects[:len(e.LiveObjects)-1] },
		"api drift":             func(e *HomelabEvidence) { e.LiveObjects[0].ResourceVersion = "old" },
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(e)
			var bad HomelabEvidence
			_ = json.Unmarshal(raw, &bad)
			mutate(&bad)
			publish(bad)
			if verifier.Verify(ctx, b, pod) == nil {
				t.Fatal("bad evidence accepted")
			}
		})
	}
	publish(e)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if verifier.Verify(cancelled, b, pod) == nil {
		t.Fatal("cancelled observation accepted")
	}
	// A revoked/changed API revision cannot be hidden by replaying a fresh receipt.
	e.LiveObjects[0].UID = "old"
	publish(e)
	if verifier.Verify(ctx, b, pod) == nil {
		t.Fatal("recreated namespace accepted")
	}
}
