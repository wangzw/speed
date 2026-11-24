package impersonation

import (
	"context"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

const target2 = authn.AccountID("acct-user-2")

// TestStartSettlesALapsedRecordWithItsOwnEvent checks that a start ends a lapsed
// record in its transaction and, after commit, counts and publishes its ended event.
func TestStartSettlesALapsedRecordWithItsOwnEvent(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		r.accounts.put(target2, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
		r.memberships.set(target2, tenantA, true)
		firstRequest := request()
		firstRequest.Target = target2
		first, err := r.p.Start(ctx, adminIdentity(t), firstRequest)
		if err != nil {
			t.Fatalf("the first Start: %v", err)
		}

		r.memberships.set(target2, tenantA, false)
		second, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("the second Start: %v", err)
		}
		_, endedBy, reason := r.stored(first.ID)
		if reason == nil || *reason != authn.EndMembershipRemoved {
			t.Errorf("the lapsed record's end reason is %q, want membership-removed", reasonText(reason))
		}
		if endedBy != nil {
			t.Errorf("a lapse wrote an initiator %q, want none", *endedBy)
		}
		payload := r.endedPayload()
		if payload.GrantID != first.ID || payload.EndedBy != "membership-removed" {
			t.Errorf("the ended payload is %+v, want the lapsed record and its cause category", payload)
		}
		if got := r.counter(outcomeEnded); got != 1 {
			t.Errorf("the ended counter reads %d, want 1", got)
		}
		if second.ID == first.ID {
			t.Error("the second start reused the first record")
		}
	})
}

// TestStartAnnouncesNoLapseWhenItsRecordTransactionRollsBack checks that a rolled-back record transaction leaves neither the lapse nor its count and event.
func TestStartAnnouncesNoLapseWhenItsRecordTransactionRollsBack(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		first, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("the first Start: %v", err)
		}
		r.clock.advance(31 * time.Minute)

		if _, err := r.p.record(ctx, adminIdentity(t), request(), 30*time.Minute, first.ID, r.clock.read()); err == nil {
			t.Fatal("record = nil error, want the insert's failure")
		}
		if endedAt, _, _ := r.stored(first.ID); endedAt != nil {
			t.Errorf("a rolled-back transaction ended the prior record: %v", *endedAt)
		}
		if events := r.bus.ofType(authn.EventImpersonationEnded); len(events) != 0 {
			t.Errorf("a rolled-back transaction published %d ended events, want none", len(events))
		}
		if got := r.counter(outcomeEnded); got != 0 {
			t.Errorf("a rolled-back transaction counted %d ended sessions, want 0", got)
		}
		if got := r.recordCount(); got != 1 {
			t.Errorf("the table holds %d records, want the first one alone", got)
		}
	})
}
