package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunDocumentsExplicitMigrationBoundary(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, want 0; stderr = %q", code, stderr.String())
	}
	for _, required := range []string{"explicit command", "API replicas never migrate automatically", "THINKPIXELAR_DATABASE_URL"} {
		if !strings.Contains(stdout.String(), required) {
			t.Errorf("usage does not contain %q: %q", required, stdout.String())
		}
	}
}

func TestRunRejectsUnknownMigrationAction(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run([]string{"down"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(down) code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "expected command up") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunRequiresDatabaseURL(t *testing.T) {
	t.Setenv("THINKPIXELAR_DATABASE_URL", "")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"up"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(up) code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "database URL is required") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
