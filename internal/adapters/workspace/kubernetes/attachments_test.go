package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func attachmentFixture(t *testing.T) (*AttachmentResolver, workspace.Attachment, runtimeprofile.Profile, map[string]*v1.PersistentVolumeClaim, *int) {
	t.Helper()
	writes := 0
	a := workspace.Attachment{State: "PREPARED", ProviderKind: "kubernetes", WorkspaceVolumeReference: "agents/workspace/uid-workspace", StateVolumeReference: "agents/state/uid-state", MountPath: "/workspace", StorageProfileReference: "storage-v1", CapacityBytes: 1024, StateCapacityBytes: 512, AccessMode: "single-pod-writer", Encrypted: true, SnapshotCapable: true}
	p := runtimeprofile.Profile{Storage: runtimeprofile.Storage{WorkspaceBytes: 1024, WorkspaceMount: "/workspace", AccessMode: "single-pod-writer", EncryptionRequired: true, SnapshotClass: "required"}, Implementation: runtimeprofile.Implementation{StorageProfileRef: "storage-v1"}}
	claims := map[string]*v1.PersistentVolumeClaim{}
	for name, capacity := range map[string]int64{"workspace": 1024, "state": 512} {
		claims[name] = &v1.PersistentVolumeClaim{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"}, ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agents", UID: types.UID("uid-" + name)}, Spec: v1.PersistentVolumeClaimSpec{VolumeName: "pv-" + name, AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOncePod}, Resources: v1.VolumeResourceRequirements{Requests: v1.ResourceList{v1.ResourceStorage: *resource.NewQuantity(capacity, resource.BinarySI)}}}, Status: v1.PersistentVolumeClaimStatus{Phase: v1.ClaimBound, Capacity: v1.ResourceList{v1.ResourceStorage: *resource.NewQuantity(capacity, resource.BinarySI)}}}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes++
			http.Error(w, "unexpected write", 500)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/agents/persistentvolumeclaims/")
		claim := claims[name]
		if claim == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(claim)
	}))
	t.Cleanup(server.Close)
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewAttachmentResolver(client, "agents", func(context.Context, workspace.Attachment, runtimeprofile.Profile) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return resolver, a, p, claims, &writes
}
func TestAttachmentResolverPreservesStorageOwnership(t *testing.T) {
	r, a, p, _, writes := attachmentFixture(t)
	for range 2 {
		got, err := r.Resolve(context.Background(), a, p)
		if err != nil || got.WorkspaceClaim != "workspace" || got.StateClaim != "state" {
			t.Fatal(got, err)
		}
	}
	if *writes != 0 {
		t.Fatal("sandbox resolution mutated durable storage")
	}
}
func TestAttachmentResolverRejectsDrift(t *testing.T) {
	cases := map[string]func(*workspace.Attachment, map[string]*v1.PersistentVolumeClaim){
		"foreign namespace": func(a *workspace.Attachment, _ map[string]*v1.PersistentVolumeClaim) {
			a.WorkspaceVolumeReference = "other/workspace/uid-workspace"
		},
		"replaced UID": func(_ *workspace.Attachment, p map[string]*v1.PersistentVolumeClaim) { p["workspace"].UID = "new" },
		"pending": func(_ *workspace.Attachment, p map[string]*v1.PersistentVolumeClaim) {
			p["workspace"].Status.Phase = v1.ClaimPending
		},
		"access mode": func(_ *workspace.Attachment, p map[string]*v1.PersistentVolumeClaim) {
			p["workspace"].Spec.AccessModes = []v1.PersistentVolumeAccessMode{v1.ReadWriteMany}
		},
		"capacity": func(_ *workspace.Attachment, p map[string]*v1.PersistentVolumeClaim) {
			p["workspace"].Status.Capacity[v1.ResourceStorage] = resource.MustParse("2Ki")
		},
		"block device": func(_ *workspace.Attachment, p map[string]*v1.PersistentVolumeClaim) {
			mode := v1.PersistentVolumeBlock
			p["workspace"].Spec.VolumeMode = &mode
		},
		"encryption":  func(a *workspace.Attachment, _ map[string]*v1.PersistentVolumeClaim) { a.Encrypted = false },
		"snapshots":   func(a *workspace.Attachment, _ map[string]*v1.PersistentVolumeClaim) { a.SnapshotCapable = false },
		"uncommitted": func(a *workspace.Attachment, _ map[string]*v1.PersistentVolumeClaim) { a.State = "PENDING" },
		"same volume": func(a *workspace.Attachment, _ map[string]*v1.PersistentVolumeClaim) {
			a.StateVolumeReference = a.WorkspaceVolumeReference
			a.StateCapacityBytes = a.CapacityBytes
		},
		"missing": func(_ *workspace.Attachment, p map[string]*v1.PersistentVolumeClaim) { delete(p, "workspace") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r, a, p, claims, writes := attachmentFixture(t)
			mutate(&a, claims)
			if _, err := r.Resolve(context.Background(), a, p); err == nil {
				t.Fatal("accepted unsafe attachment")
			}
			if *writes != 0 {
				t.Fatal("mutated storage")
			}
		})
	}
	r, a, p, _, _ := attachmentFixture(t)
	r.qualify = func(context.Context, workspace.Attachment, runtimeprofile.Profile) error {
		return errors.New("unqualified")
	}
	if _, err := r.Resolve(context.Background(), a, p); err == nil {
		t.Fatal("qualification bypass")
	}
}
