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
	if len(got) != 7 || got[0].Version != 1 || got[0].Name != "tenant_sessions" ||
		got[1].Version != 2 || got[1].Name != "executions" || got[2].Version != 3 || got[2].Name != "attempts" ||
		got[3].Version != 4 || got[3].Name != "one_mutable_execution_per_session" ||
		got[4].Version != 5 || got[4].Name != "session_execution_fencing" ||
		got[5].Version != 6 || got[5].Name != "workspace_metadata" ||
		got[6].Version != 7 || got[6].Name != "checkpoint_metadata" {
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
}

func TestParseFilenameRejectsMalformedNames(t *testing.T) {
	for _, name := range []string{"migration.sql", "0_zero.sql", "000001.sql", "x_name.sql"} {
		if _, _, err := parseFilename(name); err == nil {
			t.Errorf("parseFilename(%q) succeeded", name)
		}
	}
}
