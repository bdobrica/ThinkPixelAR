package outbox

import (
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func TestClaimRetryTakeoverAndDeliveryPreserveIdentity(t *testing.T) {
	now := time.Date(2026, 9, 3, 6, 30, 0, 0, time.UTC)
	ids := testIDs(t, now, 5)
	m, err := New(ids[0], ids[1], Envelope{Topic: "runtime.events", SchemaVersion: "v1", EventID: ids[2], AggregateType: "session", AggregateID: ids[3], AggregateVersion: 4, Payload: []byte(`{"state":"IDLE"}`), PayloadDigest: digest}, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Claim(ids[4], now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if m.Attempts() != 1 || m.ClaimFence() != 1 {
		t.Fatalf("claim attempts/fence = %d/%d", m.Attempts(), m.ClaimFence())
	}
	if err = m.Retry(ids[4], 1, now.Add(2*time.Minute), "UPSTREAM_UNAVAILABLE", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = m.Claim(ids[4], now.Add(4*time.Minute), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = m.MarkDelivered(ids[4], 1, now.Add(3*time.Minute)); err != ErrClaimConflict {
		t.Fatalf("stale delivery error = %v", err)
	}
	if err = m.MarkDelivered(ids[4], 2, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if m.ID() != ids[1] || m.Envelope().EventID != ids[2] || m.State() != Delivered {
		t.Fatalf("message identity/state changed: %#v", m)
	}
}

func TestExpiredClaimCanBeTakenOverAndDeadLettered(t *testing.T) {
	now := time.Date(2026, 9, 3, 6, 30, 0, 0, time.UTC)
	ids := testIDs(t, now, 6)
	m, _ := New(ids[0], ids[1], Envelope{Topic: "authority.results", SchemaVersion: "v1", EventID: ids[2], AggregateType: "execution", AggregateID: ids[3], AggregateVersion: 1, PayloadReference: "urn:thinkpixel:event:result", PayloadDigest: digest}, now, now)
	_ = m.Claim(ids[4], now.Add(time.Minute), now)
	if err := m.Claim(ids[5], now.Add(3*time.Minute), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := m.DeadLetter(ids[5], 2, DeadLetter{ReasonCode: "INVALID_DESTINATION", Detail: "registered topic unavailable"}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, ok := m.DeadLetterMetadata(); !ok || got.ReasonCode != "INVALID_DESTINATION" {
		t.Fatalf("dead letter = %#v, %v", got, ok)
	}
}

func TestRejectsUnboundedOrAmbiguousPayload(t *testing.T) {
	now := time.Now().UTC()
	ids := testIDs(t, now, 4)
	_, err := New(ids[0], ids[1], Envelope{Topic: "runtime.events", SchemaVersion: "v1", EventID: ids[2], AggregateType: "session", AggregateID: ids[3], AggregateVersion: 1, Payload: []byte(`{}`), PayloadReference: "both", PayloadDigest: digest}, now, now)
	if err != ErrInvalidMessage {
		t.Fatalf("New error = %v", err)
	}
}

const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testIDs(t *testing.T, now time.Time, count int) []primitives.ID {
	t.Helper()
	ids := make([]primitives.ID, count)
	for i := range ids {
		var err error
		ids[i], err = primitives.NewID(now.Add(time.Duration(i) * time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
	}
	return ids
}
