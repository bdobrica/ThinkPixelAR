package cleanup

import (
	"errors"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"testing"
	"time"
)

func TestIntentRetryConfirmAndQuarantine(t *testing.T) {
	now := time.Date(2026, 9, 3, 6, 30, 0, 0, time.UTC)
	ids := make([]primitives.ID, 4)
	for n := range ids {
		ids[n], _ = primitives.NewID(now.Add(time.Duration(n) * time.Millisecond))
	}
	target := Target{OwnerType: "checkpoint", OwnerID: ids[2], TargetType: "vendor-object", ProviderKind: "artifact", ExternalReference: "objects/exact", OperationID: ids[3], RequestDigest: digest('a'), OwnershipProofDigest: digest('b'), Orphan: true}
	i, err := New(ids[0], ids[1], target, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = i.Retry(0, now.Add(time.Minute), "TIMEOUT", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if i.Version() != 1 || i.Attempts() != 1 || i.LastErrorCode() != "TIMEOUT" {
		t.Fatalf("retry = %#v", i)
	}
	if err = i.Confirm(1, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if i.State() != Confirmed {
		t.Fatalf("state = %s", i.State())
	}
	if err = i.Retry(2, now.Add(time.Minute), "LATE", now.Add(3*time.Second)); !errors.Is(err, ErrInvalidIntent) {
		t.Fatalf("terminal retry = %v", err)
	}

	q, _ := New(ids[0], ids[1], target, now, now)
	if err = q.Quarantine(0, "OWNERSHIP_MISMATCH", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if q.State() != Quarantined {
		t.Fatalf("state = %s", q.State())
	}
}

func TestIntentRejectsUnsafeMetadataAndStaleVersion(t *testing.T) {
	now := time.Now().UTC()
	id, _ := primitives.NewID(now)
	target := Target{OwnerType: "sandbox", OwnerID: id, TargetType: "sandbox", ProviderKind: "kubernetes", ExternalReference: "exact", OperationID: id, RequestDigest: digest('a'), OwnershipProofDigest: digest('b')}
	i, err := New(id, id, target, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = i.Retry(1, now.Add(time.Minute), "TIMEOUT", now); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale = %v", err)
	}
	target.ExternalReference = ""
	if _, err = New(id, id, target, now, now); !errors.Is(err, ErrInvalidIntent) {
		t.Fatalf("empty ref = %v", err)
	}
}

func digest(c byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return "sha256:" + string(b)
}
