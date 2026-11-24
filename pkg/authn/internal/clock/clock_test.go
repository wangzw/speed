package clock

import (
	"strings"
	"testing"
	"time"
)

// TestNowIsInUTCWhateverTheLocalZoneIs holds the property the package exists
// for.
func TestNowIsInUTCWhateverTheLocalZoneIs(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	saved := time.Local
	time.Local = newYork
	t.Cleanup(func() { time.Local = saved })

	if got := Now().Location(); got != time.UTC {
		t.Errorf("Now() is in %v, want UTC", got)
	}
	if got := Source(nil).Now().Location(); got != time.UTC {
		t.Errorf("zero Source reads in %v, want UTC", got)
	}
}

// TestTheZeroSourceReadsTheSystemClock holds that a consumer which was never
// handed a source still gets a real time, not the zero time.
func TestTheZeroSourceReadsTheSystemClock(t *testing.T) {
	var s Source

	before := time.Now()
	got := s.Now()
	after := time.Now()

	if got.Before(before.Add(-time.Second)) || got.After(after.Add(time.Second)) {
		t.Errorf("zero Source read %v, want a time between %v and %v", got, before, after)
	}
}

// TestASourceInAnotherZoneYieldsTheSameInstantInUTC holds that normalising a
// reading neither shifts the instant nor leaves the zone as it came.
func TestASourceInAnotherZoneYieldsTheSameInstantInUTC(t *testing.T) {
	zone := time.FixedZone("UTC-5", -5*60*60)
	at := time.Date(2026, 10, 11, 7, 0, 0, 0, zone) // 12:00Z

	got := Source(func() time.Time { return at }).Now()

	if got.Location() != time.UTC {
		t.Errorf("Location() = %v, want UTC", got.Location())
	}
	if !got.Equal(at) {
		t.Errorf("instant moved: got %v, want %v", got, at)
	}
	if want := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC); got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestUTCConvertsANonUTCTimeAndKeepsTheZeroTimeZero holds both halves of the
// helper: a set time moves to UTC, an unset one is not turned into something
// that no longer reads as unset.
func TestUTCConvertsANonUTCTimeAndKeepsTheZeroTimeZero(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*60*60)
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, zone)

	if got := UTC(at); got.Location() != time.UTC || !got.Equal(at) {
		t.Errorf("UTC(%v) = %v, want the same instant in UTC", at, got)
	}

	zero := UTC(time.Time{})
	if !zero.IsZero() {
		t.Errorf("UTC(zero) = %v, want the zero time", zero)
	}
	if zero != (time.Time{}) {
		t.Errorf("UTC(zero) is not identical to the zero value: %#v", zero)
	}
}

// TestASourceReturningTheZeroTimeStaysZero holds that a consumer which treats
// the zero time as "unset" is not given a different value by the source.
func TestASourceReturningTheZeroTimeStaysZero(t *testing.T) {
	got := Source(func() time.Time { return time.Time{} }).Now()
	if !got.IsZero() {
		t.Errorf("got %v, want the zero time", got)
	}
}

// TestAReadingIsAWallClockInstant holds what the package documentation tells a
// consumer: the reading has no monotonic part, whereas time.Now has one, so
// in-memory lifetimes must not be measured with it.
func TestAReadingIsAWallClockInstant(t *testing.T) {
	const monotonic = "m="
	if !strings.Contains(time.Now().String(), monotonic) {
		t.Skip("this platform's time.Now carries no monotonic reading")
	}
	for name, got := range map[string]time.Time{
		"Now":        Now(),
		"UTC":        UTC(time.Now()),
		"Source.Now": Source(nil).Now(),
		"a source":   Source(time.Now).Now(),
	} {
		if strings.Contains(got.String(), monotonic) {
			t.Errorf("%s carries a monotonic reading", name)
		}
	}
}
