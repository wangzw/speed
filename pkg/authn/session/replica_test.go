package session

import (
	"context"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// TestARevocationReachesAnotherReplicaThroughTheEvent holds a revocation reaching another replica through the event.
func TestARevocationReachesAnotherReplicaThroughTheEvent(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		a := newFixture(t, d)
		a.accounts.add(alice, nil)
		issued := a.create(newBrowser(alice))
		b := a.replica()

		if got := outcomeOf(b.verify(issued.Token)); got.String() != "authenticated" {
			t.Fatalf("the second replica answered %s for a live session", got)
		}
		noErr(t, a.p.Revoke(context.Background(), issued.ID, authn.ReasonAdmin))

		if got := outcomeOf(b.verify(issued.Token)); got.String() != "authenticated" {
			t.Errorf("the second replica answered %s before its cache entry ran out", got)
		}

		b.bus.deliver(event.Event{ID: "rev-1", Type: authn.EventSessionRevoked, Payload: authn.SessionRevoked{
			SessionID: string(issued.ID), AccountID: string(alice), Scope: "one", ReasonClass: authn.ReasonAdmin,
		}})
		if got := outcomeOf(b.verify(issued.Token)); got.String() != "denied" {
			t.Errorf("the second replica answered %s after the event", got)
		}
	})
}

// TestARevocationReachesAnotherReplicaWhenItsEntryRunsOut holds a revocation reaching another replica when its cache entry runs out.
func TestARevocationReachesAnotherReplicaWhenItsEntryRunsOut(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		a := newFixture(t, d)
		a.accounts.add(alice, nil)
		issued := a.create(newBrowser(alice))
		b := a.replica()

		if got := outcomeOf(b.verify(issued.Token)); got.String() != "authenticated" {
			t.Fatalf("the second replica answered %s for a live session", got)
		}
		noErr(t, a.p.Revoke(context.Background(), issued.ID, authn.ReasonAdmin))

		if got := outcomeOf(b.verify(issued.Token)); got.String() != "authenticated" {
			t.Errorf("the second replica answered %s within check-ttl", got)
		}
		b.clock.Advance(defaults.CheckTTL + time.Second)
		if got := outcomeOf(b.verify(issued.Token)); got.String() != "denied" {
			t.Errorf("the second replica answered %s after its cache entry ran out", got)
		}
	})
}

// TestAStatusChangeReachesAnotherReplicaThroughTheEvent holds a status change reaching another replica through the event.
func TestAStatusChangeReachesAnotherReplicaThroughTheEvent(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		a := newFixture(t, d)
		a.accounts.add(alice, nil)
		issued := a.create(newBrowser(alice))
		b := a.replica()

		if got := outcomeOf(b.verify(issued.Token)); got.String() != "authenticated" {
			t.Fatalf("the second replica answered %s for a live session", got)
		}
		a.disable(alice)

		b.bus.deliver(event.Event{ID: "st-1", Type: authn.EventAccountStatusChanged, Payload: authn.AccountStatusChanged{
			AccountID: string(alice), From: authn.StatusActive.String(), To: authn.StatusDisabled.String(),
			ReasonClass: authn.ReasonAdmin,
		}})
		if got := outcomeOf(b.verify(issued.Token)); got.String() != "denied" {
			t.Errorf("the second replica answered %s after the status change", got)
		}
		if !b.row(issued.ID).RevokedAt.Valid {
			t.Error("the compensating cleanup did not revoke the session row")
		}
	})
}

// TestAnEndedImpersonationReachesAnotherReplicaThroughTheEvent holds an ended impersonation reaching another replica through the event.
func TestAnEndedImpersonationReachesAnotherReplicaThroughTheEvent(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		a := newFixture(t, d)
		_, token := impersonate(t, a)
		b := a.replica()

		if p := principalOf(b.verify(token)); p.Actor != admin {
			t.Fatalf("the second replica read the principal %+v, want the impersonation", p)
		}
		a.imp.end("grant-1")
		b.bus.deliver(event.Event{ID: "imp-1", Type: authn.EventImpersonationEnded, Payload: authn.ImpersonationEnded{
			GrantID: "grant-1", EndedBy: "actor",
		}})
		if p := principalOf(b.verify(token)); p.Actor != "" || p.Account != admin {
			t.Errorf("after the record ended the second replica read %+v, want the administrator as themselves", p)
		}
	})
}
