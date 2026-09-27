package session

import (
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"math"
	"testing"
	"time"
)

func TestResumeRequestBinding(t *testing.T) {
	id, _ := primitives.NewID(time.Now())
	r := ResumeRequest{Caller: Caller{TenantID: id, PrincipalDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, SessionID: id, OperationID: id, CheckpointID: id, ExpectedVersion: 4}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if r.Digest() == SuspendRequest(r).Digest() {
		t.Fatal("operation domains collide")
	}
	for _, version := range []int64{-1, math.MaxInt64 - 1, math.MaxInt64} {
		other := r
		other.ExpectedVersion = version
		if other.Validate() == nil {
			t.Fatal("version exhaustion accepted")
		}
	}
	other := r
	other.ExpectedVersion++
	if other.Digest() == r.Digest() {
		t.Fatal("unbound version")
	}
	other = r
	other.Caller.PrincipalDigest = ""
	if other.Validate() == nil {
		t.Fatal("missing principal")
	}
	if _, err := NewResumer(nil, nil); err == nil {
		t.Fatal("missing dependencies")
	}
}
