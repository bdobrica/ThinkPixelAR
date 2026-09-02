package runtimeprofile

import (
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func TestSnapshotCapturesImmutableResolution(t *testing.T) {
	resolution := []byte(`{"schema_version":1,"name":"coding-medium-secure"}`)
	versions := []byte(`{"agent_sandbox":"v0.1.0","kubernetes":"v1.35.1"}`)
	snapshot, err := NewSnapshot(id(1), id(2), 1, "coding-medium-secure", resolution, digest('a'),
		"coding-medium-secure-v1", "1.0.0", digest('b'), versions, digest('c'), "authority-and-operator-policy-satisfied", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	resolution[0], versions[0] = '[', '['
	gotResolution, gotVersions := snapshot.CanonicalResolution(), snapshot.CanonicalSupportedVersions()
	gotResolution[0], gotVersions[0] = '[', '['
	if snapshot.CanonicalResolution()[0] != '{' || snapshot.CanonicalSupportedVersions()[0] != '{' {
		t.Fatal("snapshot JSON was mutable through caller or accessor")
	}
	if snapshot.ExecutionID() != id(2) || snapshot.ProfileName() != "coding-medium-secure" || snapshot.SchemaVersion() != 1 {
		t.Fatal("snapshot identity was not retained")
	}
}

func TestSnapshotRejectsInvalidEvidence(t *testing.T) {
	validResolution := []byte(`{"schema_version":1}`)
	validVersions := []byte(`{"kubernetes":"v1.35.1"}`)
	cases := []struct {
		name       string
		schema     uint64
		profile    string
		resolution []byte
		digest     string
		versions   []byte
		reason     string
	}{
		{"schema", 2, "secure", validResolution, digest('a'), validVersions, "allowed"},
		{"profile", 1, "Kubernetes RuntimeClass", validResolution, digest('a'), validVersions, "allowed"},
		{"resolution", 1, "secure", []byte(`[]`), digest('a'), validVersions, "allowed"},
		{"digest", 1, "secure", validResolution, "sha256:no", validVersions, "allowed"},
		{"versions", 1, "secure", validResolution, digest('a'), []byte(`[]`), "allowed"},
		{"reason", 1, "secure", validResolution, digest('a'), validVersions, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewSnapshot(id(1), id(2), tc.schema, tc.profile, tc.resolution, tc.digest,
				"provider-profile-v1", "1.0.0", digest('b'), tc.versions, digest('c'), tc.reason, time.Unix(1, 0))
			if err != ErrInvalidSnapshot {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func id(last byte) primitives.ID {
	return primitives.ID("01890f3e-7b2d-7000-8000-00000000000" + string('0'+last))
}
func digest(char byte) string { return "sha256:" + strings.Repeat(string(char), 64) }
