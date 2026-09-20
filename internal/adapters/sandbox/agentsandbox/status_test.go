package agentsandbox

import (
	"context"
	"errors"
	"k8s.io/apimachinery/pkg/runtime"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	core "sigs.k8s.io/agent-sandbox/api/v1beta1"
)

func TestStateTranslationUsesFreshBooleanConditionsAndPhysicalAbsence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       core.SandboxOperatingMode
		condition  string
		truth      metav1.ConditionStatus
		generation int64
		pod        bool
		want       sandbox.State
	}{
		{"suspended false", core.SandboxOperatingModeRunning, "Suspended", metav1.ConditionFalse, 2, false, sandbox.Provisioning},
		{"suspend accepted", core.SandboxOperatingModeSuspended, "Suspended", metav1.ConditionFalse, 2, true, sandbox.Suspending},
		{"suspended with pod", core.SandboxOperatingModeSuspended, "Suspended", metav1.ConditionTrue, 2, true, sandbox.Suspending},
		{"suspended absent", core.SandboxOperatingModeSuspended, "Suspended", metav1.ConditionTrue, 2, false, sandbox.Suspended},
		{"stale ready", core.SandboxOperatingModeRunning, "Ready", metav1.ConditionTrue, 1, true, sandbox.Provisioning},
		{"ready", core.SandboxOperatingModeRunning, "Ready", metav1.ConditionTrue, 2, true, sandbox.Ready},
		{"finished", core.SandboxOperatingModeRunning, "Finished", metav1.ConditionTrue, 2, true, sandbox.Failed},
		{"unknown mode", "new-mode", "Ready", metav1.ConditionTrue, 2, true, sandbox.Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &core.Sandbox{ObjectMeta: metav1.ObjectMeta{Generation: 2}, Spec: core.SandboxSpec{OperatingMode: tc.mode}, Status: core.SandboxStatus{Conditions: []metav1.Condition{{Type: tc.condition, Status: tc.truth, ObservedGeneration: tc.generation, Message: "untrusted secret"}}}}
			var pod *v1.Pod
			if tc.pod {
				pod = &v1.Pod{Status: v1.PodStatus{Phase: v1.PodRunning, Conditions: []v1.PodCondition{{Type: v1.PodReady, Status: v1.ConditionTrue}}}}
			}
			state, reason := translateState(s, pod)
			if state != tc.want || reason == "untrusted secret" {
				t.Fatalf("state %s reason %s", state, reason)
			}
		})
	}
}

func TestGetFailsClosedWithoutEffectiveEvidenceAndDistinguishesOutage(t *testing.T) {
	a, b := &testAPI{}, &testBindings{}
	p := providerFixture(t, a, b)
	r := acquireFixture(t)
	ctx := context.Background()
	if _, err := p.Acquire(ctx, r); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.object["metadata"].(map[string]any)["generation"] = int64(1)
	a.object["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True", "observedGeneration": int64(1)}}}
	controller := true
	pod := v1.Pod{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"}, ObjectMeta: metav1.ObjectMeta{Name: "ar-" + string(r.Scope.SandboxID), OwnerReferences: []metav1.OwnerReference{{APIVersion: core.GroupVersion.String(), Kind: "Sandbox", Name: "ar-" + string(r.Scope.SandboxID), UID: "provider-uid-1", Controller: &controller}}}, Status: v1.PodStatus{Phase: v1.PodRunning, Conditions: []v1.PodCondition{{Type: v1.PodReady, Status: v1.ConditionTrue}}}}
	a.pod, _ = runtime.DefaultUnstructuredConverter.ToUnstructured(&pod)
	a.mu.Unlock()
	status, err := p.Get(ctx, r.Scope.TenantID, r.Scope.SandboxID)
	if err != nil || status.State != sandbox.Unknown {
		t.Fatalf("desired-only readiness: %+v %v", status, err)
	}
	p.verify = func(context.Context, sandbox.Binding, *v1.Pod) (sandbox.EffectiveFacts, error) {
		return sandbox.EffectiveFacts{Verified: true}, nil
	}
	status, err = p.Get(ctx, r.Scope.TenantID, r.Scope.SandboxID)
	if err != nil || status.State != sandbox.Ready {
		t.Fatalf("verified readiness: %+v %v", status, err)
	}
	a.mu.Lock()
	a.unavailable = true
	a.mu.Unlock()
	if _, err = p.Get(ctx, r.Scope.TenantID, r.Scope.SandboxID); !errors.Is(err, sandbox.ErrUnavailable) {
		t.Fatalf("outage treated as absence: %v", err)
	}
	a.mu.Lock()
	a.unavailable = false
	a.object = nil
	a.mu.Unlock()
	if _, err = p.Get(ctx, r.Scope.TenantID, r.Scope.SandboxID); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("absence: %v", err)
	}
}
