package sso

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestRememberAssertionRecordsOnceAndRefusesAReplay holds the replay line: the
// first write of an identifier is taken, a second is ErrAssertionReplay, and a
// different identifier of the same provider is taken on its own.
func TestRememberAssertionRecordsOnceAndRefusesAReplay(t *testing.T) {
	r := newRigSQLite(t)
	ctx := context.Background()
	idp := r.putOIDC("tenant-a", "idp")
	when := base.Add(10 * time.Minute)

	if err := r.p.RememberAssertion(ctx, idp.ID, "assertion-1", when); err != nil {
		t.Fatalf("first RememberAssertion: %v", err)
	}
	if err := r.p.RememberAssertion(ctx, idp.ID, "assertion-1", when); !errors.Is(err, authn.ErrAssertionReplay) {
		t.Fatalf("second RememberAssertion: err = %v, want ErrAssertionReplay", err)
	}
	if err := r.p.RememberAssertion(ctx, idp.ID, "assertion-2", when); err != nil {
		t.Fatalf("a different identifier of the same provider: %v", err)
	}
	if got := r.count(assertionTable); got != 2 {
		t.Errorf("the replay line holds %d rows, want 2", got)
	}
}

// TestRememberAssertionStoresTheConditionInUTC holds that the replay line's
// not_on_or_after is written UTC whatever zone the clock reads in, so the sweep's
// text comparison is between two UTC values on SQLite. The write happens under an
// America/New_York clock, and the sweep runs at an instant that falls between the
// value a local-zone write would leave and the UTC one, so only the UTC write
// survives.
func TestRememberAssertionStoresTheConditionInUTC(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	saved := time.Local
	time.Local = newYork
	t.Cleanup(func() { time.Local = saved })

	r := newRigSQLite(t)
	ctx := context.Background()
	// The clock reads in New York, which is not UTC.
	r.clock = newClock(base.In(newYork))
	r.p.clock = r.clock.read

	idp := r.putOIDC("tenant-a", "idp")
	// base is 12:00 UTC and 08:00 in New York; ten minutes earlier is 11:50 UTC
	// and 07:50 local. The condition is taken in the New York zone, so a write
	// that did not convert to UTC would leave the local value behind.
	cond := base.In(newYork).Add(-10 * time.Minute)
	if err := r.p.RememberAssertion(ctx, idp.ID, "a1", cond); err != nil {
		t.Fatalf("RememberAssertion: %v", err)
	}

	// Sweep at 09:50 UTC, which is after the value a local-zone write would have
	// left (07:50) and before the UTC one (11:50).
	r.clock = newClock(time.Date(2026, time.March, 14, 9, 50, 0, 0, time.UTC))
	r.p.clock = r.clock.read
	if err := r.p.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := r.count(assertionTable); got != 1 {
		t.Errorf("after the sweep the replay line holds %d rows, want 1 (the condition was not stored as UTC)", got)
	}
}
