package migrations

import (
	"regexp"
	"testing"
)

func TestLoadReturnsOrderedChecksummedMigrations(t *testing.T) {
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 15 || got[0].Version != 1 || got[0].Name != "tenant_sessions" ||
		got[1].Version != 2 || got[1].Name != "executions" || got[2].Version != 3 || got[2].Name != "attempts" ||
		got[3].Version != 4 || got[3].Name != "one_mutable_execution_per_session" ||
		got[4].Version != 5 || got[4].Name != "session_execution_fencing" ||
		got[5].Version != 6 || got[5].Name != "workspace_metadata" ||
		got[6].Version != 7 || got[6].Name != "checkpoint_metadata" ||
		got[7].Version != 8 || got[7].Name != "runtime_profile_resolution_snapshots" ||
		got[8].Version != 9 || got[8].Name != "runtime_bindings" ||
		got[9].Version != 10 || got[9].Name != "runtime_events" ||
		got[10].Version != 11 || got[10].Name != "session_recovery_state" ||
		got[11].Version != 12 || got[11].Name != "idempotency_records" ||
		got[12].Version != 13 || got[12].Name != "transactional_outbox" ||
		got[13].Version != 14 || got[13].Name != "reconciliation_work_claims" ||
		got[14].Version != 15 || got[14].Name != "cleanup_intents" {
		t.Fatalf("Load() = %#v", got)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(got[0].Checksum) {
		t.Fatalf("checksum = %q", got[0].Checksum)
	}
	for _, required := range []string{"CREATE TABLE tenants", "CREATE TABLE sessions", "ENABLE ROW LEVEL SECURITY", "execution_generation"} {
		if !regexp.MustCompile(required).MatchString(got[0].SQL) {
			t.Errorf("migration does not contain %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE executions", "session_generation", "executions_immutable_binding"} {
		if !regexp.MustCompile(required).MatchString(got[1].SQL) {
			t.Errorf("execution migration does not contain %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE attempts", "attempt_no", "attempts_one_current_idx", "sandbox_heartbeat_at", "attempts_immutable_identity_and_bindings"} {
		if !regexp.MustCompile(required).MatchString(got[2].SQL) {
			t.Errorf("attempt migration does not contain %q", required)
		}
	}
	for _, required := range []string{
		"CREATE UNIQUE INDEX executions_one_mutable_per_session_idx",
		"ON executions \\(tenant_id, session_id\\)",
		"WHERE state IN \\('QUEUED', 'MATERIALIZING', 'RUNNING', 'CANCELLING', 'TIMING_OUT'\\)",
	} {
		if !regexp.MustCompile(required).MatchString(got[3].SQL) {
			t.Errorf("mutable execution invariant migration does not contain %q", required)
		}
	}
	for _, required := range []string{
		"ADD COLUMN current_execution_id uuid",
		"DEFERRABLE INITIALLY DEFERRED",
		"CREATE TRIGGER sessions_execution_epoch",
		"NEW.execution_generation < OLD.execution_generation",
		"NEW.execution_generation - OLD.execution_generation <> 1",
		"CREATE TRIGGER attempts_current_mutation_fence",
		"s.current_execution_id = e.execution_id",
		"s.execution_generation = e.session_generation",
		"TG_OP = 'INSERT' AND NEW.is_current",
		"TG_OP = 'UPDATE' AND OLD.is_current",
	} {
		if !regexp.MustCompile(required).MatchString(got[4].SQL) {
			t.Errorf("session fencing migration does not contain %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE workspaces", "CREATE TABLE workspace_generations", "UNIQUE \\(tenant_id, session_id\\)", "workspaces_current_generation_fk", "workspace_generations_immutable", "workspace generation must advance by exactly one from snapshotting", "ENABLE ROW LEVEL SECURITY"} {
		if !regexp.MustCompile(required).MatchString(got[5].SQL) {
			t.Errorf("workspace migration does not contain %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE checkpoints", "workspace_generation_id", "checkpoints_tenant_session_committed_idx", "checkpoints_immutable_integrity", "committed checkpoint integrity metadata is immutable", "RFC8785-JCS", "ENABLE ROW LEVEL SECURITY"} {
		if !regexp.MustCompile(required).MatchString(got[6].SQL) {
			t.Errorf("checkpoint migration does not contain %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE runtime_profile_resolution_snapshots", "canonical_resolution bytea", "canonical_supported_versions bytea", "jsonb_typeof", "decision_reason", "RFC8785-JCS", "runtime_profile_resolution_snapshots_immutable", "ENABLE ROW LEVEL SECURITY"} {
		if !regexp.MustCompile(required).MatchString(got[7].SQL) {
			t.Errorf("runtime profile resolution migration does not contain %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE sandbox_bindings", "CREATE TABLE harness_bindings", "provider_reference", "negotiation_digest", "attempts_sandbox_binding_fk", "attempts_harness_binding_fk", "DEFERRABLE INITIALLY DEFERRED", "runtime binding ownership and attempt fence are immutable", "ENABLE ROW LEVEL SECURITY"} {
		if !regexp.MustCompile(required).MatchString(got[8].SQL) {
			t.Errorf("runtime binding migration does not contain %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE runtime_event_streams", "CREATE TABLE runtime_events", "UNIQUE \\(tenant_id, session_id, sequence\\)", "payload_reference", "octet_length\\(payload::text\\) BETWEEN 2 AND 65536", "FOR UPDATE", "runtime event sequence must advance by exactly one", "runtime events are append-only", "ENABLE ROW LEVEL SECURITY"} {
		if !regexp.MustCompile(required).MatchString(got[9].SQL) {
			t.Errorf("runtime event migration does not contain %q", required)
		}
	}
	for _, required := range []string{"ADD COLUMN recovery_state", "sessions_recovery_state_consistent", "state = 'DEGRADED'"} {
		if !regexp.MustCompile(required).MatchString(got[10].SQL) {
			t.Errorf("Session recovery state migration does not contain %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE idempotency_records", "UNIQUE \\(tenant_id, principal_digest, action, key_digest\\)", "request_digest", "response_payload", "expires_at", "idempotency_records_fenced_mutation", "active idempotency lease cannot be taken over", "ENABLE ROW LEVEL SECURITY"} {
		if !regexp.MustCompile(required).MatchString(got[11].SQL) {
			t.Errorf("idempotency migration does not contain %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE outbox_messages", "UNIQUE \\(tenant_id, topic, event_id\\)", "payload_digest", "claim_expires_at", "outbox_messages_available_idx", "FOR EACH ROW EXECUTE FUNCTION enforce_outbox_message_mutation", "active outbox claim cannot be taken over", "dead_letter_reason_code", "ENABLE ROW LEVEL SECURITY"} {
		if !regexp.MustCompile(required).MatchString(got[12].SQL) {
			t.Errorf("outbox migration does not contain %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE reconciliation_work", "UNIQUE \\(tenant_id, work_kind, target_type, target_id\\)", "reconciliation_work_available_idx", "reconciliation_work_expired_claim_idx", "claim_fence", "active reconciliation claim cannot be taken over", "ENABLE ROW LEVEL SECURITY"} {
		if !regexp.MustCompile(required).MatchString(got[13].SQL) {
			t.Errorf("reconciliation migration does not contain %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE cleanup_intents", "external_reference", "ownership_proof_digest", "cleanup_operation_id", "cleanup_intents_retry_idx", "cleanup_intents_orphan_idx", "terminal cleanup tombstone is immutable", "ENABLE ROW LEVEL SECURITY"} {
		if !regexp.MustCompile(required).MatchString(got[14].SQL) {
			t.Errorf("cleanup intent migration does not contain %q", required)
		}
	}
}

func TestParseFilenameRejectsMalformedNames(t *testing.T) {
	for _, name := range []string{"migration.sql", "0_zero.sql", "000001.sql", "x_name.sql"} {
		if _, _, err := parseFilename(name); err == nil {
			t.Errorf("parseFilename(%q) succeeded", name)
		}
	}
}
