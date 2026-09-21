package agentsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func TestCapabilityDiscoveryRejectsDriftAndMissingAPIs(t *testing.T) {
	for _, failure := range []string{"", "server", "verb", "scope", "schema", "alpha storage", "unestablished", "controller", "rollout", "oversize", "unavailable"} {
		t.Run(failure, func(t *testing.T) {
			spec := map[string]any{"group": "agents.x-k8s.io"}
			raw, _ := json.Marshal(spec)
			pin := DiscoveryPin{ServerVersion: "v1.36.4+k3s1", CRDSpecDigest: sandbox.Digest(raw), ControllerNamespace: "system", ControllerName: "controller"}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var response any
				switch r.URL.Path {
				case "/version":
					if failure == "oversize" {
						_, _ = w.Write([]byte(strings.Repeat("x", (1<<20)+2)))
						return
					}
					if failure == "unavailable" {
						w.WriteHeader(503)
						return
					}
					version := pin.ServerVersion
					if failure == "server" {
						version = "v1.37.0"
					}
					response = map[string]any{"gitVersion": version}
				case "/apis/agents.x-k8s.io/v1beta1":
					verbs := []string{"get", "create", "patch", "delete"}
					if failure == "verb" {
						verbs = []string{"get"}
					}
					response = map[string]any{"groupVersion": "agents.x-k8s.io/v1beta1", "resources": []any{map[string]any{"name": "sandboxes", "kind": "Sandbox", "namespaced": failure != "scope", "verbs": verbs}}}
				case "/api/v1":
					resources := []any{}
					for _, name := range []string{"pods", "persistentvolumeclaims", "persistentvolumes"} {
						resources = append(resources, map[string]any{"name": name, "namespaced": name != "persistentvolumes", "verbs": []string{"get"}})
					}
					response = map[string]any{"groupVersion": "v1", "resources": resources}
				case "/apis/apiextensions.k8s.io/v1/customresourcedefinitions/sandboxes.agents.x-k8s.io":
					actual := spec
					if failure == "schema" {
						actual = map[string]any{"group": "changed"}
					}
					stored := []string{"v1beta1"}
					if failure == "alpha storage" {
						stored = append(stored, "v1alpha1")
					}
					established := "True"
					if failure == "unestablished" {
						established = "False"
					}
					response = map[string]any{"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition", "metadata": map[string]any{"name": "sandboxes.agents.x-k8s.io", "uid": "crd-uid"}, "spec": actual, "status": map[string]any{"storedVersions": stored, "conditions": []any{map[string]any{"type": "Established", "status": established}}}}
				case "/apis/apps/v1/namespaces/system/deployments/controller":
					image := controllerImage
					if failure == "controller" {
						image = "example.invalid/unreviewed:latest"
					}
					updated := 1
					if failure == "rollout" {
						updated = 0
					}
					response = map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "controller", "namespace": "system", "uid": "controller-uid", "generation": 1}, "spec": map[string]any{"replicas": 1, "template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "controller", "image": image}}}}}, "status": map[string]any{"observedGeneration": 1, "replicas": 1, "availableReplicas": 1, "updatedReplicas": updated}}
				default:
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			config := &rest.Config{Host: server.URL}
			api, err := rest.UnversionedRESTClientFor(dynamic.ConfigFor(config))
			if err != nil {
				t.Fatal(err)
			}
			client, err := dynamic.NewForConfig(config)
			if err != nil {
				t.Fatal(err)
			}
			discover, err := NewCapabilityDiscovery(api, client, pin)
			if err != nil {
				t.Fatal(err)
			}
			got, err := discover(context.Background())
			if (err == nil) != (failure == "") {
				t.Fatal(got, err)
			}
			if failure == "" {
				if !shaDigest.MatchString(got.Digest) || got.SupportsWarmPool {
					t.Fatal("invalid evidence")
				}
				request := acquireFixture(t)
				if err = got.ValidateProfile(request.Profile); err != nil {
					t.Fatal(err)
				}
				request.Profile.Lifecycle.WarmPoolEligible = true
				if got.ValidateProfile(request.Profile) == nil {
					t.Fatal("warm pool admitted")
				}
				got.Architectures[0] = "changed"
				again, e := discover(context.Background())
				if e != nil || again.Architectures[0] == "changed" {
					t.Fatal("mutable discovery")
				}
				cancelled, cancel := context.WithCancel(context.Background())
				cancel()
				if _, e = discover(cancelled); e == nil {
					t.Fatal("cancelled discovery succeeded")
				}
			}
		})
	}
}

func TestAcquireCapabilityFailurePrecedesReservation(t *testing.T) {
	for _, mode := range []string{"missing", "outage", "changed", "stale-template"} {
		t.Run(mode, func(t *testing.T) {
			api, bindings := &testAPI{}, &testBindings{}
			provider := providerFixture(t, api, bindings)
			switch mode {
			case "missing":
				provider.capabilities = nil
			case "outage":
				provider.capabilities = func(context.Context) (sandbox.Capabilities, error) {
					return sandbox.Capabilities{}, sandbox.ErrUnavailable
				}
			case "stale-template":
				provider.capabilityDigest = "sha256:" + strings.Repeat("d", 64)
				provider.capabilities = func(context.Context) (sandbox.Capabilities, error) {
					c := adapterCapabilities()
					c.Digest = provider.capabilityDigest
					return c, nil
				}
			case "changed":
				provider.capabilityDigest = "sha256:" + strings.Repeat("d", 64)
			}
			_, err := provider.Acquire(context.Background(), acquireFixture(t))
			if err == nil || api.creates != 0 || bindings.b != nil {
				t.Fatal("capability failure reserved compute")
			}
			if mode == "outage" && !errors.Is(err, sandbox.ErrUnavailable) {
				t.Fatal("outage changed meaning", err)
			}
		})
	}
}
