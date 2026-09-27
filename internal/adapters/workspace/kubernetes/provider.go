package kubernetes

import (
	"context"
	"regexp"
	"strings"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
)

// StorageConfig is trusted operator configuration; it never comes from Workspace
// content. ConfigurationDigest pins the qualified driver/class/config tuple.
type StorageConfig struct {
	Namespace, StorageClass, StorageProfile, ConfigurationDigest string
	SinglePodWriter, Encrypted                                   bool
}

// QualifyStorage checks independent current storage evidence, including CSI,
// topology and encryption. A class name or PVC Bound flag alone is insufficient.
type QualifyStorage func(context.Context, StorageConfig) error

type Provider struct {
	client     dynamic.Interface
	operations workspace.Operations
	config     StorageConfig
	qualify    QualifyStorage
}

var _ workspace.Provider = (*Provider)(nil)
var claimsResource = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}
var storageDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func NewProvider(client dynamic.Interface, operations workspace.Operations, c StorageConfig, qualify QualifyStorage) (*Provider, error) {
	if client == nil || operations == nil || qualify == nil || c.Namespace == "" || len(validation.IsDNS1123Label(c.Namespace)) != 0 || c.StorageClass == "" || len(validation.IsDNS1123Subdomain(c.StorageClass)) != 0 || c.StorageProfile == "" || !storageDigest.MatchString(c.ConfigurationDigest) {
		return nil, workspace.ErrInvalid
	}
	return &Provider{client: client, operations: operations, config: c, qualify: qualify}, nil
}
func (p *Provider) Capabilities(ctx context.Context) (workspace.Capabilities, error) {
	if err := p.qualify(ctx, p.config); err != nil {
		return workspace.Capabilities{}, workspace.ErrUnsupported
	}
	return workspace.Capabilities{Create: true, Delete: true, SingleWriter: true, SinglePodWriter: p.config.SinglePodWriter}, nil
}
func validStorageRequest(r workspace.CreateRequest) bool {
	for _, id := range []primitives.ID{r.TenantID, r.SessionID, r.WorkspaceID, r.Operation.ID} {
		if _, err := primitives.ParseID(string(id)); err != nil {
			return false
		}
	}
	return r.CapacityBytes > 0 && r.StateCapacityBytes > 0 && r.StateCapacityBytes <= r.CapacityBytes && storageDigest.MatchString(r.ConfigurationDigest) && r.StorageProfile != "" && (r.AccessMode == "single-writer" || r.AccessMode == "single-pod-writer") && r.Operation.Digest == workspace.CreateDigest(r)
}
func (p *Provider) Create(ctx context.Context, r workspace.CreateRequest) (workspace.Handle, error) {
	if !validStorageRequest(r) {
		return workspace.Handle{}, workspace.ErrInvalid
	}
	return p.run(ctx, workspace.Command{Kind: "create", TenantID: r.TenantID, WorkspaceID: r.WorkspaceID, Operation: r.Operation, Create: r})
}
func (p *Provider) Get(ctx context.Context, tenant, id primitives.ID) (workspace.Handle, error) {
	return p.run(ctx, workspace.Command{Kind: "get", TenantID: tenant, WorkspaceID: id})
}
func (p *Provider) Delete(ctx context.Context, tenant, id primitives.ID, op workspace.Operation) (workspace.Handle, error) {
	if _, err := primitives.ParseID(string(op.ID)); err != nil || op.Digest != workspace.DeleteDigest(tenant, id, op) {
		return workspace.Handle{}, workspace.ErrInvalid
	}
	return p.run(ctx, workspace.Command{Kind: "delete", TenantID: tenant, WorkspaceID: id, Operation: op})
}
func (p *Provider) run(ctx context.Context, cmd workspace.Command) (workspace.Handle, error) {
	var result workspace.Handle
	for _, id := range []primitives.ID{cmd.TenantID, cmd.WorkspaceID} {
		if _, err := primitives.ParseID(string(id)); err != nil {
			return result, workspace.ErrInvalid
		}
	}
	err := p.operations.Do(ctx, cmd, func(ctx context.Context, saved workspace.Reservation, bind func(string, string) error) error {
		r := saved.Request
		if !validStorageRequest(r) || r.TenantID != cmd.TenantID || r.WorkspaceID != cmd.WorkspaceID || cmd.Kind == "create" && r != cmd.Create {
			return workspace.ErrIntegrity
		}
		if r.StorageProfile != p.config.StorageProfile || r.ConfigurationDigest != p.config.ConfigurationDigest || r.EncryptionRequired && !p.config.Encrypted || r.AccessMode == "single-pod-writer" && !p.config.SinglePodWriter {
			return workspace.ErrUnsupported
		}
		if err := p.qualify(ctx, p.config); err != nil {
			return workspace.ErrUnsupported
		}
		result = workspace.Handle{WorkspaceID: r.WorkspaceID, ProviderKind: "kubernetes", State: "BOUND"}
		for i, ref := range []string{saved.WorkspaceReference, saved.StateReference} {
			role := "workspace"
			capacity := r.CapacityBytes
			if i == 1 {
				role = "state"
				capacity = r.StateCapacityBytes
			}
			name := "ar-" + role + "-" + string(r.WorkspaceID)
			if ref != "" {
				parts := strings.Split(ref, "/")
				if len(parts) != 3 || parts[0] != p.config.Namespace || parts[1] != name || parts[2] == "" {
					return workspace.ErrIntegrity
				}
			}
			api := p.client.Resource(claimsResource).Namespace(p.config.Namespace)
			obj, err := api.Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				if cmd.Kind == "create" && ref == "" {
					desired := p.claim(r, role, name, capacity)
					raw, e := runtime.DefaultUnstructuredConverter.ToUnstructured(desired)
					if e != nil {
						return workspace.ErrInvalid
					}
					obj, err = api.Create(ctx, &unstructured.Unstructured{Object: raw}, metav1.CreateOptions{})
					if apierrors.IsAlreadyExists(err) {
						obj, err = api.Get(ctx, name, metav1.GetOptions{})
					}
				} else if cmd.Kind == "delete" {
					continue
				} else {
					return workspace.ErrNotFound
				}
			}
			if err != nil {
				return storageError(err)
			}
			var pvc v1.PersistentVolumeClaim
			if runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &pvc) != nil || !p.matches(&pvc, r, role, name, capacity) {
				return workspace.ErrIntegrity
			}
			observed := p.config.Namespace + "/" + name + "/" + string(pvc.UID)
			if ref != "" && observed != ref {
				return workspace.ErrIntegrity
			}
			// Persist each identity independently: a partial pair remains reconcilable.
			if ref == "" {
				if bind == nil {
					return workspace.ErrIntegrity
				}
				if err := bind(role, observed); err != nil {
					return err
				}
			}
			if i == 0 {
				result.WorkspaceReference = observed
			} else {
				result.StateReference = observed
			}
			if cmd.Kind == "delete" {
				result.State = "DELETING"
				if pvc.DeletionTimestamp == nil {
					uid, rv := pvc.UID, pvc.ResourceVersion
					if rv == "" {
						return workspace.ErrIntegrity
					}
					err = api.Delete(ctx, name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}})
					if err != nil && !apierrors.IsNotFound(err) {
						return storageError(err)
					}
				}
			} else {
				if pvc.DeletionTimestamp != nil || pvc.Status.Phase == v1.ClaimLost {
					return workspace.ErrConflict
				}
				if pvc.Status.Phase != v1.ClaimBound {
					result.State = "PENDING"
				} else if pvc.Spec.VolumeName == "" || pvc.Status.Capacity.Storage().Value() != capacity {
					return workspace.ErrIntegrity
				}
			}
		}
		if cmd.Kind == "delete" && result.State == "BOUND" {
			result.State = "ABSENT"
		}
		return nil
	})
	if err != nil {
		return workspace.Handle{}, err
	}
	return result, nil
}
func (p *Provider) claim(r workspace.CreateRequest, role, name string, capacity int64) *v1.PersistentVolumeClaim {
	mode := v1.ReadWriteOnce
	if r.AccessMode == "single-pod-writer" {
		mode = v1.ReadWriteOncePod
	}
	fs := v1.PersistentVolumeFilesystem
	class := p.config.StorageClass
	return &v1.PersistentVolumeClaim{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"}, ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: p.config.Namespace, Annotations: map[string]string{"thinkpixel.io/storage-owner": "ar", "thinkpixel.io/tenant": string(r.TenantID), "thinkpixel.io/session": string(r.SessionID), "thinkpixel.io/workspace": string(r.WorkspaceID), "thinkpixel.io/storage-role": role, "thinkpixel.io/create-operation": string(r.Operation.ID), "thinkpixel.io/create-digest": r.Operation.Digest, "thinkpixel.io/storage-config": r.ConfigurationDigest}}, Spec: v1.PersistentVolumeClaimSpec{StorageClassName: &class, VolumeMode: &fs, AccessModes: []v1.PersistentVolumeAccessMode{mode}, Resources: v1.VolumeResourceRequirements{Requests: v1.ResourceList{v1.ResourceStorage: *resource.NewQuantity(capacity, resource.BinarySI)}}}}
}
func (p *Provider) matches(pvc *v1.PersistentVolumeClaim, r workspace.CreateRequest, role, name string, capacity int64) bool {
	want := p.claim(r, role, name, capacity)
	if pvc.Name != name || pvc.Namespace != p.config.Namespace || pvc.UID == "" || len(pvc.OwnerReferences) != 0 || pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != p.config.StorageClass || pvc.Spec.VolumeMode != nil && *pvc.Spec.VolumeMode != v1.PersistentVolumeFilesystem || len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != want.Spec.AccessModes[0] || pvc.Spec.Resources.Requests.Storage().Value() != capacity || pvc.Spec.DataSource != nil || pvc.Spec.DataSourceRef != nil || pvc.Spec.Selector != nil || pvc.Spec.VolumeAttributesClassName != nil {
		return false
	}
	for key, value := range want.Annotations {
		if pvc.Annotations[key] != value {
			return false
		}
	}
	return true
}
func storageError(err error) error {
	if apierrors.IsNotFound(err) {
		return workspace.ErrNotFound
	}
	if apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err) {
		return workspace.ErrConflict
	}
	return workspace.ErrUnavailable
}
