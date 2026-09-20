package agentsandbox

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	core "sigs.k8s.io/agent-sandbox/api/v1beta1"
	extensions "sigs.k8s.io/agent-sandbox/extensions/api/v1beta1"
)

const upstreamVersion = "v1.0.0"

var (
	sandboxResource  = schema.GroupVersionResource{Group: core.GroupVersion.Group, Version: core.GroupVersion.Version, Resource: "sandboxes"}
	templateResource = schema.GroupVersionResource{Group: extensions.GroupVersion.Group, Version: extensions.GroupVersion.Version, Resource: "sandboxtemplates"}
	claimResource    = schema.GroupVersionResource{Group: extensions.GroupVersion.Group, Version: extensions.GroupVersion.Version, Resource: "sandboxclaims"}
)

// apiScheme registers only the pinned beta APIs; native types remain private
// to this adapter. No controller, router, or upstream process is embedded.
func apiScheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	if err := core.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := extensions.AddToScheme(s); err != nil {
		return nil, err
	}
	return s, nil
}
