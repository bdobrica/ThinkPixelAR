package agentsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"net/http"
	"net/http/httptest"

	core "sigs.k8s.io/agent-sandbox/api/v1beta1"
	"testing"
)

func TestScratchClaimMappingAndGuard(t *testing.T) {
	old, r, volumes := codingFixture(t)
	_, raw, _, _, oldDigest := old.Resolution()
	config := old.config
	config.ScratchStorageClass = "ar-bounded-scratch-v1"
	mapper, err := NewCodingTemplate(raw, config, func(_ runtimeprofile.Profile, got CodingTemplateConfig) error {
		if got.ScratchStorageClass != config.ScratchStorageClass {
			t.Fatal("scratch absent from qualification")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r.Profile, _, r.ProfileDigest, _, r.ImplementationDigest = mapper.Resolution()
	if r.ImplementationDigest == oldDigest {
		t.Fatal("storage substitution did not change resolution")
	}
	r.Operation.Digest, _ = RequestDigest(r)
	mapped, err := mapper.Render(r, volumes)
	if err != nil {
		t.Fatal(err)
	}
	pod := mapped.Spec.PodTemplate.Spec
	if pod.Volumes[2].EmptyDir != nil || pod.Volumes[2].Ephemeral == nil || !secureBlueprint(r, pod) {
		t.Fatal("bounded scratch rejected or missing")
	}
	for name, mutate := range map[string]func(*v1.PersistentVolumeClaimSpec){
		"existing volume": func(s *v1.PersistentVolumeClaimSpec) { s.VolumeName = "workspace" },
		"clone": func(s *v1.PersistentVolumeClaimSpec) {
			s.DataSource = &v1.TypedLocalObjectReference{Kind: "PersistentVolumeClaim", Name: "workspace"}
		},
		"multi writer": func(s *v1.PersistentVolumeClaimSpec) {
			s.AccessModes = []v1.PersistentVolumeAccessMode{v1.ReadWriteMany}
		},
		"unbounded": func(s *v1.PersistentVolumeClaimSpec) { s.Resources.Requests = nil },
		"widened": func(s *v1.PersistentVolumeClaimSpec) {
			s.Resources.Requests[v1.ResourceStorage] = resource.MustParse("100Ti")
		},
		"default class": func(s *v1.PersistentVolumeClaimSpec) { s.StorageClassName = nil },
	} {
		t.Run(name, func(t *testing.T) {
			bad := pod.DeepCopy()
			mutate(&bad.Volumes[2].Ephemeral.VolumeClaimTemplate.Spec)
			if secureBlueprint(r, *bad) {
				t.Fatal("unsafe scratch accepted")
			}
		})
	}
	// A scratch template cannot be smuggled into a durable or credential mount.
	for _, index := range []int{0, 1, 3} {
		bad := pod.DeepCopy()
		bad.Volumes[index].Ephemeral = pod.Volumes[2].Ephemeral
		if secureBlueprint(r, *bad) {
			t.Fatal("extra source accepted")
		}
	}
}

func TestScratchVerifierRejectsIdentityAndBackingDrift(t *testing.T) {
	for _, failure := range []string{"", "owner", "larger backing", "rebound", "physical"} {
		t.Run(failure, func(t *testing.T) {
			controller := true
			spec := scratchClaim("bounded", 32<<20)
			pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "sandbox", Namespace: "tenant", UID: "pod-uid"}, Spec: v1.PodSpec{NodeName: "node", Volumes: []v1.Volume{{Name: "tmp", VolumeSource: v1.VolumeSource{Ephemeral: &v1.EphemeralVolumeSource{VolumeClaimTemplate: &v1.PersistentVolumeClaimTemplate{Spec: spec}}}}}}}
			claim := &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "sandbox-tmp", Namespace: "tenant", UID: "claim-uid", OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: "sandbox", UID: pod.UID, Controller: &controller}}}, Spec: spec, Status: v1.PersistentVolumeClaimStatus{Phase: v1.ClaimBound, Capacity: v1.ResourceList{v1.ResourceStorage: resource.MustParse("32Mi")}}}
			claim.Spec.VolumeName = "slot"
			pv := &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "slot", UID: "pv-uid"}, Spec: v1.PersistentVolumeSpec{StorageClassName: "bounded", Capacity: v1.ResourceList{v1.ResourceStorage: resource.MustParse("32Mi")}, ClaimRef: &v1.ObjectReference{Namespace: "tenant", Name: "sandbox-tmp", UID: claim.UID}}, Status: v1.PersistentVolumeStatus{Phase: v1.VolumeBound}}
			switch failure {
			case "owner":
				claim.OwnerReferences[0].UID = "other-pod"
			case "larger backing":
				pv.Spec.Capacity[v1.ResourceStorage] = resource.MustParse("64Mi")
			case "rebound":
				pv.Spec.ClaimRef.UID = "other-claim"
			}
			claim.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"}
			pv.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolume"}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/namespaces/tenant/persistentvolumeclaims/sandbox-tmp" {
					_ = json.NewEncoder(w).Encode(claim)
				} else if r.URL.Path == "/api/v1/persistentvolumes/slot" {
					_ = json.NewEncoder(w).Encode(pv)
				} else {
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, e := dynamic.NewForConfig(&rest.Config{Host: server.URL})
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			verify, err := NewScratchVerifier(client, func(context.Context, *v1.Pod, *v1.PersistentVolumeClaim, *v1.PersistentVolume) error {
				calls++
				if failure == "physical" {
					return errors.New("unbounded mount")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			err = verify(context.Background(), pod)
			if (err == nil) != (failure == "") {
				t.Fatal("unexpected verification", err)
			}
			if failure != "" && failure != "physical" && calls != 0 {
				t.Fatal("identity failure reached qualification")
			}
		})
	}
}

func TestEffectiveVerifierSupportsOnlyApprovedScratch(t *testing.T) {
	resolve, b, pod := secureFixture(t)
	source := v1.VolumeSource{Ephemeral: &v1.EphemeralVolumeSource{VolumeClaimTemplate: &v1.PersistentVolumeClaimTemplate{Spec: scratchClaim("bounded", 32<<20)}}}
	original := resolve
	resolve = func(ctx context.Context, r sandbox.AcquireRequest) (core.SandboxBlueprint, error) {
		bp, e := original(ctx, r)
		bp.PodTemplate.Spec.Volumes[2].VolumeSource = source
		return bp, e
	}
	pod.Spec.Volumes[2].VolumeSource = source
	verify, _ := NewSecureEffectiveVerifier(resolve, func(context.Context, sandbox.Binding, *v1.Pod) error { return nil })
	facts, err := verify(context.Background(), b, pod)
	if err != nil || !facts.Verified {
		t.Fatal("approved scratch rejected", err)
	}
	pod.Spec.Volumes[2].Ephemeral.VolumeClaimTemplate.Spec.StorageClassName = nil
	if facts, err = verify(context.Background(), b, pod); err == nil || facts.Verified {
		t.Fatal("unqualified scratch accepted")
	}
}
