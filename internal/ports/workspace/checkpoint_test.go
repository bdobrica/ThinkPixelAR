package workspace

import (
	"math"
	"testing"
)

func TestCheckpointValidation(t *testing.T) {
	r := CheckpointRequest{TenantID: "00000000-0000-7000-8000-000000000001", SessionID: "00000000-0000-7000-8000-000000000002", WorkspaceID: "00000000-0000-7000-8000-000000000003", ParentID: "00000000-0000-7000-8000-000000000004", Operation: Operation{ID: "00000000-0000-7000-8000-000000000005"}, ConfigurationDigest: EmptyManifestDigest()}
	r.Operation.Digest = CheckpointDigest(r)
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*CheckpointRequest){func(r *CheckpointRequest) { r.ParentGeneration = math.MaxInt64 }, func(r *CheckpointRequest) { r.ParentGeneration = -1 }, func(r *CheckpointRequest) { r.AttemptID = r.ParentID }, func(r *CheckpointRequest) { r.ExecutionID = r.ParentID }, func(r *CheckpointRequest) { r.TenantID = "bad" }} {
		c := r
		change(&c)
		c.Operation.Digest = CheckpointDigest(c)
		if c.Validate() == nil {
			t.Fatal("accepted invalid request", c)
		}
	}
	p := CheckpointProof{SnapshotReference: "exact/uid", IntegrityAlgorithm: "manifest-v1", IntegrityRoot: EmptyManifestDigest(), ManifestDigest: EmptyManifestDigest(), Evidence: []byte(`{"source":"verified"}`)}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"null", "[]", "{}", "not-json"} {
		c := p
		c.Evidence = []byte(raw)
		if c.Validate() == nil {
			t.Fatal("accepted invalid proof", raw)
		}
	}
}
