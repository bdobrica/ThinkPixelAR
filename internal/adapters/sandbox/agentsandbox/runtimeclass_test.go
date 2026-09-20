package agentsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	v1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func runtimeFixture(t *testing.T) (*KataRuntimeResolver, runtimeprofile.Profile, *nodev1.RuntimeClass) {
	t.Helper()
	p := acquireFixture(t).Profile
	m := KataRuntimeMapping{Reference: p.Implementation.IsolationRuntimeRef, KataVersion: "3.31.0", RuntimeClass: "operator-kata", Handler: "operator-kata", Architectures: []string{"amd64"}, NodeSelector: map[string]string{"thinkpixel.io/qualified": "yes"}, OverheadCPUMillis: 250, OverheadMemoryBytes: 1 << 30, QualificationReference: "reviewed-fixture"}
	rc := &nodev1.RuntimeClass{TypeMeta: metav1.TypeMeta{APIVersion: "node.k8s.io/v1", Kind: "RuntimeClass"}, ObjectMeta: metav1.ObjectMeta{Name: m.RuntimeClass, UID: "runtime-uid"}, Handler: m.Handler, Scheduling: &nodev1.Scheduling{NodeSelector: map[string]string{"thinkpixel.io/qualified": "yes"}}, Overhead: &nodev1.Overhead{PodFixed: v1.ResourceList{v1.ResourceCPU: resource.MustParse("250m"), v1.ResourceMemory: resource.MustParse("1Gi")}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/apis/node.k8s.io/v1/runtimeclasses/operator-kata" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rc)
	}))
	t.Cleanup(server.Close)
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewKataRuntimeResolver(client, []KataRuntimeMapping{m}, func(context.Context, runtimeprofile.Profile, KataRuntimeMapping, *nodev1.RuntimeClass) (string, error) {
		return "sha256:" + strings.Repeat("e", 64), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	m.NodeSelector["thinkpixel.io/qualified"] = "mutated"
	m.Architectures[0] = "arm64"
	return resolver, p, rc
}
func TestKataRuntimeMappingBindsObservedClass(t *testing.T) {
	r, p, _ := runtimeFixture(t)
	got, err := r.Resolve(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mapping.RuntimeClass != "operator-kata" || got.RuntimeClassUID != "runtime-uid" || !shaDigest.MatchString(got.Digest) {
		t.Fatal("incomplete resolution")
	}
	got.Mapping.NodeSelector["thinkpixel.io/qualified"] = "mutated"
	got.Mapping.Architectures[0] = "arm64"
	again, err := r.Resolve(context.Background(), p)
	if err != nil || again.Digest != got.Digest {
		t.Fatal("mapping mutated", err)
	}
}
func TestKataRuntimeMappingFailsClosed(t *testing.T) {
	for _, name := range []string{"handler", "selectors", "overhead", "tolerations", "architecture", "reference", "evidence", "missing qualifier"} {
		t.Run(name, func(t *testing.T) {
			r, p, rc := runtimeFixture(t)
			switch name {
			case "handler":
				rc.Handler = "runc"
			case "selectors":
				rc.Scheduling.NodeSelector = map[string]string{}
			case "overhead":
				rc.Overhead = nil
			case "tolerations":
				rc.Scheduling.Tolerations = []v1.Toleration{{Operator: v1.TolerationOpExists}}
			case "architecture":
				p.Platform.Architectures = []string{"arm64"}
			case "reference":
				p.Implementation.IsolationRuntimeRef = "unconfigured"
			case "evidence":
				r.qualify = func(context.Context, runtimeprofile.Profile, KataRuntimeMapping, *nodev1.RuntimeClass) (string, error) {
					return "", errors.New("unqualified")
				}
			case "missing qualifier":
				if _, err := NewKataRuntimeResolver(r.client, []KataRuntimeMapping{r.mappings[p.Implementation.IsolationRuntimeRef]}, nil); err == nil {
					t.Fatal("no qualifier")
				}
				return
			}
			if _, err := r.Resolve(context.Background(), p); err == nil {
				t.Fatal("unsafe runtime accepted")
			}
		})
	}
}
