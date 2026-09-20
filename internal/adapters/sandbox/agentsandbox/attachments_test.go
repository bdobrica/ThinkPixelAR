package agentsandbox

import (
	"context"
	"encoding/json"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	workspacek8s "github.com/bdobrica/ThinkPixelAR/internal/adapters/workspace/kubernetes"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type attachmentReaderFunc func(context.Context, primitives.ID, string) (workspace.Attachment, error)

func (f attachmentReaderFunc) GetAttachment(ctx context.Context, tenant primitives.ID, ref string) (workspace.Attachment, error) {
	return f(ctx, tenant, ref)
}
func TestAttachedBlueprintRejectsWrongOwnershipBeforeProviderReads(t *testing.T) {
	template, r, _ := codingFixture(t)
	api := &testAPI{}
	provider := providerFixture(t, api, &testBindings{})
	volumes, err := workspacek8s.NewAttachmentResolver(provider.client, "agents", func(context.Context, workspace.Attachment, runtimeprofile.Profile) error {
		t.Fatal("unowned attachment reached storage qualification")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := attachmentReaderFunc(func(context.Context, primitives.ID, string) (workspace.Attachment, error) {
		return workspace.Attachment{Reference: r.Workspace.Reference, Scope: workspace.AttachmentScope{TenantID: r.Scope.TenantID}}, nil
	})
	resolve, err := AttachedBlueprintResolver(template, reader, volumes, func(context.Context, sandbox.Scope, string) (string, error) {
		t.Fatal("unowned attachment reached bootstrap")
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = resolve(context.Background(), r); err == nil {
		t.Fatal("accepted foreign ownership")
	}
}

func TestAttachedBlueprintComposesReservedVolumes(t *testing.T) {
	template, r, _ := codingFixture(t)
	r.Workspace.WorkspaceID = r.Scope.SessionID
	scope := workspace.AttachmentScope{TenantID: r.Scope.TenantID, SessionID: r.Scope.SessionID, ExecutionID: r.Scope.ExecutionID, AttemptID: r.Scope.AttemptID, SandboxID: r.Scope.SandboxID, WorkspaceID: r.Workspace.WorkspaceID, ExecutionGeneration: r.Scope.Generation, AttemptOrdinal: r.Scope.AttemptOrdinal, WorkspaceGeneration: r.Workspace.Generation}
	a := workspace.Attachment{Scope: scope, Reference: r.Workspace.Reference, OperationID: r.Scope.AttemptID, RequestDigest: r.Operation.Digest, State: "PREPARED", ProviderKind: "kubernetes", WorkspaceVolumeReference: "agents/workspace/uid-workspace", StateVolumeReference: "agents/state/uid-state", MountPath: "/workspace", StorageProfileReference: r.Profile.Implementation.StorageProfileRef, ConfigurationDigest: r.ImplementationDigest, CapacityBytes: r.Profile.Storage.WorkspaceBytes, StateCapacityBytes: 1 << 30, AccessMode: r.Profile.Storage.AccessMode, Encrypted: true, SnapshotCapable: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" {
			t.Error("unexpected mutation")
			http.Error(w, "write", 500)
			return
		}
		name := strings.TrimPrefix(req.URL.Path, "/api/v1/namespaces/agents/persistentvolumeclaims/")
		size := a.CapacityBytes
		if name == "state" {
			size = a.StateCapacityBytes
		} else if name != "workspace" {
			http.NotFound(w, req)
			return
		}
		claim := v1.PersistentVolumeClaim{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"}, ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agents", UID: types.UID("uid-" + name)}, Spec: v1.PersistentVolumeClaimSpec{VolumeName: "pv-" + name, AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOncePod}, Resources: v1.VolumeResourceRequirements{Requests: v1.ResourceList{v1.ResourceStorage: *resource.NewQuantity(size, resource.BinarySI)}}}, Status: v1.PersistentVolumeClaimStatus{Phase: v1.ClaimBound, Capacity: v1.ResourceList{v1.ResourceStorage: *resource.NewQuantity(size, resource.BinarySI)}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(claim)
	}))
	defer server.Close()
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	volumes, err := workspacek8s.NewAttachmentResolver(client, "agents", func(context.Context, workspace.Attachment, runtimeprofile.Profile) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	reader := attachmentReaderFunc(func(_ context.Context, tenant primitives.ID, ref string) (workspace.Attachment, error) {
		if tenant != a.Scope.TenantID || ref != a.Reference {
			t.Fatal("wrong lookup")
		}
		return a, nil
	})
	resolve, err := AttachedBlueprintResolver(template, reader, volumes, func(_ context.Context, scope sandbox.Scope, ref string) (string, error) {
		if scope != r.Scope || ref != r.BootstrapReference {
			t.Fatal("wrong bootstrap lookup")
		}
		return "bootstrap", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		blueprint, err := resolve(context.Background(), r)
		if err != nil || len(blueprint.VolumeClaimTemplates) != 0 || blueprint.PodTemplate.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "workspace" {
			t.Fatal("composition failed", err)
		}
	}
}
