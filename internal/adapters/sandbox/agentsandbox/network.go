package agentsandbox

import (
	"context"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	networking "k8s.io/api/networking/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// NetworkEnforcer checks enforcement before compute creation/resumption and
// readiness. Implementations may prepare policy only after durable reservation.
// A successful call requires physical enforcement evidence, not just manifests.
type NetworkEnforcer func(context.Context, sandbox.AcquireRequest, string) error

func WithNetworkEnforcer(enforce NetworkEnforcer) Option {
	return func(p *KubernetesAgentSandboxProvider) { p.network = enforce }
}

func (p *KubernetesAgentSandboxProvider) checkNetwork(ctx context.Context, r sandbox.AcquireRequest) error {
	if p.network == nil {
		if r.Profile.IsolationClass != "container-standard" {
			return sandbox.ErrUnsupported
		}
		return nil
	}
	return p.network(ctx, r, p.namespace)
}

// NetworkQualification verifies the operator-controlled namespace, trusted peer
// identity, CNI and protected-destination enforcement for this exact resolution.
// In particular, Pod selectors alone do not prove DNS/service identity or deny
// host-network and node-local bypasses. Evidence must remain fresh at every call.
type NetworkQualification func(context.Context, sandbox.AcquireRequest, *networking.NetworkPolicy) error

// NamespaceNetworkBinding describes a dedicated namespace with one installed
// policy. Separate network classes require separate namespace/provider instances.
// The selected policy and digests are trusted operator inputs, never workload input.
type NamespaceNetworkBinding struct {
	ProfileDigest        string
	ImplementationDigest string
	Policy               *networking.NetworkPolicy
}

// NewNamespaceNetworkEnforcer verifies an operator-installed policy. It never
// widens rules or deletes shared policy during sandbox teardown. Additional
// policies are rejected because Kubernetes combines their allowances additively.
func NewNamespaceNetworkEnforcer(client dynamic.Interface, binding NamespaceNetworkBinding, qualify NetworkQualification) (NetworkEnforcer, error) {
	if client == nil || qualify == nil || !shaDigest.MatchString(binding.ProfileDigest) || !shaDigest.MatchString(binding.ImplementationDigest) || !boundedPolicy(binding.Policy) {
		return nil, sandbox.ErrInvalid
	}
	expected := binding.Policy.DeepCopy()
	return func(ctx context.Context, r sandbox.AcquireRequest, namespace string) error {
		if namespace != expected.Namespace {
			return sandbox.ErrIntegrity
		}
		if r.ProfileDigest != binding.ProfileDigest || r.ImplementationDigest != binding.ImplementationDigest {
			return sandbox.ErrConflict
		}
		n := r.Profile.Network
		if !n.DefaultDenyIngress || !n.DefaultDenyEgress || !n.DenyCloudMetadata || !n.DenyKubernetesAPI || n.DNSPolicy != "trusted-only" {
			return sandbox.ErrUnsupported
		}
		switch n.Profile {
		case "none", "thinkpixel-only", "restricted-development", "package-mirrors":
		default:
			return sandbox.ErrUnsupported
		}
		result, err := client.Resource(schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}).Namespace(expected.Namespace).List(ctx, metav1.ListOptions{Limit: 2})
		if err != nil {
			return providerError(err)
		}
		if len(result.Items) != 1 || result.GetContinue() != "" {
			return sandbox.ErrIntegrity
		}
		var observed networking.NetworkPolicy
		if runtime.DefaultUnstructuredConverter.FromUnstructured(result.Items[0].Object, &observed) != nil || observed.Name != expected.Name || observed.Namespace != expected.Namespace || observed.UID != expected.UID || observed.DeletionTimestamp != nil || !apiequality.Semantic.DeepEqual(observed.Spec, expected.Spec) {
			return sandbox.ErrIntegrity
		}
		// The qualifier also binds each permitted peer to the selected class: for
		// "none", only mandatory DNS and AR transport are eligible destinations.
		if err := qualify(ctx, r, observed.DeepCopy()); err != nil {
			return sandbox.ErrIntegrity
		}
		return nil
	}, nil
}

func boundedPolicy(p *networking.NetworkPolicy) bool {
	if p == nil || p.Name == "" || p.Namespace == "" || p.UID == "" || p.DeletionTimestamp != nil || len(p.Spec.PodSelector.MatchLabels) != 0 || len(p.Spec.PodSelector.MatchExpressions) != 0 || len(p.Spec.Ingress) != 0 || len(p.Spec.PolicyTypes) != 2 {
		return false
	}
	ingress, egress := false, false
	for _, kind := range p.Spec.PolicyTypes {
		switch kind {
		case networking.PolicyTypeIngress:
			ingress = true
		case networking.PolicyTypeEgress:
			egress = true
		}
	}
	if !ingress || !egress {
		return false
	}
	for _, rule := range p.Spec.Egress {
		if len(rule.To) == 0 || len(rule.Ports) == 0 {
			return false
		}
		for _, peer := range rule.To {
			if peer.IPBlock != nil || peer.NamespaceSelector == nil || peer.PodSelector == nil || len(peer.NamespaceSelector.MatchExpressions) != 0 || len(peer.NamespaceSelector.MatchLabels) != 1 || peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] == "" || len(peer.PodSelector.MatchLabels) == 0 || len(peer.PodSelector.MatchExpressions) != 0 {
				return false
			}
		}
		for _, port := range rule.Ports {
			if port.Port == nil || port.Port.Type != 0 || port.Port.IntVal < 1 || port.Port.IntVal > 65535 || port.EndPort != nil || port.Protocol == nil || (*port.Protocol != "TCP" && *port.Protocol != "UDP") {
				return false
			}
		}
	}
	return true
}
