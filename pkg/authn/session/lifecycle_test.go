package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// TestAccountDisabledRevokesTheCookieAndTokenKindsAndLeavesTheServiceOne revokes
// browser and token sessions and leaves the service row.
func TestAccountDisabledRevokesTheCookieAndTokenKindsAndLeavesTheServiceOne(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		ctx := context.Background()
		browser := f.create(newBrowser(alice))
		token := f.create(newToken1(alice))
		secret, err := f.p.ForCredential(ctx, alice, "secret-1", nil)
		noErr(t, err)

		f.clock.Advance(time.Minute)
		noErr(t, f.p.AccountDisabled(ctx, alice))

		for _, id := range []authn.SessionID{browser.ID, token.ID} {
			if !f.row(id).RevokedAt.Valid {
				t.Errorf("the %s session was not revoked", f.row(id).Kind)
			}
			if f.status(id).Active {
				t.Errorf("the %s session is still active", f.row(id).Kind)
			}
		}
		if f.row(secret).RevokedAt.Valid {
			t.Error("disabling the account revoked a service row, which only the secret ends")
		}
	})
}

// AccountEnabled changes nothing: a session of the enabled account stays as it
// is, and one the disable revoked does not come back.
func TestAccountEnabledLeavesTheSessionsAsTheyAre(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		ctx := context.Background()
		browser := f.create(newBrowser(alice))
		noErr(t, f.p.AccountEnabled(ctx, alice))
		if !f.status(browser.ID).Active {
			t.Error("enabling revoked a session of the enabled account")
		}
		f.clock.Advance(time.Minute)
		f.disable(alice)
		noErr(t, f.p.AccountDisabled(ctx, alice))
		f.enable(alice)
		noErr(t, f.p.AccountEnabled(ctx, alice))
		if !f.row(browser.ID).RevokedAt.Valid {
			t.Error("enabling brought back the session the disable revoked")
		}
	})
}

// AccountDeleted deletes every row of the account, service rows included.
func TestAccountDeletedRemovesEveryRow(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		f.accounts.add(bob, nil)
		ctx := context.Background()
		f.create(newBrowser(alice))
		f.create(newToken1(alice))
		_, err := f.p.ForCredential(ctx, alice, "secret-1", nil)
		noErr(t, err)
		others := f.create(newBrowser(bob))

		noErr(t, f.p.AccountDeleted(ctx, alice))
		if n := f.count(alice); n != 0 {
			t.Errorf("the deleted account still holds %d rows", n)
		}
		if f.row(others.ID).RevokedAt.Valid {
			t.Error("another account's session was touched")
		}
	})
}

// ServiceSecretRevoked revokes the service row of the revoked secret, and
// ForCredential then refuses it.
func TestServiceSecretRevokedRevokesTheServiceRow(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		ctx := context.Background()
		secret, err := f.p.ForCredential(ctx, alice, "secret-1", nil)
		noErr(t, err)

		noErr(t, f.p.ServiceSecretRevoked(ctx, alice, "secret-1"))
		if !f.row(secret).RevokedAt.Valid {
			t.Error("the service row was not revoked")
		}
		if _, err := f.p.ForCredential(ctx, alice, "secret-1", nil); !errors.Is(err, authn.ErrSessionRevoked) {
			t.Errorf("ForCredential = %v, want ErrSessionRevoked", err)
		}
	})
}

// The compensating cleanup runs from the event as well, so a hook that was missed
// leaves nothing behind once the event arrives.
func TestTheAccountDeletedEventRemovesTheRowsToo(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		f.create(newBrowser(alice))
		_, err := f.p.ForCredential(context.Background(), alice, "secret-1", nil)
		noErr(t, err)

		f.bus.deliver(event.Event{ID: "del-1", Type: authn.EventAccountDeleted, Payload: authn.AccountDeleted{
			AccountID: string(alice),
		}})
		if n := f.count(alice); n != 0 {
			t.Errorf("the event left %d rows", n)
		}
	})
}

// TestASessionOfAnAccountWhoseHookWasMissedIsStillInvalid refuses a session of a
// disabled account even when no cleanup ran.
func TestASessionOfAnAccountWhoseHookWasMissedIsStillInvalid(t *testing.T) {
	forEachDialect(t, func(t *testing.T, d dialectCase) {
		f := newFixture(t, d)
		f.accounts.add(alice, nil)
		issued := f.create(newBrowser(alice))
		if !f.status(issued.ID).Active {
			t.Fatal("the session is not active to begin with")
		}

		f.clock.Advance(time.Minute)
		f.disable(alice)
		f.p.caches().accounts.Invalidate(string(alice))

		if f.status(issued.ID).Active {
			t.Error("a session of a disabled account is active although no cleanup ran")
		}
		if got := outcomeOf(f.verify(issued.Token)); got.String() != "denied" {
			t.Errorf("the verdict is %s, want denied", got)
		}
	})
}

// An assembly with no event bus still revokes: the publish is skipped and the
// startup says so.
func TestAnAssemblyWithoutABusStillRevokes(t *testing.T) {
	f := newFixture(t, dialects[0], withoutBus())
	f.accounts.add(alice, nil)
	issued := f.create(newBrowser(alice))

	noErr(t, f.p.Revoke(context.Background(), issued.ID, authn.ReasonSelf))
	if !f.row(issued.ID).RevokedAt.Valid {
		t.Error("the session was not revoked without a bus")
	}
	f.p.diagnose()
	if out := f.logs.String(); !strings.Contains(out, "no event bus") {
		t.Errorf("the diagnostics do not say the bus is absent:\n%s", out)
	}
}

// TestTheAccountIsReadOnceWhileItsSnapshotHolds reads the account store once for
// repeated requests while the entry holds.
func TestTheAccountIsReadOnceWhileItsSnapshotHolds(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	var reads int
	f.accounts.hook(func() { reads++ })
	issued := f.create(newBrowser(alice))

	for i := range 3 {
		if got := outcomeOf(f.verify(issued.Token)); got.String() != "authenticated" {
			t.Fatalf("request %d: %s", i, got)
		}
	}
	if reads != 1 {
		t.Errorf("the account store was read %d times for three requests, want 1", reads)
	}
}

// TestAnImpersonationEndedWithNoRecordClearsEveryCachedAnswer clears every cached
// answer when the event names no record.
func TestAnImpersonationEndedWithNoRecordClearsEveryCachedAnswer(t *testing.T) {
	f := newFixture(t, dialects[0])
	_, token := impersonate(t, f)
	if p := principalOf(f.verify(token)); p.Actor != admin {
		t.Fatalf("the session is not an impersonation to begin with: %+v", p)
	}

	f.imp.end("grant-1")
	f.bus.deliver(event.Event{ID: "imp-all", Type: authn.EventImpersonationEnded, Payload: authn.ImpersonationEnded{}})
	if p := principalOf(f.verify(token)); p.Actor != "" || p.Account != admin {
		t.Errorf("after the record ended the identity is %+v, want the administrator as themselves", p)
	}
}
