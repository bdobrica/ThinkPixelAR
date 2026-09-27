package session

import (
	"math"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func TestSuspendRequestBinding(t *testing.T) {
	id, _ := primitives.NewID(time.Now())
	r := SuspendRequest{Caller: Caller{TenantID: id, PrincipalDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, SessionID: id, CheckpointID: id, OperationID: id, ExpectedVersion: 3}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*SuspendRequest){
		func(v *SuspendRequest) { v.ExpectedVersion++ },
		func(v *SuspendRequest) {
			v.Caller.PrincipalDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		},
		func(v *SuspendRequest) { v.CheckpointID, _ = primitives.NewID(time.Now()) },
	} {
		other := r
		change(&other)
		if other.Digest() == r.Digest() {
			t.Fatal("unbound replay input")
		}
	}
	for _, version := range []int64{-1, math.MaxInt64} {
		other := r
		other.ExpectedVersion = version
		if other.Validate() == nil {
			t.Fatal("invalid version accepted")
		}
	}
	other := r
	other.Caller.PrincipalDigest = ""
	if other.Validate() == nil {
		t.Fatal("missing caller accepted")
	}
}
