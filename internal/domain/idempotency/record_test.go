package idempotency

import (
	"errors"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func TestRecordOwnershipCompletionAndCopies(t *testing.T) {
	now := time.Date(2026, 9, 3, 6, 30, 0, 0, time.UTC)
	ids := make([]primitives.ID, 6)
	for i := range ids {
		ids[i], _ = primitives.NewID(now.Add(time.Duration(i) * time.Millisecond))
	}
	d := "sha256:" + string(make([]byte, 0)) + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	r, err := New(ids[0], ids[1], Scope{PrincipalDigest: d, Action: "POST:/v1/sessions", KeyDigest: d}, "http-v1", d, ids[2], "", ids[3], ids[4], now.Add(time.Minute), now.Add(time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.TakeOwnership(ids[5], 1, now.Add(3*time.Minute), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = r.Succeed(Response{HTTPStatus: 201, Payload: []byte(`{"id":"same"}`)}, ids[3], 1, now.Add(2*time.Minute)); !errors.Is(err, ErrOwnership) {
		t.Fatalf("stale completion error = %v", err)
	}
	payload := []byte(`{"id":"same"}`)
	if err = r.Succeed(Response{HTTPStatus: 201, Payload: payload}, ids[5], 2, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	payload[0] = 'x'
	got, ok := r.Response()
	if !ok || string(got.Payload) != `{"id":"same"}` || r.State() != Succeeded {
		t.Fatalf("response = %#v, %v", got, ok)
	}
}

func TestRecordRejectsEarlyTakeoverAndInvalidTerminalFailure(t *testing.T) {
	now := time.Date(2026, 9, 3, 6, 30, 0, 0, time.UTC)
	ids := make([]primitives.ID, 6)
	for i := range ids {
		ids[i], _ = primitives.NewID(now.Add(time.Duration(i) * time.Millisecond))
	}
	d := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	r, _ := New(ids[0], ids[1], Scope{PrincipalDigest: d, Action: "cancel:v1", KeyDigest: d}, "v1", d, ids[2], "", ids[3], ids[4], now.Add(time.Minute), now.Add(time.Hour), now)
	if err := r.TakeOwnership(ids[5], 1, now.Add(2*time.Minute), now.Add(30*time.Second)); !errors.Is(err, ErrOwnership) {
		t.Fatalf("early takeover = %v", err)
	}
	if err := r.Fail(Failure{HTTPStatus: 503, ProblemType: "temporary", ProblemCode: "temporary"}, ids[3], 1, now.Add(time.Second)); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("transient failure = %v", err)
	}
}
