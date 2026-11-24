package session

import (
	"testing"
	"time"
)

// SQLite keeps a time as text and compares it as a string, so a reading left
// in the machine's zone would not compare with one written in UTC.
func TestTheSessionPeriodsAreJudgedInUTCUnderANonUTCLocalZone(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	saved := time.Local
	time.Local = newYork
	t.Cleanup(func() { time.Local = saved })

	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	// The clock reads in the local zone, as a wall clock would.
	start := time.Date(2026, time.October, 11, 8, 0, 0, 0, newYork)
	f.clock.mu.Lock()
	f.clock.t = start
	f.clock.mu.Unlock()

	issued := f.create(newBrowser(alice))
	if got := outcomeOf(f.verify(issued.Token)); got.String() != "authenticated" {
		t.Fatalf("the verify under a non-UTC local zone answered %s", got)
	}

	row := f.row(issued.ID)
	if row.CreatedAt.Location() != time.UTC {
		t.Errorf("the created_at reads in %v, want UTC", row.CreatedAt.Location())
	}
	if want := start.Add(defaults.AbsoluteTimeout); !row.ExpiresAt.Time.Equal(want) {
		t.Errorf("expires_at = %s, want the absolute period %s from the creation", row.ExpiresAt.Time, want)
	}

	f.clock.Advance(defaults.IdleTimeout + time.Second)
	if got := outcomeOf(f.verify(issued.Token)); got.String() != "denied" {
		t.Errorf("past the idle period the verdict is %s, want denied", got)
	}
}

// The valid-after comparison against the account is in UTC too: a session made
// before the account's valid-after instant is refused whatever the local zone.
func TestTheValidAfterComparisonHoldsUnderANonUTCLocalZone(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	saved := time.Local
	time.Local = newYork
	t.Cleanup(func() { time.Local = saved })

	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	f.clock.mu.Lock()
	f.clock.t = time.Date(2026, time.October, 11, 8, 0, 0, 0, newYork)
	f.clock.mu.Unlock()
	issued := f.create(newBrowser(alice))

	f.clock.Advance(time.Minute)
	f.disable(alice)
	f.enable(alice)
	f.p.caches().accounts.Invalidate(string(alice))

	if got := outcomeOf(f.verify(issued.Token)); got.String() != "denied" {
		t.Errorf("a session older than valid_after is %s under a non-UTC local zone, want denied", got)
	}
}
