package sso

import (
	"context"
	"testing"
	"time"
)

// TestTheSweepsHoldUnderAnotherLocalZone checks that both sweeps hold when the clock reads in another zone.
func TestTheSweepsHoldUnderAnotherLocalZone(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	saved := time.Local
	time.Local = newYork
	t.Cleanup(func() { time.Local = saved })

	r := newRigSQLite(t)
	r.clock = newClock(base.In(newYork))
	r.p.clock = r.clock.read

	if got := r.p.now().Location(); got != time.UTC {
		t.Fatalf("now() is in %v, want UTC", got)
	}

	idp := r.putOIDC("tenant-a", "idp")
	if _, err := r.p.ClaimDomain(context.Background(), idp.ID, "example.com"); err != nil {
		t.Fatalf("ClaimDomain: %v", err)
	}
	r.insertAssertion(idp.ID, "expired", base.In(newYork).Add(-time.Minute), base.In(newYork).Add(-10*time.Minute))

	r.clock.advance(8 * 24 * time.Hour)
	if err := r.p.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if r.count(domainTable) != 0 {
		t.Error("the unverified claim past the window was not removed under a non-UTC zone")
	}
	if r.count(assertionTable) != 0 {
		t.Error("the expired assertion was not removed under a non-UTC zone")
	}
}
