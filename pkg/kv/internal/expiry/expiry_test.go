package expiry

import (
	"testing"
	"time"
)

// TestRoundTrip pins the header's meaning for the two backends that read and
// write it: the value survives, the expiry survives, and a future expiry reads
// as live.
func TestRoundTrip(t *testing.T) {
	expires := time.Now().Add(time.Hour).Truncate(time.Nanosecond)
	value, gotExpires, live := UnwrapFull(Wrap([]byte("value"), expires))
	if string(value) != "value" {
		t.Fatalf("the value behind the header is %q, want %q", value, "value")
	}
	if !gotExpires.Equal(expires) {
		t.Fatalf("the expiry came back as %v, want %v", gotExpires, expires)
	}
	if !live {
		t.Fatal("a value an hour from expiring reads as dead")
	}
}

// TestZeroExpiryMeansNoExpiry pins the sentinel in the header: a zero time is
// how both backends store a value that never ages out.
func TestZeroExpiryMeansNoExpiry(t *testing.T) {
	value, expires, live := UnwrapFull(Wrap([]byte("v"), time.Time{}))
	if string(value) != "v" || !expires.IsZero() || !live {
		t.Fatalf("a zero expiry came back as (%q, %v, live=%v), want the value, a zero time and live", value, expires, live)
	}
}

// TestPastExpiryReadsAsDead pins the other end: the header is what decides a
// key has aged out, not the backend.
func TestPastExpiryReadsAsDead(t *testing.T) {
	_, _, live := UnwrapFull(Wrap([]byte("v"), time.Now().Add(-time.Hour)))
	if live {
		t.Fatal("a value whose hour passed reads as live")
	}
}

// TestShortValueReadsAsUnexpiring pins the shape nothing this package wrote
// has: a stored value too short for a header is served as bytes with no expiry
// rather than refused, because a caller can act on the bytes and cannot act on
// an error saying the header is missing.
func TestShortValueReadsAsUnexpiring(t *testing.T) {
	value, expires, live := UnwrapFull([]byte("ab"))
	if string(value) != "ab" || !expires.IsZero() || !live {
		t.Fatalf("a short value came back as (%q, %v, live=%v), want it whole, with no expiry and live", value, expires, live)
	}
}
