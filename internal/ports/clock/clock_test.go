package clock

import (
	"testing"
	"time"
)

func TestClocksReturnUTC(t *testing.T) {
	local := time.Date(2026, 8, 31, 12, 0, 0, 0, time.FixedZone("test", 3*60*60))
	if got := (Fixed{Time: local}).Now(); got.Location() != time.UTC || got.Hour() != 9 {
		t.Fatalf("Fixed.Now() = %v, want 09:00 UTC", got)
	}
	if got := (UTC{}).Now(); got.Location() != time.UTC {
		t.Fatalf("UTC.Now() location = %v, want UTC", got.Location())
	}
}
