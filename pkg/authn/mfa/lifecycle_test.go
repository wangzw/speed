package mfa

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

func TestAccountDeletedHasEveryFactorCleanItsDataAndRemovesTheRows(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP, authn.FactorSMS}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		sms := r.p.factors[authn.FactorSMS].(*fakeFactor)
		active := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		pending := r.insertRow(acct, authn.FactorSMS, authn.FactorPending, r.clock.read(), nil)
		disabled := r.insertRow(acct, authn.FactorTOTP, authn.FactorDisabled, r.clock.read(), nil)
		unimplemented := r.activeFactor(acct, authn.FactorWebAuthn, time.Hour)
		theirs := r.activeFactor("someone-else", authn.FactorTOTP, time.Hour)

		if err := r.p.AccountDeleted(context.Background(), acct); err != nil {
			t.Fatalf("AccountDeleted: %v", err)
		}
		if got := append(slices.Clone(totp.removed), sms.removed...); !slices.Equal(sortedStrings(got), sortedStrings([]string{active, pending, disabled})) {
			t.Errorf("the factors were asked to clean %v, want one call for each of the rows they implement, in every status", got)
		}
		for _, id := range []string{active, pending, disabled, unimplemented} {
			if _, there := r.status(id); there {
				t.Errorf("the row %s is still there", id)
			}
		}
		if _, there := r.status(theirs); !there {
			t.Error("another account's row was removed")
		}
		// Idempotent: nothing left, nothing to fail on.
		if err := r.p.AccountDeleted(context.Background(), acct); err != nil {
			t.Errorf("a second call: %v", err)
		}
		if n := len(totp.removed) + len(sms.removed); n != 3 {
			t.Errorf("a second call asked the factors again (%d calls in all)", n)
		}
	})
}

func sortedStrings(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

func TestACleanupThatFailsIsReturnedAndTheRepeatFinishesIt(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		bad := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		good := r.activeFactor(acct, authn.FactorTOTP, 2*time.Hour)
		totp.removeFn = func(_ context.Context, _ authn.AccountID, rec authn.FactorRecord) error {
			if rec.ID == bad {
				return errors.New("seed store down")
			}
			return nil
		}
		err := r.p.AccountDeleted(context.Background(), acct)
		if !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("AccountDeleted = %v, want an error that is ErrUnavailable, which the account module reports as an incomplete cleanup", err)
		}
		if _, there := r.status(bad); !there {
			t.Error("the row whose data could not be cleaned was removed, so nobody could find that data again")
		}
		if _, there := r.status(good); there {
			t.Error("the row that could be cleaned was left because another could not")
		}
		totp.removeFn = nil
		if err := r.p.AccountDeleted(context.Background(), acct); err != nil {
			t.Errorf("the repeat: %v", err)
		}
		if _, there := r.status(bad); there {
			t.Error("the repeat left the row")
		}
	})
}

func TestADisabledAccountKeepsItsFactors(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		if err := r.p.AccountDisabled(context.Background(), acct); err != nil {
			t.Errorf("AccountDisabled: %v", err)
		}
		if err := r.p.ServiceSecretRevoked(context.Background(), acct, "secret-1"); err != nil {
			t.Errorf("ServiceSecretRevoked: %v", err)
		}
		if s, there := r.status(id); !there || s != authn.FactorActive {
			t.Error("a disabled account lost or changed a factor, which it needs back when it is enabled")
		}
		if r.p.factors[authn.FactorTOTP].(*fakeFactor).count("remove") != 0 {
			t.Error("a factor was asked to clean its data on a disable")
		}
	})
}

func deleted(id string, account authn.AccountID) event.Event {
	return event.Event{ID: id, Type: authn.EventAccountDeleted, Payload: authn.AccountDeleted{AccountID: string(account)}}
}

func TestTheDeletionEventCompensatesAndIsDeduplicatedByItsID(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		ctx := context.Background()
		if types := r.bus.subscribedTypes(); !slices.Equal(types, []string{authn.EventAccountDeleted}) {
			t.Fatalf("the module subscribed to %v, want the account deletion alone", types)
		}
		first := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		if err := r.bus.deliver(ctx, deleted("evt-1", acct)); err != nil {
			t.Fatalf("delivering: %v", err)
		}
		if _, there := r.status(first); there {
			t.Fatal("the deletion event did not clean the account")
		}

		// The same event again, at-least-once as some transports are, is not done a
		// second time: a row that appeared in between is left, which shows the
		// identifier was what stopped it. A different event for the account is.
		again := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		if err := r.bus.deliver(ctx, deleted("evt-1", acct)); err != nil {
			t.Fatal(err)
		}
		if _, there := r.status(again); !there {
			t.Error("the same event was handled twice")
		}
		if err := r.bus.deliver(ctx, deleted("evt-2", acct)); err != nil {
			t.Fatal(err)
		}
		if _, there := r.status(again); there {
			t.Error("a new event for the account did not clean it")
		}
	})
}

func TestAFailedCleanupIsNotMarkedDoneSoARedeliveryRunsIt(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		totp := r.p.factors[authn.FactorTOTP].(*fakeFactor)
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		totp.removeFn = func(context.Context, authn.AccountID, authn.FactorRecord) error { return errors.New("down") }
		if err := r.bus.deliver(context.Background(), deleted("evt-1", acct)); err == nil {
			t.Error("a cleanup that failed returned nothing to the bus")
		}
		totp.removeFn = nil
		if err := r.bus.deliver(context.Background(), deleted("evt-1", acct)); err != nil {
			t.Fatalf("the redelivery: %v", err)
		}
		if _, there := r.status(id); there {
			t.Error("the redelivery of an event whose cleanup had failed was skipped as done")
		}
	})
}

func TestADeletionEventThatCannotBeReadIsDropped(t *testing.T) {
	onEachDialect(t, []authn.FactorKind{authn.FactorTOTP}, func(t *testing.T, r *rig) {
		id := r.activeFactor(acct, authn.FactorTOTP, time.Hour)
		for name, evt := range map[string]event.Event{
			"no payload":          {ID: "a", Type: authn.EventAccountDeleted},
			"not an object":       {ID: "b", Type: authn.EventAccountDeleted, Payload: "text"},
			"no account":          {ID: "c", Type: authn.EventAccountDeleted, Payload: authn.AccountDeleted{}},
			"a malformed account": {ID: "d", Type: authn.EventAccountDeleted, Payload: authn.AccountDeleted{AccountID: "no spaces/allowed"}},
		} {
			if err := r.bus.deliver(context.Background(), evt); err != nil {
				t.Errorf("%s: the handler returned %v, want it to drop what it cannot read", name, err)
			}
		}
		if _, there := r.status(id); !there {
			t.Error("an event that named no account cleaned one")
		}
	})
}

func TestTheSeenEventsAreBounded(t *testing.T) {
	s := newSeenEvents()
	for i := 0; i < seenCapacity+50; i++ {
		s.mark(fmt.Sprintf("evt-%d", i))
	}
	if s.has("evt-0") {
		t.Error("the oldest identifier is still remembered past the capacity")
	}
	if !s.has(fmt.Sprintf("evt-%d", seenCapacity+49)) {
		t.Error("the newest identifier was not remembered")
	}
	if len(s.ids) != seenCapacity || s.order.Len() != seenCapacity {
		t.Errorf("the set holds %d identifiers, want at most %d", len(s.ids), seenCapacity)
	}
	s.mark(fmt.Sprintf("evt-%d", seenCapacity+49))
	if s.order.Len() != seenCapacity {
		t.Error("marking an identifier twice grew the set")
	}
}
