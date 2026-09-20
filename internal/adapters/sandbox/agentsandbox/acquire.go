package agentsandbox

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	core "sigs.k8s.io/agent-sandbox/api/v1beta1"
)

// BlueprintResolver is trusted adapter configuration: it must resolve the exact
// approved profile/runtime/attachment/bootstrap references, enforce all required
// capabilities and return a deterministic blueprint. It may not create storage
// or grant authority. Profile/template implementation is separate from acquisition.
type BlueprintResolver func(context.Context, sandbox.AcquireRequest) (core.SandboxBlueprint, error)

type KubernetesAgentSandboxProvider struct {
	client    dynamic.Interface
	bindings  sandbox.BindingStore
	resolve   BlueprintResolver
	namespace string
	now       func() time.Time
	verify    EffectiveVerifier
}

func New(client dynamic.Interface, bindings sandbox.BindingStore, namespace string, resolve BlueprintResolver, options ...Option) (*KubernetesAgentSandboxProvider, error) {
	if client == nil || bindings == nil || resolve == nil || !regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`).MatchString(namespace) {
		return nil, sandbox.ErrInvalid
	}
	p := &KubernetesAgentSandboxProvider{client: client, bindings: bindings, namespace: namespace, resolve: resolve, now: time.Now}
	for _, option := range options {
		option(p)
	}
	return p, nil
}

// RequestDigest hashes the complete normalized request, excluding only its
// supplied digest. Callers persist this identity before retrying an operation.
func RequestDigest(r sandbox.AcquireRequest) (string, error) {
	return sandbox.RequestDigest(r)
}

func (p *KubernetesAgentSandboxProvider) Acquire(ctx context.Context, r sandbox.AcquireRequest) (sandbox.Handle, error) {
	if !validAcquire(r) || !r.Deadline.After(p.now()) {
		return sandbox.Handle{}, sandbox.ErrInvalid
	}
	digest, err := RequestDigest(r)
	if err != nil || digest != r.Operation.Digest {
		return sandbox.Handle{}, sandbox.ErrConflict
	}
	// Resolve and validate before reserving or making a Kubernetes mutation.
	blueprint, err := p.resolve(ctx, r)
	if err != nil {
		return sandbox.Handle{}, sandbox.ErrUnsupported
	}
	if blueprint.Service == nil || *blueprint.Service || len(blueprint.VolumeClaimTemplates) != 0 || len(blueprint.PodTemplate.Spec.Containers) == 0 {
		return sandbox.Handle{}, sandbox.ErrIntegrity
	}
	binding, err := p.bindings.Reserve(ctx, r)
	if err != nil {
		return sandbox.Handle{}, err
	}
	if !reflect.DeepEqual(binding.Request, r) {
		return sandbox.Handle{}, sandbox.ErrConflict
	}
	name := "ar-" + string(r.Scope.SandboxID)
	annotations := map[string]string{"thinkpixel.io/acquire-digest": digest, "thinkpixel.io/attempt": string(r.Scope.AttemptID)}
	sb := &core.Sandbox{TypeMeta: metav1.TypeMeta{APIVersion: core.GroupVersion.String(), Kind: "Sandbox"}, ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: p.namespace, Annotations: annotations}, Spec: core.SandboxSpec{SandboxBlueprint: blueprint, OperatingMode: core.SandboxOperatingModeRunning, Lifecycle: core.Lifecycle{ShutdownTime: &metav1.Time{Time: r.Deadline}}}}
	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(sb)
	if err != nil {
		return sandbox.Handle{}, sandbox.ErrInvalid
	}
	resources := p.client.Resource(sandboxResource).Namespace(p.namespace)
	existing, err := resources.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if binding.ProviderReference != "" {
			return sandbox.Handle{}, sandbox.ErrNotFound
		}
		existing, err = resources.Create(ctx, &unstructured.Unstructured{Object: object}, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			existing, err = resources.Get(ctx, name, metav1.GetOptions{})
		}
	}
	if err != nil {
		return sandbox.Handle{}, providerError(err)
	}
	if existing.GetAnnotations()["thinkpixel.io/acquire-digest"] != digest || existing.GetAnnotations()["thinkpixel.io/attempt"] != string(r.Scope.AttemptID) || existing.GetUID() == "" || existing.GetDeletionTimestamp() != nil {
		return sandbox.Handle{}, sandbox.ErrConflict
	}
	var observed core.Sandbox
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(existing.Object, &observed); err != nil || !reflect.DeepEqual(observed.Spec.SandboxBlueprint, blueprint) {
		return sandbox.Handle{}, sandbox.ErrIntegrity
	}
	reference := p.namespace + "/" + name + "/" + string(existing.GetUID())
	if binding.ProviderReference != "" && binding.ProviderReference != reference {
		return sandbox.Handle{}, sandbox.ErrIntegrity
	}
	if err = p.bindings.BindReference(ctx, r.Scope.TenantID, r.Scope.SandboxID, reference); err != nil {
		return sandbox.Handle{}, err
	}
	return sandbox.Handle{SandboxID: r.Scope.SandboxID, ProviderKind: "kubernetes-agent-sandbox", ProviderReference: reference, State: sandbox.Provisioning, ObservedAt: p.now().UTC()}, nil
}

var imageDigest = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/-]*@sha256:[0-9a-f]{64}$`)
var shaDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func validAcquire(r sandbox.AcquireRequest) bool {
	for _, id := range []primitives.ID{r.Scope.TenantID, r.Scope.SessionID, r.Scope.ExecutionID, r.Scope.AttemptID, r.Scope.SandboxID} {
		if _, err := primitives.ParseID(string(id)); err != nil {
			return false
		}
	}
	return r.Scope.Generation > 0 && r.Scope.AttemptOrdinal > 0 && len(r.Operation.ID) > 0 && len(r.Operation.ID) <= 128 && imageDigest.MatchString(r.Runtime.Image) && len(r.Runtime.Entrypoint) > 0 && (r.Runtime.Architecture == "amd64" || r.Runtime.Architecture == "arm64") && shaDigest.MatchString(r.ProfileDigest) && shaDigest.MatchString(r.ImplementationDigest) && r.Profile.ValidateConstraints() == nil && r.BootstrapReference != "" && r.Workspace.Reference != ""
}
func providerError(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded), apierrors.IsTimeout(err), apierrors.IsServerTimeout(err):
		return sandbox.ErrTimeout
	case apierrors.IsNotFound(err):
		return sandbox.ErrNotFound
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return sandbox.ErrPermission
	case apierrors.IsConflict(err), apierrors.IsAlreadyExists(err):
		return sandbox.ErrConflict
	case apierrors.IsInvalid(err), apierrors.IsBadRequest(err):
		return sandbox.ErrInvalid
	default:
		return sandbox.ErrUnavailable
	}
}
