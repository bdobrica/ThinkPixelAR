package telemetry

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsExposeInitialCollectorsWithoutSensitiveLabels(t *testing.T) {
	metrics := NewMetrics()
	if err := metrics.SetSessions("active", 2); err != nil {
		t.Fatal(err)
	}
	if err := metrics.Execution("running"); err != nil {
		t.Fatal(err)
	}
	if err := metrics.Attempt("success"); err != nil {
		t.Fatal(err)
	}
	metrics.Recovery()
	if err := metrics.ObserveSandboxAcquisition("success", time.Second); err != nil {
		t.Fatal(err)
	}
	metrics.SandboxFailure()
	if err := metrics.ObserveStart("cold", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := metrics.ObserveExecution("success", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := metrics.ObserveLifecycle("checkpoint", "failure", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := metrics.ObserveWorkspace("snapshot", "success", time.Second); err != nil {
		t.Fatal(err)
	}
	metrics.EventStreamConnected()
	metrics.EventStreamBackpressure()
	metrics.EventStreamDisconnected()
	if err := metrics.ObserveAuthority("local", "success", time.Second); err != nil {
		t.Fatal(err)
	}
	metrics.AuthorityFailure()
	metrics.SetReconcilerQueue(2, time.Second)
	if err := metrics.SetPostgresConnections("idle", 3); err != nil {
		t.Fatal(err)
	}
	if err := metrics.PostgresTransaction("success"); err != nil {
		t.Fatal(err)
	}
	metrics.AgentdDisconnect()
	metrics.StaleAttemptFenceRejected()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/metrics", nil)
	metrics.Handler().ServeHTTP(recorder, request)
	body := recorder.Body.String()
	for _, name := range []string{"thinkpixelar_sessions", "thinkpixelar_executions_total", "thinkpixelar_sandbox_acquisition_seconds", "thinkpixelar_authority_operation_seconds", "thinkpixelar_stale_attempt_fence_rejections_total"} {
		if !strings.Contains(body, name) {
			t.Errorf("exposition missing %q", name)
		}
	}
	for _, prohibited := range []string{"tenant_id", "session_id", "execution_id", "attempt_id", "sandbox_id", "request_id", "trace_id"} {
		if strings.Contains(body, prohibited) {
			t.Errorf("exposition contains prohibited label %q", prohibited)
		}
	}
}

func TestMetricsRejectUnboundedLabelValuesWithoutExposingThem(t *testing.T) {
	const canary = "tenant-secret-canary"
	metrics := NewMetrics()
	checks := []error{
		metrics.SetSessions(canary, 1),
		metrics.Execution(canary),
		metrics.Attempt(canary),
		metrics.ObserveSandboxAcquisition(canary, 0),
		metrics.ObserveStart(canary, 0),
		metrics.ObserveLifecycle(canary, "success", 0),
		metrics.ObserveWorkspace("attach", canary, 0),
		metrics.ObserveAuthority(canary, "success", 0),
		metrics.SetPostgresConnections(canary, 1),
	}
	for index, err := range checks {
		if err == nil {
			t.Errorf("check %d accepted unbounded label", index)
		}
	}
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	if strings.Contains(recorder.Body.String(), canary) {
		t.Fatal("rejected sensitive value reached metrics exposition")
	}
}
