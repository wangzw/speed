package sms

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/sms/budget"
)

// TestSendVerifyAndVoidWorkOnEveryDialect holds the native SQL on both engines:
// a challenge is written, the code passes once, a replay fails, and the row is
// consumed.
func TestSendVerifyAndVoidWorkOnEveryDialect(t *testing.T) {
	forEachDialect(t, func(t *testing.T, database db.Database, _ string) {
		r := newRig(t, database, newSender())
		r.allow()
		id, code := r.sentCode()
		if err := r.p.Verify(context.Background(), id, code); err != nil {
			t.Fatalf("Verify with the right code: %v", err)
		}
		if err := r.p.Verify(context.Background(), id, code); !errors.Is(err, authn.ErrInvalidCredentials) {
			t.Fatalf("the replay returned %v, want ErrInvalidCredentials", err)
		}
	})
}

// TestTheDeploymentBudgetCountsOnlyRealRowsOnEveryDialect holds the global
// dimension's predicate on both engines: the ledger stores suppressed as an
// integer on SQLite and a boolean on PostgreSQL, so the predicate has to select
// the real row and leave the suppressed one out on each. A suppressed send is
// written through Send, which is the path that writes both the challenge and the
// ledger row, and the real sends that follow show the suppressed one spent
// nothing of the purpose's budget.
func TestTheDeploymentBudgetCountsOnlyRealRowsOnEveryDialect(t *testing.T) {
	forEachDialect(t, func(t *testing.T, database db.Database, _ string) {
		r := newRig(t, database, newSender())
		r.allow()
		r.liftBudgets()
		r.store.set(resendIntervalPath, 30*time.Second)
		r.store.set(globalLoginPath, 1)

		if _, err := r.p.Send(context.Background(), authn.SendCode{
			Purpose: authn.SMSPurposeLogin, Phone: phone, Deliver: false,
		}); err != nil {
			t.Fatalf("the suppressed send: %v", err)
		}
		r.clock.advance(31 * time.Second)
		if _, err := r.p.Send(context.Background(), authn.SendCode{
			Purpose: authn.SMSPurposeLogin, Phone: "+8613800138001", Deliver: true,
		}); err != nil {
			t.Fatalf("the first real send returned %v, want success: the suppressed row spent the global budget", err)
		}
		r.clock.advance(31 * time.Second)
		if _, err := r.p.Send(context.Background(), authn.SendCode{
			Purpose: authn.SMSPurposeLogin, Phone: "+8613800138002", Deliver: true,
		}); !errors.Is(err, authn.ErrThrottled) {
			t.Fatalf("the second real send returned %v, want ErrThrottled: the first real send did not spend the budget", err)
		}
	})
}

// TestTheWindowCountingHoldsInAnotherLocalZone holds the design's timezone
// hypothesis on SQLite: with the process's local zone set elsewhere, the module
// still writes and binds UTC, so a row inside the hour is counted and one outside
// it is not.
func TestTheWindowCountingHoldsInAnotherLocalZone(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("loading the zone: %v", err)
	}
	saved := time.Local
	time.Local = newYork
	t.Cleanup(func() { time.Local = saved })

	r := newRig(t, openSQLite(t), newSender())
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)

	phoneMac := r.database.BlindIndex(phone)
	if _, sendErr := r.sendLogin(); sendErr != nil {
		t.Fatalf("Send: %v", sendErr)
	}
	window := func() int64 {
		return r.ledgerCount(budget.Query{
			Channel:           budget.ChannelAuthn,
			Dimension:         budget.DimensionDestination,
			Value:             phoneMac,
			Since:             r.clock.read().Add(-time.Hour),
			IncludeSuppressed: true,
		})
	}
	if n := window(); n != 1 {
		t.Errorf("a send made in another local zone counts as %d rows within the hour, want 1", n)
	}
	// A row older than the hour is outside the window.
	r.clock.advance(2 * time.Hour)
	if n := window(); n != 0 {
		t.Errorf("a row two hours old counts as %d rows within the hour, want 0", n)
	}
}

// TestTheBudgetComparesAcrossZones holds that a time read in another zone is
// still bound and compared as UTC: with the clock in America/New_York, a resend
// within the interval is throttled and one after it goes through.
func TestTheBudgetComparesAcrossZones(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("loading the zone: %v", err)
	}
	forEachDialect(t, func(t *testing.T, database db.Database, _ string) {
		r := newRig(t, database, newSender())
		r.allow()
		r.setClock(base.In(ny))
		r.store.set(resendIntervalPath, 60*time.Second)

		if _, err := r.sendLogin(); err != nil {
			t.Fatalf("the first Send: %v", err)
		}
		r.clock.advance(30 * time.Second)
		if _, err := r.sendLogin(); !errors.Is(err, authn.ErrThrottled) {
			t.Fatalf("a resend in another zone returned %v, want ErrThrottled", err)
		}
		r.clock.advance(31 * time.Second)
		if _, err := r.sendLogin(); err != nil {
			t.Fatalf("a send after the interval returned %v, want success", err)
		}
	})
}
