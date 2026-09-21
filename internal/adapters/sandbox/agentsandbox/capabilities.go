package agentsandbox

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const capabilityAnnotation = "thinkpixel.io/capability-digest"

const controllerImage = "registry.k8s.io/agent-sandbox/agent-sandbox-controller:v1.0.0@sha256:bdde1a3150bd385f7318c974c1516e880b4f826b6b51a3e7f127c2f8c95b55cd"

// DiscoveryPin is trusted installation evidence from the reviewed release, not
// values learned on first use. A changed CRD/controller requires requalification.
type DiscoveryPin struct {
	ServerVersion       string `json:"server_version"`
	CRDSpecDigest       string `json:"crd_spec_digest"`
	ControllerNamespace string `json:"controller_namespace"`
	ControllerName      string `json:"controller_name"`
}
type CapabilityResolver func(context.Context) (sandbox.Capabilities, error)

func WithCapabilities(resolve CapabilityResolver, expectedDigest string) Option {
	return func(p *KubernetesAgentSandboxProvider) { p.capabilities = resolve; p.capabilityDigest = expectedDigest }
}
func (p *KubernetesAgentSandboxProvider) Capabilities(ctx context.Context) (sandbox.Capabilities, error) {
	if p.capabilities == nil {
		return sandbox.Capabilities{}, sandbox.ErrUnsupported
	}
	c, err := p.capabilities(ctx)
	if err != nil {
		return sandbox.Capabilities{}, err
	}
	if !shaDigest.MatchString(c.Digest) || c.ProviderKind != "kubernetes-agent-sandbox" || c.ContractVersion != "v1" || c.ProviderVersion != upstreamVersion {
		return sandbox.Capabilities{}, sandbox.ErrUnsupported
	}
	return c.Clone(), nil
}
func (p *KubernetesAgentSandboxProvider) checkCapabilities(ctx context.Context, r sandbox.AcquireRequest) error {
	c, err := p.Capabilities(ctx)
	if err != nil {
		return err
	}
	if !shaDigest.MatchString(p.capabilityDigest) || c.Digest != p.capabilityDigest {
		return sandbox.ErrConflict
	}
	return c.ValidateProfile(r.Profile)
}

func adapterCapabilities() sandbox.Capabilities {
	return sandbox.Capabilities{ProviderKind: "kubernetes-agent-sandbox", ContractVersion: "v1", ProviderVersion: upstreamVersion, SupportsSuspend: true, SupportsResume: true,
		IsolationClasses: []string{"microvm-strong"}, Architectures: []string{"amd64", "arm64"}, VolumeAttachment: []string{"single-writer", "single-pod-writer"}, NetworkClasses: []string{"none", "thinkpixel-only", "restricted-development", "package-mirrors"}}
}

// NewCapabilityDiscovery performs bounded live discovery on every call, avoiding
// an authoritative cache. Use a REST client from the trusted HTTPS connection.
func NewCapabilityDiscovery(api rest.Interface, client dynamic.Interface, pin DiscoveryPin) (CapabilityResolver, error) {
	if api == nil || client == nil || !shaDigest.MatchString(pin.CRDSpecDigest) || !dnsName(pin.ControllerNamespace) || !dnsName(pin.ControllerName) || (pin.ServerVersion != "v1.36.4" && !strings.HasPrefix(pin.ServerVersion, "v1.36.4+")) {
		return nil, sandbox.ErrInvalid
	}
	return func(ctx context.Context) (sandbox.Capabilities, error) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		fail := sandbox.Capabilities{}
		var version struct {
			GitVersion string `json:"gitVersion"`
		}
		if err := discoveryJSON(ctx, api, "/version", &version); err != nil {
			return fail, err
		}
		if version.GitVersion != pin.ServerVersion {
			return fail, sandbox.ErrUnsupported
		}
		for _, required := range []struct {
			path, group string
			resources   map[string][]string
		}{
			{"/apis/agents.x-k8s.io/v1beta1", "agents.x-k8s.io/v1beta1", map[string][]string{"sandboxes": {"get", "create", "patch", "delete"}}},
			{"/api/v1", "v1", map[string][]string{"pods": {"get"}, "persistentvolumeclaims": {"get"}, "persistentvolumes": {"get"}}},
		} {
			var list metav1.APIResourceList
			if err := discoveryJSON(ctx, api, required.path, &list); err != nil {
				return fail, err
			}
			if list.GroupVersion != required.group {
				return fail, sandbox.ErrUnsupported
			}
			for name, verbs := range required.resources {
				found := 0
				for _, resource := range list.APIResources {
					if resource.Name == name {
						found++
						if resource.Namespaced != (name != "persistentvolumes") || name == "sandboxes" && resource.Kind != "Sandbox" {
							return fail, sandbox.ErrUnsupported
						}
						for _, verb := range verbs {
							ok := false
							for _, v := range resource.Verbs {
								if v == verb {
									ok = true
								}
							}
							if !ok {
								return fail, sandbox.ErrUnsupported
							}
						}
					}
				}
				if found != 1 {
					return fail, sandbox.ErrUnsupported
				}
			}
		}
		crd, err := client.Resource(schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}).Get(ctx, "sandboxes.agents.x-k8s.io", metav1.GetOptions{})
		if err != nil {
			return fail, providerError(err)
		}
		stored, found, e := unstructured.NestedStringSlice(crd.Object, "status", "storedVersions")
		conditions, _, _ := unstructured.NestedSlice(crd.Object, "status", "conditions")
		established := false
		for _, value := range conditions {
			if condition, ok := value.(map[string]any); ok && condition["type"] == "Established" && condition["status"] == "True" {
				established = true
			}
		}
		if e != nil || !found || len(stored) != 1 || stored[0] != "v1beta1" || !established || crd.GetName() != "sandboxes.agents.x-k8s.io" {
			return fail, sandbox.ErrUnsupported
		}
		raw, err := json.Marshal(crd.Object["spec"])
		if err != nil || crd.GetUID() == "" || crd.GetDeletionTimestamp() != nil || sandbox.Digest(raw) != pin.CRDSpecDigest {
			return fail, sandbox.ErrUnsupported
		}
		u, err := client.Resource(schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}).Namespace(pin.ControllerNamespace).Get(ctx, pin.ControllerName, metav1.GetOptions{})
		if err != nil {
			return fail, providerError(err)
		}
		dep := &appsv1.Deployment{}
		if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, dep) != nil || dep.UID == "" || dep.DeletionTimestamp != nil || dep.Status.ObservedGeneration != dep.Generation || dep.Spec.Replicas == nil || *dep.Spec.Replicas < 1 || dep.Status.Replicas != *dep.Spec.Replicas || dep.Status.AvailableReplicas != *dep.Spec.Replicas || dep.Status.UpdatedReplicas != *dep.Spec.Replicas || dep.Name != pin.ControllerName || dep.Namespace != pin.ControllerNamespace || len(dep.Spec.Template.Spec.Containers) != 1 || dep.Spec.Template.Spec.Containers[0].Image != controllerImage {
			return fail, sandbox.ErrUnsupported
		}
		c := adapterCapabilities()
		evidence, _ := json.Marshal(struct {
			Pin                  DiscoveryPin
			CRDUID               string
			ControllerUID        string
			ControllerGeneration int64
			Capabilities         sandbox.Capabilities
		}{pin, string(crd.GetUID()), string(dep.UID), dep.Generation, c})
		c.Digest = sandbox.Digest(evidence)
		return c, nil
	}, nil
}
func discoveryJSON(ctx context.Context, api rest.Interface, path string, target any) error {
	stream, err := api.Get().AbsPath(path).Stream(ctx)
	if err != nil {
		return providerError(err)
	}
	defer stream.Close()
	raw, err := io.ReadAll(io.LimitReader(stream, (1<<20)+1))
	if err != nil {
		return sandbox.ErrUnavailable
	}
	if len(raw) > 1<<20 || json.Unmarshal(raw, target) != nil {
		return sandbox.ErrIntegrity
	}
	return nil
}
