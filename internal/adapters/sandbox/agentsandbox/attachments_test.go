package agentsandbox

import (
	"context"
	"encoding/json"
	"errors"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestAttachedBlueprintComposesWSReservedVolumes(t *testing.T) {
	_, r, _ := codingFixture(t)
	raw, err := json.Marshal(r.Profile)
	if err != nil {
		t.Fatal(err)
	}
	capability := "sha256:" + strings.Repeat("c", 64)
	template, err := NewCodingTemplate(raw, CodingTemplateConfig{
		CapabilityDigest: capability, References: r.Profile.Implementation,
		RuntimeClass: "operator-kata", NodeSelector: map[string]string{"thinkpixel.io/pool": "qualified"},
		UserID: 65532, GroupID: 65532, TempBytes: 1 << 30, QualificationDigest: "sha256:" + strings.Repeat("d", 64),
	}, func(runtimeprofile.Profile, CodingTemplateConfig) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	r.Profile, _, r.ProfileDigest, _, r.ImplementationDigest = template.Resolution()
	r.Workspace.WorkspaceID = r.Scope.SessionID
	r.Operation.Digest, err = RequestDigest(r)
	if err != nil {
		t.Fatal(err)
	}
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
	denied := false
	wsVolumes, err := workspacek8s.NewWSVolumeResolver(volumes, func(_ context.Context, got workspace.Attachment) ([]byte, error) {
		if got != a {
			t.Fatal("WS lookup lost reserved attachment scope")
		}
		if denied {
			return nil, errors.New("revoked")
		}
		return []byte(`{"kind":"kubernetes-pvc-v1","handle":"k8s-pvc-v1:uid-workspace","reference":{"namespace":"agents","claimName":"workspace","claimUid":"uid-workspace","mountPath":"/workspace","readOnly":false}}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := attachmentReaderFunc(func(_ context.Context, tenant primitives.ID, ref string) (workspace.Attachment, error) {
		if tenant != a.Scope.TenantID || ref != a.Reference {
			t.Fatal("wrong lookup")
		}
		return a, nil
	})
	resolve, err := AttachedBlueprintResolver(template, reader, wsVolumes, func(_ context.Context, scope sandbox.Scope, ref string) (string, error) {
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
	api := &testAPI{}
	kasServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		prefix := "/apis/agents.x-k8s.io/v1beta1/namespaces/agents/sandboxes"
		if req.URL.Path != prefix && req.URL.Path != prefix+"/ar-"+string(r.Scope.SandboxID) {
			t.Errorf("unexpected compute API request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
			return
		}
		api.ServeHTTP(w, req)
	}))
	defer kasServer.Close()
	kasClient, err := dynamic.NewForConfig(&rest.Config{Host: kasServer.URL})
	if err != nil {
		t.Fatal(err)
	}
	bindings := &testBindings{}
	provider, err := New(kasClient, bindings, "agents", resolve,
		WithNetworkEnforcer(func(context.Context, sandbox.AcquireRequest, string) error { return nil }),
		WithCapabilities(testCapabilities, capability))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := provider.Acquire(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	if api.creates != 1 {
		t.Fatal("acquisition replay created another Sandbox")
	}
	denied = true
	if _, err := provider.Acquire(context.Background(), r); err == nil {
		t.Fatal("replay bypassed current WS verification")
	}
	if api.creates != 1 {
		t.Fatal("denied resolution mutated compute")
	}
	// Release must remain possible after authority loss without resolving or
	// mutating WS storage. The storage HTTP fixture rejects every write.
	before := *bindings.b
	op := lifecycleOperation(r, "release", "release-ws")
	for range 2 {
		if err := provider.Release(context.Background(), r.Scope.TenantID, r.Scope.SandboxID, op); err != nil {
			t.Fatal("release WS-backed Sandbox", err)
		}
	}
	if api.deletes != 1 || api.object != nil || api.deleteOptions.Preconditions == nil ||
		*api.deleteOptions.Preconditions.UID != "provider-uid-1" ||
		*api.deleteOptions.PropagationPolicy != metav1.DeletePropagationForeground {
		t.Fatalf("release did not delete only the exact Sandbox: %+v", api.deleteOptions)
	}
	if !reflect.DeepEqual(before, *bindings.b) {
		t.Fatal("release changed durable Workspace attachment binding")
	}
	// A replacement Attempt needs a new reservation and current WS verification;
	// it cannot reuse the released Attempt's attachment scope.
	r.Scope.AttemptID = primitives.ID("01991e0b-7f42-7d68-8c2a-b684d1be7e39")
	r.Scope.SandboxID = r.Scope.AttemptID
	r.Scope.AttemptOrdinal++
	r.Operation.ID = string(r.Scope.AttemptID)
	r.Operation.Digest, err = RequestDigest(r)
	if err != nil {
		t.Fatal(err)
	}
	denied = false
	if _, err := resolve(context.Background(), r); err == nil {
		t.Fatal("replacement accepted old attachment reservation")
	}
	a.Scope.AttemptID, a.Scope.SandboxID = r.Scope.AttemptID, r.Scope.SandboxID
	a.Scope.AttemptOrdinal = r.Scope.AttemptOrdinal
	a.OperationID, a.RequestDigest = r.Scope.AttemptID, r.Operation.Digest
	replacement, err := New(kasClient, &testBindings{}, "agents", resolve,
		WithNetworkEnforcer(func(context.Context, sandbox.AcquireRequest, string) error { return nil }),
		WithCapabilities(testCapabilities, capability))
	if err != nil {
		t.Fatal(err)
	}
	denied = true
	if _, err := replacement.Acquire(context.Background(), r); err == nil || api.creates != 1 {
		t.Fatal("replacement bypassed fresh WS verification")
	}
	denied = false
	for range 2 {
		if _, err := replacement.Acquire(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	blueprint, err := resolve(context.Background(), r)
	if err != nil || api.creates != 2 || api.object == nil || blueprint.PodTemplate.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "workspace" {
		t.Fatal("replacement did not reattach original Workspace exactly once")
	}
}
