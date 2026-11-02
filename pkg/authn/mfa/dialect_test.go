package mfa

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// newYork is a zone four or five hours behind UTC, which is what makes a time that
// was not converted compare wrongly against one that was: SQLite compares a time
// as text in the zone it carries.
func newYork(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tz database here: %v", err)
	}
	return loc
}

// A clock that reads in New York, and rows another replica wrote in UTC, are the
// two halves of the comparison. Every comparison the module makes has to come out
// the same as it does in UTC.
func TestComparisonsAreRightWhenTheClockReadsInAnotherZone(t *testing.T) {
	ny := newYork(t)
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h, newFactor(authn.FactorTOTP))
		r.clock.now = base.In(ny)
		if r.p.clock.Now().Location() != time.UTC {
			t.Fatalf("the module's reading of the clock is in %v, want UTC", r.p.clock.Now().Location())
		}
		now := base // the same instant, in UTC, for what "another replica" writes

		// Seasoned: the line is now - 24h, and a row at it counts.
		at := func(d time.Duration) *time.Time { v := now.Add(-d); return &v }
		r.insertRow("exactly", authn.FactorTOTP, authn.FactorActive, now.Add(-48*time.Hour), at(24*time.Hour))
		r.insertRow("a-second-short", authn.FactorTOTP, authn.FactorActive, now.Add(-48*time.Hour), at(24*time.Hour-time.Second))
		r.insertRow("a-second-over", authn.FactorTOTP, authn.FactorActive, now.Add(-48*time.Hour), at(24*time.Hour+time.Second))
		for account, want := range map[authn.AccountID]bool{"exactly": true, "a-second-short": false, "a-second-over": true} {
			got, err := r.p.Seasoned(context.Background(), account)
			if err != nil || got != want {
				t.Errorf("Seasoned(%s) = %v, %v, want %v", account, got, err, want)
			}
		}

		// The sweep: the line is now - 1h.
		over := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, now.Add(-time.Hour-time.Second), nil)
		exact := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, now.Add(-time.Hour), nil)
		short := r.insertRow(acct, authn.FactorTOTP, authn.FactorPending, now.Add(-time.Hour+time.Second), nil)
		if n, err := r.p.Sweep(context.Background()); err != nil || n != 1 {
			t.Errorf("Sweep = %d, %v, want 1", n, err)
		}
		for id, want := range map[string]bool{over: false, exact: true, short: true} {
			if _, there := r.status(id); there != want {
				t.Errorf("the row %s is there = %v, want %v", id, there, want)
			}
		}
	})
}

// The register's statements convert a time they are given: a caller that hands
// them one in another zone gets the same answer as one that hands them UTC.
func TestTheRegisterConvertsATimeItIsGivenToUTC(t *testing.T) {
	ny := newYork(t)
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h, newFactor(authn.FactorTOTP))
		ctx := context.Background()
		local := base.In(ny)

		rec, err := r.p.register.insertPending(ctx, acct, authn.FactorTOTP, "", 5, false, local)
		if err != nil {
			t.Fatal(err)
		}
		if !rec.CreatedAt.Equal(base) || rec.CreatedAt.Location() != time.UTC {
			t.Errorf("the pending row's creation time is %v, want the instant in UTC", rec.CreatedAt)
		}
		won, err := r.p.register.activate(ctx, acct, rec.ID, local)
		if err != nil || !won {
			t.Fatalf("activate = %v, %v", won, err)
		}
		if terr := r.p.register.touch(ctx, rec.ID, local.Add(time.Minute)); terr != nil {
			t.Fatal(terr)
		}
		got, found, err := r.p.register.find(ctx, acct, rec.ID)
		if err != nil || !found {
			t.Fatalf("find = %v, %v", found, err)
		}
		if !got.ActivatedAt.Equal(base) || !got.CreatedAt.Equal(base) || !got.LastUsedAt.Equal(base.Add(time.Minute)) {
			t.Errorf("the row reads back as %+v, want the instants written", got)
		}
		for name, at := range map[string]time.Time{"activated": got.ActivatedAt, "created": got.CreatedAt, "last used": got.LastUsedAt} {
			if at.Location() != time.UTC {
				t.Errorf("the %s time reads back in %v, want UTC", name, at.Location())
			}
		}

		// Read raw, the written time is text in UTC on SQLite and the same instant on
		// PostgreSQL.
		var raw []struct{ Activated string }
		if qerr := r.h.Raw("SELECT CAST(activated_at AS TEXT) AS activated FROM authn_factors WHERE id = ?", rec.ID).Scan(&raw).Error; qerr != nil || len(raw) != 1 {
			t.Fatalf("reading the column: %v %v", raw, qerr)
		}
		if strings.Contains(raw[0].Activated, "-04:00") || strings.Contains(raw[0].Activated, "-05:00") || strings.Contains(raw[0].Activated, "EDT") || strings.Contains(raw[0].Activated, "EST") {
			t.Errorf("the activation time is stored as %q, which is in New York's zone", raw[0].Activated)
		}

		// A line handed in as a New York time cuts at the same instant.
		yes, err := r.p.register.seasoned(ctx, acct, local.Add(time.Second))
		if err != nil || !yes {
			t.Errorf("seasoned just after the activation = %v, %v, want true", yes, err)
		}
		no, err := r.p.register.seasoned(ctx, acct, local.Add(-time.Second))
		if err != nil || no {
			t.Errorf("seasoned just before the activation = %v, %v, want false", no, err)
		}
		pending := r.insertRow(acct, authn.FactorSMS, authn.FactorPending, base.Add(-time.Minute), nil)
		list, err := r.p.register.expiredPending(ctx, local)
		if err != nil || len(list) != 1 || list[0].ID != pending {
			t.Errorf("expiredPending = %+v, %v, want the one row older than the line", list, err)
		}
		none, err := r.p.register.expiredPending(ctx, local.Add(-2*time.Minute))
		if err != nil || len(none) != 0 {
			t.Errorf("expiredPending with an earlier line = %+v, %v, want none", none, err)
		}
	})
}

// A registration finished while the clock reads in another zone is activated at
// the instant and Seasoned counts from it.
func TestARegistrationFinishedUnderAnotherZoneIsActiveAtTheInstant(t *testing.T) {
	ny := newYork(t)
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		r := newRig(t, h, newFactor(authn.FactorTOTP))
		r.clock.now = base.In(ny)
		got, err := r.p.Enroll(context.Background(), flow(acct), authn.FactorTOTP, authn.StepInput{})
		if err != nil || step.KindOf(got) != step.KindEnrolled {
			t.Fatalf("Enroll = %v, %v", step.KindOf(got), err)
		}
		rec := r.rows(acct)[0]
		if !rec.ActivatedAt.Equal(base) {
			t.Errorf("the factor was activated at %v, want %v", rec.ActivatedAt, base)
		}
		r.clock.advance(24*time.Hour - time.Second)
		if ok, _ := r.p.Seasoned(context.Background(), acct); ok {
			t.Error("a factor a second short of a day old is seasoned")
		}
		r.clock.advance(time.Second)
		if ok, _ := r.p.Seasoned(context.Background(), acct); !ok {
			t.Error("a factor a day old is not seasoned")
		}
	})
}
