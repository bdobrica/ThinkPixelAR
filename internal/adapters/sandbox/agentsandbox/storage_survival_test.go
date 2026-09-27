package agentsandbox

import (
	"context"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// Capture storage before AR Release, then verify physical compute absence,
// unchanged independent PVC/PV identities and bytes through a fresh read-only Pod.
// This is provider evidence, not live WS metadata or AG authorization coverage.
func liveStorageSurvival(t *testing.T, ctx context.Context, client dynamic.Interface, endpoint, namespace, sandboxName, image, expected string) func() {
	t.Helper()
	api, err := rest.UnversionedRESTClientFor(dynamic.ConfigFor(&rest.Config{Host: endpoint}))
	if err != nil {
		t.Fatal(err)
	}
	claims := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}).Namespace(namespace)
	getClaim := func(name string) (*v1.PersistentVolumeClaim, error) {
		obj, err := claims.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		var claim v1.PersistentVolumeClaim
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &claim)
		return &claim, err
	}
	getVolume := func(name string) (*v1.PersistentVolume, error) {
		obj, err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		var pv v1.PersistentVolume
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &pv)
		return &pv, err
	}
	logs := func(name, container string) ([]byte, error) {
		return api.Get().AbsPath("/api/v1/namespaces/"+namespace+"/pods/"+name+"/log").Param("container", container).DoRaw(ctx)
	}
	pods := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).Namespace(namespace)
	before := map[string]*v1.PersistentVolumeClaim{}
	volumes := map[string]*v1.PersistentVolume{}
	for _, name := range []string{"workspace", "state"} {
		claim, err := getClaim(name)
		if err != nil {
			t.Fatal(err)
		}
		if claim.Status.Phase != v1.ClaimBound || claim.DeletionTimestamp != nil || len(claim.OwnerReferences) != 0 {
			t.Fatal("storage is not independently owned and bound")
		}
		pv, err := getVolume(claim.Spec.VolumeName)
		if err != nil {
			t.Fatal(err)
		}
		before[name], volumes[name] = claim, pv
	}
	// Native readiness alone does not prove the marker write has finished.
	liveWait(t, ctx, func() bool {
		output, err := logs(sandboxName, "agent")
		return err == nil && strings.TrimSpace(string(output)) == expected
	})
	return func() {
		t.Helper()
		if _, err := client.Resource(sandboxResource).Namespace(namespace).Get(ctx, sandboxName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Fatal("Sandbox still exists", err)
		}
		liveWait(t, ctx, func() bool {
			list, err := pods.List(ctx, metav1.ListOptions{})
			return err == nil && len(list.Items) == 0
		})
		for name, old := range before {
			claim, err := getClaim(name)
			if err != nil {
				t.Fatal(err)
			}
			if claim.UID != old.UID || claim.Spec.VolumeName != old.Spec.VolumeName || claim.DeletionTimestamp != nil || len(claim.OwnerReferences) != 0 || claim.Status.Phase != v1.ClaimBound {
				t.Fatal("release changed persistent claim", name)
			}
			pv, err := getVolume(claim.Spec.VolumeName)
			if err != nil {
				t.Fatal(err)
			}
			if pv.UID != volumes[name].UID || pv.DeletionTimestamp != nil || pv.Spec.ClaimRef == nil || pv.Spec.ClaimRef.UID != claim.UID {
				t.Fatal("release changed backing volume", name)
			}
			t.Logf("preserved %s PVC=%s PV=%s UID=%s", name, claim.UID, pv.Name, pv.UID)
		}
		// Restricted Pod; no credentials, no writer and no Sandbox ownership.
		verifier := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "tar005-reader"},
			"spec": map[string]any{
				"restartPolicy": "Never", "automountServiceAccountToken": false,
				"securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": int64(65532), "seccompProfile": map[string]any{"type": "RuntimeDefault"}},
				"containers":      []any{map[string]any{"name": "reader", "image": image, "command": []any{"cat", "/workspace/tar005"}, "securityContext": map[string]any{"allowPrivilegeEscalation": false, "capabilities": map[string]any{"drop": []any{"ALL"}}}, "volumeMounts": []any{map[string]any{"name": "workspace", "mountPath": "/workspace", "readOnly": true}}}},
				"volumes":         []any{map[string]any{"name": "workspace", "persistentVolumeClaim": map[string]any{"claimName": "workspace", "readOnly": true}}},
			},
		}}
		reader, err := pods.Create(ctx, verifier, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		liveWait(t, ctx, func() bool {
			pod, err := pods.Get(ctx, reader.GetName(), metav1.GetOptions{})
			if err != nil {
				return false
			}
			phase, _, _ := unstructured.NestedString(pod.Object, "status", "phase")
			if phase == string(v1.PodFailed) {
				t.Fatal("storage reader failed")
			}
			return phase == string(v1.PodSucceeded)
		})
		output, err := logs(reader.GetName(), "reader")
		if err != nil || string(output) != expected {
			t.Fatal("bytes lost after AR release", err)
		}
		uid := reader.GetUID()
		if err := pods.Delete(ctx, reader.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil {
			t.Fatal(err)
		}
		liveWait(t, ctx, func() bool {
			_, err := pods.Get(ctx, reader.GetName(), metav1.GetOptions{})
			return apierrors.IsNotFound(err)
		})
		t.Log("Sandbox and all execution Pods absent; fresh read-only Pod recovered original bytes")
	}
}
