package telemetry

import (
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const metricNamespace = "thinkpixelar"

// Metrics owns the process-local Prometheus registry and the initial runtime
// collectors. Collectors are intentionally private: callers use typed methods
// so tenant and runtime object identifiers cannot become metric labels.
type Metrics struct {
	registry *prometheus.Registry

	sessions                  *prometheus.GaugeVec
	executions                *prometheus.CounterVec
	attempts                  *prometheus.CounterVec
	recoveries                prometheus.Counter
	sandboxAcquisition        *prometheus.HistogramVec
	sandboxFailures           prometheus.Counter
	startLatency              *prometheus.HistogramVec
	executionDuration         *prometheus.HistogramVec
	lifecycleLatency          *prometheus.HistogramVec
	workspaceLatency          *prometheus.HistogramVec
	eventStreamConnections    prometheus.Gauge
	eventStreamBackpressure   prometheus.Counter
	authorityLatency          *prometheus.HistogramVec
	authorityFailures         prometheus.Counter
	reconcilerQueueDepth      prometheus.Gauge
	reconcilerQueueLag        prometheus.Gauge
	postgresConnections       *prometheus.GaugeVec
	postgresTransactions      *prometheus.CounterVec
	agentdDisconnects         prometheus.Counter
	staleAttemptFenceRejected prometheus.Counter
}

// NewMetrics constructs an isolated registry without the global Go or process
// collectors. The absence of implicit collectors makes exposition deterministic
// and prevents unrelated libraries from publishing metrics through this sink.
func NewMetrics() *Metrics {
	buckets := prometheus.DefBuckets
	m := &Metrics{registry: prometheus.NewRegistry()}
	m.sessions = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: metricNamespace, Name: "sessions", Help: "Current Sessions by lifecycle state."}, []string{"state"})
	m.executions = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Name: "executions_total", Help: "Executions reaching a lifecycle state."}, []string{"state"})
	m.attempts = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Name: "attempts_total", Help: "Physical execution Attempts by terminal result class."}, []string{"result"})
	m.recoveries = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricNamespace, Name: "recoveries_total", Help: "Recovery operations started."})
	m.sandboxAcquisition = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Name: "sandbox_acquisition_seconds", Help: "Sandbox acquisition latency.", Buckets: buckets}, []string{"result"})
	m.sandboxFailures = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricNamespace, Name: "sandbox_failures_total", Help: "Sandbox lifecycle failures."})
	m.startLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Name: "start_seconds", Help: "Cold, warm, and harness start latency.", Buckets: buckets}, []string{"kind"})
	m.executionDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Name: "execution_seconds", Help: "Execution duration by result class.", Buckets: buckets}, []string{"result"})
	m.lifecycleLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Name: "lifecycle_operation_seconds", Help: "Suspend, resume, and checkpoint latency.", Buckets: buckets}, []string{"operation", "result"})
	m.workspaceLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Name: "workspace_operation_seconds", Help: "Workspace attach and snapshot latency.", Buckets: buckets}, []string{"operation", "result"})
	m.eventStreamConnections = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Name: "event_stream_connections", Help: "Current Runtime Event stream connections."})
	m.eventStreamBackpressure = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricNamespace, Name: "event_stream_backpressure_total", Help: "Runtime Event stream backpressure occurrences."})
	m.authorityLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Name: "authority_operation_seconds", Help: "Authority-provider latency by bounded mode and result.", Buckets: buckets}, []string{"mode", "result"})
	m.authorityFailures = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricNamespace, Name: "authority_failures_total", Help: "Authority-provider failures."})
	m.reconcilerQueueDepth = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Name: "reconciler_queue_depth", Help: "Current reconciler queue depth."})
	m.reconcilerQueueLag = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: metricNamespace, Name: "reconciler_queue_lag_seconds", Help: "Age of the oldest pending reconciliation item."})
	m.postgresConnections = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: metricNamespace, Name: "postgres_connections", Help: "PostgreSQL pool connections by state."}, []string{"state"})
	m.postgresTransactions = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Name: "postgres_transactions_total", Help: "PostgreSQL transactions by result."}, []string{"result"})
	m.agentdDisconnects = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricNamespace, Name: "agentd_disconnects_total", Help: "Sandbox agentd transport disconnects."})
	m.staleAttemptFenceRejected = prometheus.NewCounter(prometheus.CounterOpts{Namespace: metricNamespace, Name: "stale_attempt_fence_rejections_total", Help: "Rejected stale Attempt or fence operations."})
	m.registry.MustRegister(m.sessions, m.executions, m.attempts, m.recoveries, m.sandboxAcquisition, m.sandboxFailures, m.startLatency, m.executionDuration, m.lifecycleLatency, m.workspaceLatency, m.eventStreamConnections, m.eventStreamBackpressure, m.authorityLatency, m.authorityFailures, m.reconcilerQueueDepth, m.reconcilerQueueLag, m.postgresConnections, m.postgresTransactions, m.agentdDisconnects, m.staleAttemptFenceRejected)
	return m
}

// Gatherer exposes read-only collection for tests and the HTTP adapter.
func (m *Metrics) Gatherer() prometheus.Gatherer { return m.registry }

// Handler returns a Prometheus exposition handler for the isolated registry.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) SetSessions(state string, count float64) error {
	if !allowed(state, "creating", "ready", "active", "idle", "suspending", "suspended", "resuming", "degraded", "recovering", "closing", "closed", "failed") {
		return errors.New("unsupported Session state metric label")
	}
	m.sessions.WithLabelValues(state).Set(count)
	return nil
}

func (m *Metrics) Execution(state string) error {
	if !allowed(state, "admitted", "pending", "running", "cancelling", "succeeded", "failed", "cancelled", "timed_out") {
		return errors.New("unsupported Execution state metric label")
	}
	m.executions.WithLabelValues(state).Inc()
	return nil
}

func (m *Metrics) Attempt(result string) error {
	if !resultLabel(result) {
		return errors.New("unsupported Attempt result metric label")
	}
	m.attempts.WithLabelValues(result).Inc()
	return nil
}
func (m *Metrics) Recovery() { m.recoveries.Inc() }
func (m *Metrics) ObserveSandboxAcquisition(result string, d time.Duration) error {
	if !resultLabel(result) {
		return errors.New("unsupported sandbox result metric label")
	}
	m.sandboxAcquisition.WithLabelValues(result).Observe(d.Seconds())
	return nil
}
func (m *Metrics) SandboxFailure() { m.sandboxFailures.Inc() }
func (m *Metrics) ObserveStart(kind string, d time.Duration) error {
	if !allowed(kind, "cold", "warm", "harness") {
		return errors.New("unsupported start kind metric label")
	}
	m.startLatency.WithLabelValues(kind).Observe(d.Seconds())
	return nil
}
func (m *Metrics) ObserveExecution(result string, d time.Duration) error {
	if !resultLabel(result) {
		return errors.New("unsupported execution result metric label")
	}
	m.executionDuration.WithLabelValues(result).Observe(d.Seconds())
	return nil
}
func (m *Metrics) ObserveLifecycle(operation, result string, d time.Duration) error {
	if !allowed(operation, "suspend", "resume", "checkpoint") || !resultLabel(result) {
		return errors.New("unsupported lifecycle metric label")
	}
	m.lifecycleLatency.WithLabelValues(operation, result).Observe(d.Seconds())
	return nil
}
func (m *Metrics) ObserveWorkspace(operation, result string, d time.Duration) error {
	if !allowed(operation, "attach", "snapshot") || !resultLabel(result) {
		return errors.New("unsupported Workspace metric label")
	}
	m.workspaceLatency.WithLabelValues(operation, result).Observe(d.Seconds())
	return nil
}
func (m *Metrics) EventStreamConnected()    { m.eventStreamConnections.Inc() }
func (m *Metrics) EventStreamDisconnected() { m.eventStreamConnections.Dec() }
func (m *Metrics) EventStreamBackpressure() { m.eventStreamBackpressure.Inc() }
func (m *Metrics) ObserveAuthority(mode, result string, d time.Duration) error {
	if !allowed(mode, "local", "thinkpixelag") || !resultLabel(result) {
		return errors.New("unsupported authority metric label")
	}
	m.authorityLatency.WithLabelValues(mode, result).Observe(d.Seconds())
	return nil
}
func (m *Metrics) AuthorityFailure() { m.authorityFailures.Inc() }
func (m *Metrics) SetReconcilerQueue(depth int, oldestAge time.Duration) {
	m.reconcilerQueueDepth.Set(float64(depth))
	m.reconcilerQueueLag.Set(oldestAge.Seconds())
}
func (m *Metrics) SetPostgresConnections(state string, count int) error {
	if !allowed(state, "idle", "in_use", "max") {
		return errors.New("unsupported PostgreSQL connection metric label")
	}
	m.postgresConnections.WithLabelValues(state).Set(float64(count))
	return nil
}
func (m *Metrics) PostgresTransaction(result string) error {
	if !resultLabel(result) {
		return errors.New("unsupported PostgreSQL result metric label")
	}
	m.postgresTransactions.WithLabelValues(result).Inc()
	return nil
}
func (m *Metrics) AgentdDisconnect()          { m.agentdDisconnects.Inc() }
func (m *Metrics) StaleAttemptFenceRejected() { m.staleAttemptFenceRejected.Inc() }

func resultLabel(value string) bool {
	return allowed(value, "success", "failure", "cancelled", "timeout")
}
func allowed(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
