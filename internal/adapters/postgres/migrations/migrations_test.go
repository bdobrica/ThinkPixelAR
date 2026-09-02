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
	if len(got) != 2 || got[0].Version != 1 || got[0].Name != "tenant_sessions" ||
		got[1].Version != 2 || got[1].Name != "executions" {
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
}

func TestParseFilenameRejectsMalformedNames(t *testing.T) {
	for _, name := range []string{"migration.sql", "0_zero.sql", "000001.sql", "x_name.sql"} {
		if _, _, err := parseFilename(name); err == nil {
			t.Errorf("parseFilename(%q) succeeded", name)
		}
	}
}
