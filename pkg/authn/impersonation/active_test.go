package impersonation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// TestActiveHoldsForAHealthyRecord holds the valid side: a record whose end has
// not passed, whose session is alive, whose actor and target are enabled and
// whose target is a member is active, and nothing is written.
func TestActiveHoldsForAHealthyRecord(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		got, active, err := r.p.Active(ctx, g.ID)
		if err != nil {
			t.Fatalf("Active: %v", err)
		}
		if !active || got.ID != g.ID {
			t.Errorf("Active = (%+v, %v), want the record active", got, active)
		}
		if ended, _, _ := r.stored(g.ID); ended != nil {
			t.Errorf("a valid record was written back ended: %v", *ended)
		}
	})
}

// TestActiveLapsesForEachReason holds the closed set of lapse causes one by one:
// each makes Active false, writes the end reason back with no initiator, and
// publishes the ended event except for an expiry, which has no owner. The event
// carries the cause category the design fixes for the end reason.
func TestActiveLapsesForEachReason(t *testing.T) {
	for _, c := range []struct {
		name        string
		mutate      func(r *rig)
		want        string
		wantEvent   bool
		wantEndedBy string
	}{
		{"expired", func(r *rig) { r.clock.advance(31 * time.Minute) }, authn.EndExpired, false, ""},
		{"session ended", func(r *rig) { r.sessions.session.Active = false }, authn.EndSessionEnded, true, "session-ended"},
		{"session gone", func(r *rig) { r.sessions.statusErr = authn.ErrSessionNotFound }, authn.EndSessionEnded, true, "session-ended"},
		{"actor disabled", func(r *rig) {
			r.accounts.put(actorID, authn.Account{Kind: authn.KindUser, Status: authn.StatusDisabled})
		}, authn.EndActorDisabled, true, "disabled"},
		{"actor gone", func(r *rig) { r.accounts.remove(actorID) }, authn.EndActorDisabled, true, "disabled"},
		{"target disabled", func(r *rig) {
			r.accounts.put(targetID, authn.Account{Kind: authn.KindUser, Status: authn.StatusDisabled})
		}, authn.EndTargetDisabled, true, "disabled"},
		{"target gone", func(r *rig) { r.accounts.remove(targetID) }, authn.EndTargetDisabled, true, "disabled"},
		{"membership removed", func(r *rig) { r.memberships.set(targetID, tenantA, false) }, authn.EndMembershipRemoved, true, "membership-removed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				ctx := context.Background()
				g, err := r.p.Start(ctx, adminIdentity(t), request())
				if err != nil {
					t.Fatalf("Start: %v", err)
				}
				c.mutate(r)

				_, active, err := r.p.Active(ctx, g.ID)
				if err != nil {
					t.Fatalf("Active: %v", err)
				}
				if active {
					t.Fatal("a lapsed record reads as active")
				}
				_, endedBy, reason := r.stored(g.ID)
				if reason == nil || *reason != c.want {
					t.Errorf("the end reason is %v, want %q", reason, c.want)
				}
				if endedBy != nil {
					t.Errorf("a lapse wrote an initiator %q, want none", *endedBy)
				}
				events := r.bus.ofType(authn.EventImpersonationEnded)
				if c.wantEvent && len(events) != 1 {
					t.Errorf("%d ended events were published, want 1", len(events))
				}
				if !c.wantEvent && len(events) != 0 {
					t.Errorf("an expiry published %d ended events, want none", len(events))
				}
				if c.wantEvent {
					if got := r.endedPayload().EndedBy; got != c.wantEndedBy {
						t.Errorf("the ended event's cause is %q, want %q", got, c.wantEndedBy)
					}
				}
			})
		})
	}
}

// TestActiveIsFalseForAnEndedRecord holds the first step the design fixes: a
// record that has already ended reads as false at once, however it was ended and
// even before its end time arrives. Nothing is written back.
func TestActiveIsFalseForAnEndedRecord(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		id := adminIdentity(t)
		g, err := r.p.Start(ctx, id, request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if endErr := r.p.End(ctx, id, "the call is over"); endErr != nil {
			t.Fatalf("End: %v", endErr)
		}
		endedAt, _, _ := r.stored(g.ID)
		events := len(r.bus.ofType(authn.EventImpersonationEnded))

		// The record's end time is still in the future, so only the ended check
		// can make Active false here.
		got, active, err := r.p.Active(ctx, g.ID)
		if err != nil {
			t.Fatalf("Active: %v", err)
		}
		if active {
			t.Errorf("an ended record reads as active: %+v", got)
		}
		if again, _, _ := r.stored(g.ID); again == nil || endedAt == nil || !again.Equal(*endedAt) {
			t.Errorf("Active rewrote an ended record: %v then %v", endedAt, again)
		}
		if len(r.bus.ofType(authn.EventImpersonationEnded)) != events {
			t.Error("Active published an ended event for an already ended record")
		}
	})
}

// TestActiveIsFalseWhenTheEndArrivesExactly holds the boundary of the second
// step: an end time equal to the current instant is not in the future, so the
// record has lapsed.
func TestActiveIsFalseWhenTheEndArrivesExactly(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		// The record ends 30 minutes from base; move the clock to exactly then.
		r.clock.advance(30 * time.Minute)
		_, active, err := r.p.Active(ctx, g.ID)
		if err != nil {
			t.Fatalf("Active: %v", err)
		}
		if active {
			t.Error("a record whose end time equals the instant still reads as active")
		}
		if _, _, reason := r.stored(g.ID); reason == nil || *reason != authn.EndExpired {
			t.Errorf("the end reason is %v, want expired", reason)
		}
	})
}

// TestALapseIsWrittenOnceAndOnlyOnce holds the conditional update as the arbiter:
// a second read of the same lapsed record writes nothing and publishes nothing,
// and the date the end was first written stands.
func TestALapseIsWrittenOnceAndOnlyOnce(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		r.clock.advance(31 * time.Minute)

		if _, active, err := r.p.Active(ctx, g.ID); err != nil || active {
			t.Fatalf("the first Active = (active %v, err %v), want a lapse", active, err)
		}
		first, _, _ := r.stored(g.ID)
		r.clock.advance(time.Minute)
		if _, active, err := r.p.Active(ctx, g.ID); err != nil || active {
			t.Fatalf("the second Active = (active %v, err %v), want a lapse", active, err)
		}
		second, _, _ := r.stored(g.ID)
		if first == nil || second == nil || !first.Equal(*second) {
			t.Errorf("the end was rewritten: %v then %v", first, second)
		}
		if got := r.counter(outcomeEnded); got != 1 {
			t.Errorf("the ended counter reads %d, want 1", got)
		}
	})
}

// TestASessionCanStartAgainAfterALapse holds the reason the lapse is written
// back: once the record has ended, the same session may begin another one, which
// the partial unique index would otherwise refuse.
func TestASessionCanStartAgainAfterALapse(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		first, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("the first Start: %v", err)
		}
		r.clock.advance(31 * time.Minute)
		if _, active, aerr := r.p.Active(ctx, first.ID); aerr != nil || active {
			t.Fatalf("the lapse was not read: (active %v, err %v)", active, aerr)
		}

		second, err := r.p.Start(ctx, adminIdentity(t, authntest.WithAuthTime(r.clock.read())), request())
		if err != nil {
			t.Fatalf("the second Start after a lapse: %v", err)
		}
		if second.ID == first.ID {
			t.Error("the second start reused the first record")
		}
		if got := r.recordCount(); got != 2 {
			t.Errorf("the table holds %d records, want the ended one and the new one", got)
		}
	})
}

// TestStartEndsALapsedRecordInItsOwnTransaction holds the other writer of a
// lapse: a start whose session already carries a lapsed but unended record ends
// that record in its own transaction before inserting the new one, so the
// partial unique index does not refuse the start.
func TestStartEndsALapsedRecordInItsOwnTransaction(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		first, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("the first Start: %v", err)
		}
		// Let the first lapse, without reading Active: nothing has ended it yet.
		r.clock.advance(31 * time.Minute)

		second, err := r.p.Start(ctx, adminIdentity(t, authntest.WithAuthTime(r.clock.read())), request())
		if err != nil {
			t.Fatalf("the second Start: %v", err)
		}
		_, _, reason := r.stored(first.ID)
		if reason == nil || *reason != authn.EndExpired {
			t.Errorf("the lapsed record's end reason is %v, want expired", reason)
		}
		if second.ID == first.ID {
			t.Error("the second start reused the first record")
		}
	})
}

// TestActiveReturnsFalseForAnUnknownRecord holds the first step: a record that is
// not there is false at once and is never written.
func TestActiveReturnsFalseForAnUnknownRecord(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		_, active, err := r.p.Active(context.Background(), "no-such-record")
		if err != nil {
			t.Fatalf("Active: %v", err)
		}
		if active {
			t.Error("an unknown record reads as active")
		}
	})
}

// TestActiveReturnsAnErrorWhenADependencyFails holds the failure direction: a
// dependency that cannot answer is an error and never a valid record.
func TestActiveReturnsAnErrorWhenADependencyFails(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		r.accounts.err = errFromStore
		_, active, err := r.p.Active(ctx, g.ID)
		if !errors.Is(err, errFromStore) {
			t.Fatalf("Active = %v, want the dependency's error", err)
		}
		if active {
			t.Error("a failed dependency read the record as active")
		}
	})
}
