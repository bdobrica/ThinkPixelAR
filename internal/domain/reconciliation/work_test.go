package reconciliation

import (
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"testing"
	"time"
)

func TestFencedClaimRenewRescheduleAndTakeover(t *testing.T) {
	now := time.Date(2026, 9, 3, 6, 30, 0, 0, time.UTC)
	ids := make([]primitives.ID, 5)
	for i := range ids {
		ids[i], _ = primitives.NewID(now.Add(time.Duration(i) * time.Millisecond))
	}
	w, err := New(ids[0], ids[1], "session.reconcile", "session", ids[2], now, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Claim(ids[3], now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if err = w.Renew(ids[3], 1, now.Add(2*time.Minute), now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = w.Claim(ids[4], now.Add(3*time.Minute), now.Add(time.Minute)); err != ErrClaimConflict {
		t.Fatalf("live takeover = %v", err)
	}
	if err = w.Claim(ids[4], now.Add(4*time.Minute), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = w.Reschedule(ids[3], 1, now.Add(5*time.Minute), "RETRY", now.Add(3*time.Minute)); err != ErrClaimConflict {
		t.Fatalf("stale reschedule = %v", err)
	}
	if err = w.Reschedule(ids[4], 2, now.Add(5*time.Minute), "PROVIDER_UNAVAILABLE", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if w.State() != Pending || w.Attempts() != 2 || w.ClaimFence() != 2 {
		t.Fatalf("work = %#v", w)
	}
}

func TestCompleteRequiresLiveClaim(t *testing.T) {
	now := time.Now().UTC()
	ids := make([]primitives.ID, 4)
	for i := range ids {
		ids[i], _ = primitives.NewID(now.Add(time.Duration(i) * time.Millisecond))
	}
	w, _ := New(ids[0], ids[1], "execution.reconcile", "execution", ids[2], now, now)
	_ = w.Claim(ids[3], now.Add(time.Minute), now)
	if err := w.Complete(ids[3], 1, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if w.State() != Completed {
		t.Fatalf("state = %s", w.State())
	}
	if err := w.Claim(ids[3], now.Add(2*time.Minute), now.Add(time.Minute)); err != ErrClaimConflict {
		t.Fatalf("terminal claim = %v", err)
	}
}
