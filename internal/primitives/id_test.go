package primitives

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestUUIDv7GenerationAndParsing(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 34, 56, 789000000, time.UTC)
	id, err := NewIDFrom(now, bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if id != "01a057d0-cc95-7000-8000-000000000000" {
		t.Fatalf("NewIDFrom() = %q", id)
	}
	if parsed, err := ParseID(string(id)); err != nil || parsed != id {
		t.Fatalf("ParseID() = %q, %v", parsed, err)
	}
}

func TestUUIDv7RejectsNonCanonicalAndWrongVersions(t *testing.T) {
	for _, value := range []string{
		"01A057D0-CC95-7000-8000-000000000000",
		"01a057d0-cc95-4000-8000-000000000000",
		"01a057d0-cc95-7000-c000-000000000000",
		"not-an-id",
	} {
		if _, err := ParseID(value); !errors.Is(err, ErrInvalidID) {
			t.Errorf("ParseID(%q) error = %v", value, err)
		}
	}
}
