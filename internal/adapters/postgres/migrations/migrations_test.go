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
	if len(got) != 4 || got[0].Version != 1 || got[0].Name != "tenant_sessions" ||
		got[1].Version != 2 || got[1].Name != "executions" || got[2].Version != 3 || got[2].Name != "attempts" ||
		got[3].Version != 4 || got[3].Name != "one_mutable_execution_per_session" {
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
}

func TestParseFilenameRejectsMalformedNames(t *testing.T) {
	for _, name := range []string{"migration.sql", "0_zero.sql", "000001.sql", "x_name.sql"} {
		if _, _, err := parseFilename(name); err == nil {
			t.Errorf("parseFilename(%q) succeeded", name)
		}
	}
}
