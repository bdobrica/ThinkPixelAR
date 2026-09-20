package reconciliation

import (
	"context"
	"errors"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type fixture struct {
	intent                                sandbox.ComputeIntent
	observed                              sandbox.ComputeObservation
	getErr, actionErr, recordErr, authErr error
	state                                 sandbox.State
	verified                              bool
	calls                                 []string
}

func (f *fixture) LoadCompute(context.Context, primitives.ID, primitives.ID) (sandbox.ComputeIntent, error) {
	f.calls = append(f.calls, "load")
	return f.intent, nil
}
func (f *fixture) RecordCompute(_ context.Context, _ sandbox.ComputeIntent, o sandbox.ComputeObservation) error {
	f.calls = append(f.calls, "record")
	f.observed = o
	return f.recordErr
}
func (f *fixture) CheckCompute(context.Context, sandbox.Scope) error {
	f.calls = append(f.calls, "authority")
	return f.authErr
}
func (f *fixture) Acquire(_ context.Context, r sandbox.AcquireRequest) (sandbox.Handle, error) {
	f.calls = append(f.calls, "acquire")
	if r.Operation != f.intent.Binding.Request.Operation {
		panic("changed operation")
	}
	return sandbox.Handle{SandboxID: r.Scope.SandboxID, ProviderReference: "opaque"}, f.actionErr
}
func (f *fixture) Get(context.Context, primitives.ID, primitives.ID) (sandbox.Status, error) {
	f.calls = append(f.calls, "get")
	return sandbox.Status{Handle: sandbox.Handle{SandboxID: f.intent.Binding.Request.Scope.SandboxID, ProviderReference: f.intent.Binding.ProviderReference}, State: f.state, Effective: sandbox.EffectiveFacts{Verified: f.verified}}, f.getErr
}
func (f *fixture) Release(_ context.Context, _ primitives.ID, _ primitives.ID, op sandbox.Operation) error {
	f.calls = append(f.calls, "release")
	if op != f.intent.Operation {
		panic("changed release identity")
	}
	return f.actionErr
}

func newFixture() *fixture {
	id := primitives.ID("01900000-0000-7000-8000-000000000001")
	op := sandbox.Operation{ID: string(id), Digest: "saved-digest"}
	return &fixture{intent: sandbox.ComputeIntent{Binding: sandbox.Binding{Request: sandbox.AcquireRequest{Scope: sandbox.Scope{TenantID: id, SandboxID: id}, Operation: op}}, Desired: sandbox.ComputeRunning, Current: true, Operation: op}, state: sandbox.Provisioning}
}
func TestComputeReconciliation(t *testing.T) {
	for _, tc := range []struct {
		name                string
		setup               func(*fixture)
		state               sandbox.State
		code                string
		converged, recovery bool
		action              string
	}{
		{"ambiguous acquisition", func(f *fixture) { f.getErr = sandbox.ErrNotFound }, sandbox.Provisioning, "ACQUIRE_ACCEPTED", false, false, "acquire"},
		{"fenced orphan", func(f *fixture) { f.intent.Current = false }, sandbox.Unknown, "FENCED_COMPUTE", false, true, ""},
		{"bound missing", func(f *fixture) { f.getErr = sandbox.ErrNotFound; f.intent.Binding.ProviderReference = "opaque" }, sandbox.Unknown, "BOUND_COMPUTE_MISSING", false, true, ""},
		{"outage", func(f *fixture) { f.getErr = errors.New("sensitive provider payload") }, sandbox.Unknown, "PROVIDER_UNAVAILABLE", false, false, ""},
		{"timeout after create", func(f *fixture) { f.getErr = sandbox.ErrNotFound; f.actionErr = sandbox.ErrTimeout }, sandbox.Unknown, "PROVIDER_UNAVAILABLE", false, false, "acquire"},
		{"verified ready", func(f *fixture) { f.state = sandbox.Ready; f.verified = true }, sandbox.Ready, "COMPUTE_READY", true, false, ""},
		{"unverified ready", func(f *fixture) { f.state = sandbox.Ready }, sandbox.Unknown, "EFFECTIVE_STATE_UNVERIFIED", false, true, ""},
		{"unknown", func(f *fixture) { f.state = sandbox.Unknown }, sandbox.Unknown, "PROVIDER_UNKNOWN", false, false, ""},
		{"failed", func(f *fixture) { f.state = sandbox.Failed }, sandbox.Unknown, "COMPUTE_UNEXPECTED_STATE", false, true, ""},
		{"integrity drift", func(f *fixture) { f.getErr = sandbox.ErrIntegrity }, sandbox.Unknown, "PROVIDER_INTEGRITY_FAILURE", false, true, ""},
		{"release pending", func(f *fixture) {
			f.intent.Desired = sandbox.ComputeReleased
			f.intent.ReleaseAuthorized = true
			f.intent.Current = false
			f.authErr = sandbox.ErrPermission
		}, sandbox.Releasing, "RELEASE_PENDING", false, false, "release"},
		{"release confirmed", func(f *fixture) {
			f.intent.Desired = sandbox.ComputeReleased
			f.intent.ReleaseAuthorized = true
			f.getErr = sandbox.ErrNotFound
		}, sandbox.Released, "COMPUTE_ABSENT", true, false, "release"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			tc.setup(f)
			c, _ := NewCompute(f, f, f)
			r, err := c.Reconcile(context.Background(), f.intent.Binding.Request.Scope.TenantID, f.intent.Binding.Request.Scope.SandboxID)
			if err != nil || r.State != tc.state || r.Code != tc.code || r.Converged != tc.converged || r.RecoveryRequired != tc.recovery {
				t.Fatalf("result %+v error %v", r, err)
			}
			for _, action := range []string{"acquire", "release"} {
				found := false
				for _, call := range f.calls {
					found = found || call == action
				}
				if found != (action == tc.action) {
					t.Fatalf("actions %v", f.calls)
				}
			}
		})
	}
}
func TestComputeFencesAndRecordCAS(t *testing.T) {
	for _, kind := range []string{"authority", "cleanup", "tenant", "cas"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture()
			tenant := f.intent.Binding.Request.Scope.TenantID
			switch kind {
			case "stale":
				f.intent.Current = false
			case "authority":
				f.authErr = sandbox.ErrPermission
			case "cleanup":
				f.intent.Desired = sandbox.ComputeReleased
			case "tenant":
				tenant = "01900000-0000-7000-8000-000000000002"
			case "cas":
				f.recordErr = sandbox.ErrConflict
			}
			c, _ := NewCompute(f, f, f)
			_, err := c.Reconcile(context.Background(), tenant, f.intent.Binding.Request.Scope.SandboxID)
			if err == nil {
				t.Fatal("missing fence")
			}
			if kind != "cas" {
				for _, call := range f.calls {
					if call == "get" || call == "acquire" || call == "release" || call == "record" {
						t.Fatalf("action after failed fence: %v", f.calls)
					}
				}
			}
		})
	}
}
