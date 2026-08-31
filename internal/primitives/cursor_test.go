package primitives

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
)

func TestCursorRoundTripAndContextBinding(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	codec, err := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"), clock.Fixed{Time: now})
	if err != nil {
		t.Fatal(err)
	}
	want := Cursor{
		TenantID: "tenant-opaque", PrincipalID: "principal-opaque", SecurityEpoch: 7,
		Route: "/v1/sessions", APIVersion: "v1", QueryDigest: "sha256:filter-and-sort",
		LastKey: "2026-08-31T11:00:00Z/id", PageSizeCeiling: 50, Snapshot: "snapshot-1",
		ExpiresAt: now.Add(time.Hour),
	}
	token, err := codec.Encode(want)
	if err != nil {
		t.Fatal(err)
	}
	context := CursorContext{
		TenantID: want.TenantID, PrincipalID: want.PrincipalID, SecurityEpoch: want.SecurityEpoch,
		Route: want.Route, APIVersion: want.APIVersion, QueryDigest: want.QueryDigest,
	}
	got, err := codec.DecodeFor(token, context)
	if err != nil || got.LastKey != want.LastKey || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("DecodeFor() = %+v, %v", got, err)
	}
	context.TenantID = "another-tenant"
	if _, err := codec.DecodeFor(token, context); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("wrong-context error = %v", err)
	}
}

func TestCursorRejectsTamperingExpiryAndWeakKeys(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	if _, err := NewCursorCodec([]byte("short"), clock.Fixed{Time: now}); err == nil {
		t.Fatal("NewCursorCodec accepted weak key")
	}
	codec, _ := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"), clock.Fixed{Time: now})
	value := Cursor{
		TenantID: "t", PrincipalID: "p", Route: "/v1/sessions", APIVersion: "v1",
		QueryDigest: "digest", LastKey: "key", PageSizeCeiling: 50, ExpiresAt: now.Add(time.Minute),
	}
	token, err := codec.Encode(value)
	if err != nil {
		t.Fatal(err)
	}
	tampered := token[:len(token)-1] + strings.ToUpper(token[len(token)-1:])
	if tampered == token {
		tampered = token[:len(token)-1] + "A"
	}
	if _, err := codec.Decode(tampered); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("tampered cursor error = %v", err)
	}
	expired, _ := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"), clock.Fixed{Time: now.Add(2 * time.Minute)})
	if _, err := expired.Decode(token); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expired cursor error = %v", err)
	}
}
