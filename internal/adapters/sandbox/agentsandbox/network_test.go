package agentsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	v1 "k8s.io/api/core/v1"
	networking "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func networkPolicyFixture() *networking.NetworkPolicy {
	tcp := v1.ProtocolTCP
	port := intstr.FromInt32(7443)
	return &networking.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "offline", Namespace: "sandboxes", UID: "policy-uid"}, Spec: networking.NetworkPolicySpec{PodSelector: metav1.LabelSelector{}, PolicyTypes: []networking.PolicyType{networking.PolicyTypeIngress, networking.PolicyTypeEgress}, Egress: []networking.NetworkPolicyEgressRule{{To: []networking.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "ar-control"}}, PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "agentd-transport"}}}}, Ports: []networking.NetworkPolicyPort{{Protocol: &tcp, Port: &port}}}}}}
}

func TestNetworkPolicyRequiresExactQualifiedNamespace(t *testing.T) {
	request := acquireFixture(t)
	expected := networkPolicyFixture()
	for _, scenario := range []string{"valid", "added-policy", "replacement", "widened", "missing", "pagination", "unqualified", "wrong-namespace", "wrong-digest"} {
		t.Run(scenario, func(t *testing.T) {
			policy := expected.DeepCopy()
			result := networking.NetworkPolicyList{TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicyList"}, Items: []networking.NetworkPolicy{*policy}}
			switch scenario {
			case "added-policy":
				result.Items = append(result.Items, *policy)
			case "replacement":
				result.Items[0].UID = "replacement-uid"
			case "widened":
				result.Items[0].Spec.Egress[0].To = nil
			case "missing":
				result.Items = nil
			case "pagination":
				result.Continue = "more"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/apis/networking.k8s.io/v1/namespaces/sandboxes/networkpolicies" {
					t.Error("unexpected API call")
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(result)
			}))
			defer server.Close()
			client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			qualified := false
			enforce, err := NewNamespaceNetworkEnforcer(client, NamespaceNetworkBinding{request.ProfileDigest, request.ImplementationDigest, expected}, func(context.Context, sandbox.AcquireRequest, *networking.NetworkPolicy) error {
				qualified = true
				if scenario == "unqualified" {
					return errors.New("no physical proof")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			r := request
			namespace := "sandboxes"
			if scenario == "wrong-namespace" {
				namespace = "other"
			}
			if scenario == "wrong-digest" {
				r.ProfileDigest = "changed"
			}
			err = enforce(context.Background(), r, namespace)
			if scenario == "valid" {
				if err != nil || !qualified {
					t.Fatal("qualified policy rejected", err)
				}
			} else if err == nil {
				t.Fatal("unsafe network admitted")
			}
		})
	}
}

func TestNetworkPolicyRejectsUnboundedSelectors(t *testing.T) {
	for _, mutate := range []func(*networking.NetworkPolicy){
		func(p *networking.NetworkPolicy) { p.Spec.Egress[0].To = nil },
		func(p *networking.NetworkPolicy) { p.Spec.Egress[0].Ports = nil },
		func(p *networking.NetworkPolicy) { p.Spec.Egress[0].To[0].NamespaceSelector = nil },
		func(p *networking.NetworkPolicy) { p.Spec.Egress[0].To[0].PodSelector = nil },
		func(p *networking.NetworkPolicy) {
			p.Spec.Egress[0].To[0].IPBlock = &networking.IPBlock{CIDR: "0.0.0.0/0"}
		},
		func(p *networking.NetworkPolicy) { p.Spec.Ingress = []networking.NetworkPolicyIngressRule{{}} },
		func(p *networking.NetworkPolicy) {
			p.Spec.PodSelector.MatchLabels = map[string]string{"optional": "label"}
		},
		func(p *networking.NetworkPolicy) { p.Spec.Egress[0].Ports[0].Port = nil },
	} {
		p := networkPolicyFixture()
		mutate(p)
		if boundedPolicy(p) {
			t.Fatal("unbounded policy accepted")
		}
	}
}

func TestNetworkEnforcementPrecedesComputeAfterReservation(t *testing.T) {
	api, bindings := &testAPI{}, &testBindings{}
	provider := providerFixture(t, api, bindings)
	r := acquireFixture(t)
	provider.network = nil
	if _, err := provider.Acquire(context.Background(), r); !errors.Is(err, sandbox.ErrUnsupported) || bindings.b != nil {
		t.Fatal("missing enforcer reserved compute", err)
	}
	calls := 0
	provider.network = func(_ context.Context, got sandbox.AcquireRequest, namespace string) error {
		calls++
		if bindings.b == nil || bindings.b.Request.Operation != got.Operation || namespace != "sandboxes" {
			t.Error("enforcement before durable reservation or wrong scope")
		}
		return sandbox.ErrIntegrity
	}
	if _, err := provider.Acquire(context.Background(), r); !errors.Is(err, sandbox.ErrIntegrity) || api.object != nil || calls != 1 {
		t.Fatal("compute escaped failed enforcement", err)
	}
	provider.network = func(context.Context, sandbox.AcquireRequest, string) error { return nil }
	if _, err := provider.Acquire(context.Background(), r); err != nil {
		t.Fatal("corrected policy could not replay reservation", err)
	}
}
