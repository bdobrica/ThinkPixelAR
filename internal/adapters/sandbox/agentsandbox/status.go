package agentsandbox

import (
	"context"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"strconv"
	"strings"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	core "sigs.k8s.io/agent-sandbox/api/v1beta1"
)

// EffectiveVerifier compares trusted observed Pod facts to the saved resolution,
// including independently qualified runtime/network/storage enforcement. Desired
// fields and sandbox reports alone cannot satisfy this gate.
type EffectiveVerifier func(context.Context, sandbox.Binding, *v1.Pod) (sandbox.EffectiveFacts, error)
type Option func(*KubernetesAgentSandboxProvider)

func WithEffectiveVerifier(v EffectiveVerifier) Option {
	return func(p *KubernetesAgentSandboxProvider) { p.verify = v }
}

func (p *KubernetesAgentSandboxProvider) getOwned(ctx context.Context, tenant, id primitives.ID) (sandbox.Binding, *core.Sandbox, error) {
	b, err := p.bindings.Get(ctx, tenant, id)
	if err != nil {
		return b, nil, err
	}
	if b.Request.Scope.TenantID != tenant || b.Request.Scope.SandboxID != id {
		return b, nil, sandbox.ErrIntegrity
	}
	name := "ar-" + string(id)
	if b.ProviderReference != "" && !strings.HasPrefix(b.ProviderReference, p.namespace+"/"+name+"/") {
		return b, nil, sandbox.ErrIntegrity
	}
	obj, err := p.client.Resource(sandboxResource).Namespace(p.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return b, nil, providerError(err)
	}
	var sb core.Sandbox
	if runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &sb) != nil {
		return b, nil, sandbox.ErrIntegrity
	}
	ref := p.namespace + "/" + name + "/" + string(sb.UID)
	if sb.UID == "" || b.ProviderReference != "" && b.ProviderReference != ref || sb.Annotations["thinkpixel.io/acquire-digest"] != b.Request.Operation.Digest || sb.Annotations["thinkpixel.io/attempt"] != string(b.Request.Scope.AttemptID) {
		return b, nil, sandbox.ErrIntegrity
	}
	return b, &sb, nil
}

func (p *KubernetesAgentSandboxProvider) Get(ctx context.Context, tenant, id primitives.ID) (sandbox.Status, error) {
	b, sb, err := p.getOwned(ctx, tenant, id)
	if err != nil {
		return sandbox.Status{State: sandbox.Unknown}, err
	}
	result := sandbox.Status{Handle: sandbox.Handle{SandboxID: id, ProviderKind: "kubernetes-agent-sandbox", ProviderReference: b.ProviderReference, ObservedAt: p.now().UTC()}, ProviderGeneration: strconv.FormatInt(sb.Generation, 10)}
	podObj, err := p.client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).Namespace(p.namespace).Get(ctx, sb.Name, metav1.GetOptions{})
	var pod *v1.Pod
	if err != nil && !apierrors.IsNotFound(err) {
		return sandbox.Status{State: sandbox.Unknown}, providerError(err)
	}
	if err == nil {
		pod = &v1.Pod{}
		if runtime.DefaultUnstructuredConverter.FromUnstructured(podObj.Object, pod) != nil || !ownedPod(pod, sb) {
			return sandbox.Status{State: sandbox.Unknown}, sandbox.ErrIntegrity
		}
	}
	result.State, result.Reason = translateState(sb, pod)
	if result.State == sandbox.Ready {
		expected, e := p.resolve(ctx, b.Request)
		if e != nil || !apiequality.Semantic.DeepEqual(expected, sb.Spec.SandboxBlueprint) {
			return sandbox.Status{State: sandbox.Unknown}, sandbox.ErrIntegrity
		}
		if p.verify == nil || pod == nil || b.ProviderReference == "" {
			result.State = sandbox.Unknown
			result.Reason = "EFFECTIVE_STATE_UNVERIFIED"
		} else {
			result.Effective, e = p.verify(ctx, b, pod.DeepCopy())
			if e != nil || !result.Effective.Verified {
				return sandbox.Status{State: sandbox.Unknown}, sandbox.ErrIntegrity
			}
		}
	}
	result.Handle.State = result.State
	return result, nil
}
func ownedPod(p *v1.Pod, s *core.Sandbox) bool {
	for _, o := range p.OwnerReferences {
		if o.UID == s.UID && o.Name == s.Name && o.Kind == "Sandbox" && o.APIVersion == core.GroupVersion.String() && o.Controller != nil && *o.Controller {
			return true
		}
	}
	return false
}
func translateState(s *core.Sandbox, p *v1.Pod) (sandbox.State, string) {
	if s.DeletionTimestamp != nil {
		return sandbox.Releasing, "DELETION_PENDING"
	}
	var ready, suspended, finished bool
	for _, c := range s.Status.Conditions {
		if c.ObservedGeneration != s.Generation {
			continue
		}
		if c.Status != metav1.ConditionTrue {
			continue
		}
		switch c.Type {
		case "Ready":
			ready = true
		case "Suspended":
			suspended = true
		case "Finished":
			finished = true
		}
	}
	if s.Spec.OperatingMode == core.SandboxOperatingModeSuspended {
		if suspended && p == nil {
			return sandbox.Suspended, "COMPUTE_SUSPENDED"
		}
		return sandbox.Suspending, "SUSPEND_PENDING"
	}
	if s.Spec.OperatingMode != "" && s.Spec.OperatingMode != core.SandboxOperatingModeRunning {
		return sandbox.Unknown, "UNRECOGNIZED_MODE"
	}
	if finished || p != nil && (p.Status.Phase == v1.PodFailed || p.Status.Phase == v1.PodSucceeded) {
		return sandbox.Failed, "COMPUTE_TERMINAL"
	}
	if suspended {
		return sandbox.Resuming, "RESUME_PENDING"
	}
	if ready && p != nil && p.DeletionTimestamp == nil && p.Status.Phase == v1.PodRunning {
		for _, c := range p.Status.Conditions {
			if c.Type == v1.PodReady && c.Status == v1.ConditionTrue {
				return sandbox.Ready, "INFRASTRUCTURE_READY"
			}
		}
	}
	return sandbox.Provisioning, "DEPENDENCIES_PENDING"
}
