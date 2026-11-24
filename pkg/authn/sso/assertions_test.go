package sso

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestRememberAssertionRecordsOnceAndRefusesAReplay checks that an assertion identifier is recorded once and a repeat is ErrAssertionReplay.
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

// TestRememberAssertionStoresTheConditionInUTC checks that RememberAssertion stores the condition in UTC.
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
	r.clock = newClock(base.In(newYork))
	r.p.clock = r.clock.read

	idp := r.putOIDC("tenant-a", "idp")
	cond := base.In(newYork).Add(-10 * time.Minute)
	if err := r.p.RememberAssertion(ctx, idp.ID, "a1", cond); err != nil {
		t.Fatalf("RememberAssertion: %v", err)
	}

	r.clock = newClock(time.Date(2026, time.March, 14, 9, 50, 0, 0, time.UTC))
	r.p.clock = r.clock.read
	if err := r.p.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := r.count(assertionTable); got != 1 {
		t.Errorf("after the sweep the replay line holds %d rows, want 1 (the condition was not stored as UTC)", got)
	}
}
