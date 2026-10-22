package account

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// identifierOfKind reads the one identifier of a kind the account holds.
func identifierOfKind(t *testing.T, h *harness, id authn.AccountID, kind authn.IdentifierKind) authn.Identifier {
	t.Helper()
	list, err := h.p.Identifiers(context.Background(), id)
	if err != nil {
		t.Fatalf("listing identifiers: %v", err)
	}
	for _, item := range list {
		if item.Kind == kind {
			return item
		}
	}
	t.Fatalf("account %s has no %s identifier", id, kind)
	return authn.Identifier{}
}

// TestRemovingTheLastLoginNameOfAPasswordAccountIsRefused holds that an account
// holding a password credential cannot lose its last username or email address:
// the removal answers authn.ErrIdentifierRequired, the transaction rolls back,
// the identifier stays held and no removal event is published.
func TestRemovingTheLastLoginNameOfAPasswordAccountIsRefused(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()

		usernameOnly, err := h.p.Create(ctx, authn.CreateAccount{
			Username: "onlyname", PasswordHash: cheapHash(t, "correct horse battery"), Via: "admin",
		})
		if err != nil {
			t.Fatalf("creating an account with only a username: %v", err)
		}
		before := len(h.bus.of(authn.EventIdentifierChanged))
		usernameID := identifierOfKind(t, h, usernameOnly.ID, authn.IdentifierUsername).ID
		if err = h.p.RemoveIdentifier(ctx, usernameOnly.ID, usernameID); !errors.Is(err, authn.ErrIdentifierRequired) {
			t.Fatalf("removing the only login name gave %v, want ErrIdentifierRequired", err)
		}
		if got, lookupErr := h.p.Lookup(ctx, authn.IdentifierUsername, "onlyname"); lookupErr != nil || got != usernameOnly.ID {
			t.Errorf("the refused removal left the name found at %q, %v, want %q", got, lookupErr, usernameOnly.ID)
		}
		if n := h.count(tableIdentifiers, "id = ?", usernameID); n != 1 {
			t.Errorf("the refused removal left %d identifier row(s), want the row kept", n)
		}
		if got := len(h.bus.of(authn.EventIdentifierChanged)); got != before {
			t.Errorf("the refused removal published %d event(s)", got-before)
		}

		emailOnly, err := h.p.Create(ctx, authn.CreateAccount{
			Email:            "only@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin},
			PasswordHash:     cheapHash(t, "correct horse battery"),
			Via:              "admin",
		})
		if err != nil {
			t.Fatalf("creating an account with only an email address: %v", err)
		}
		emailID := identifierOfKind(t, h, emailOnly.ID, authn.IdentifierEmail).ID
		if err = h.p.RemoveIdentifier(ctx, emailOnly.ID, emailID); !errors.Is(err, authn.ErrIdentifierRequired) {
			t.Fatalf("removing the only email address gave %v, want ErrIdentifierRequired", err)
		}
		if got, lookupErr := h.p.Lookup(ctx, authn.IdentifierEmail, "only@example.com"); lookupErr != nil || got != emailOnly.ID {
			t.Errorf("the refused removal left the address found at %q, %v, want %q", got, lookupErr, emailOnly.ID)
		}
	})
}

// TestRemovingALoginNameIsAllowedWhileAnotherRemains holds that a password
// account may lose one of its login names as long as another stays, and that the
// last one is then refused.
func TestRemovingALoginNameIsAllowedWhileAnotherRemains(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("both", "correct horse battery")

		usernameID := identifierOfKind(t, h, id, authn.IdentifierUsername).ID
		if err := h.p.RemoveIdentifier(ctx, id, usernameID); err != nil {
			t.Fatalf("removing the username while an email address remains gave %v, want nil", err)
		}
		if _, err := h.p.Lookup(ctx, authn.IdentifierEmail, "both@example.com"); err != nil {
			t.Fatalf("the email address is not found after removing the username: %v", err)
		}

		emailID := identifierOfKind(t, h, id, authn.IdentifierEmail).ID
		if err := h.p.RemoveIdentifier(ctx, id, emailID); !errors.Is(err, authn.ErrIdentifierRequired) {
			t.Fatalf("removing the last login name gave %v, want ErrIdentifierRequired", err)
		}
		if _, err := h.p.Lookup(ctx, authn.IdentifierEmail, "both@example.com"); err != nil {
			t.Errorf("the refused removal left the email address unfound: %v", err)
		}
	})
}

// TestAnAccountWithoutAPasswordMayLoseItsLastLoginName holds that the rule binds
// only accounts holding a password credential: one that signs in another way
// gives up its last username freely.
func TestAnAccountWithoutAPasswordMayLoseItsLastLoginName(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id, err := h.p.Create(ctx, authn.CreateAccount{Username: "nopass", Via: "admin"})
		if err != nil {
			t.Fatalf("creating an account without a password: %v", err)
		}
		if err = h.p.RemoveIdentifier(ctx, id.ID, identifierOfKind(t, h, id.ID, authn.IdentifierUsername).ID); err != nil {
			t.Fatalf("removing the last login name of an account without a password gave %v, want nil", err)
		}
		if held, err := h.p.Identifiers(ctx, id.ID); err != nil || len(held) != 0 {
			t.Errorf("after the removal the account holds %v, %v, want no identifier", held, err)
		}
	})
}

// TestRemovingAPhoneNeverHitsTheLoginNameRule holds that the rule is about
// username and email addresses: a phone number is removed even when the account
// holds a password credential and nothing else that serves as a login name.
func TestRemovingAPhoneNeverHitsTheLoginNameRule(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("phoneholder", "correct horse battery")
		smsCode := authn.Verification{Via: authn.VerifiedViaSMSCode}
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierPhone, "+8613800000001", smsCode); err != nil {
			t.Fatalf("adding a phone number: %v", err)
		}
		if err := h.p.RemoveIdentifier(ctx, id, identifierOfKind(t, h, id, authn.IdentifierPhone).ID); err != nil {
			t.Errorf("removing the phone number gave %v, want nil", err)
		}

		// A password account with no login name, which an account written around
		// this rule can hold, still loses its phone number.
		if err := h.handle.Exec("DELETE FROM "+tableIdentifiers+" WHERE account_id = ? AND kind IN ?",
			id.String(), []string{string(authn.IdentifierUsername), string(authn.IdentifierEmail)}).Error; err != nil {
			t.Fatalf("clearing the login names: %v", err)
		}
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierPhone, "+8613800000002", smsCode); err != nil {
			t.Fatalf("adding a second phone number: %v", err)
		}
		if err := h.p.RemoveIdentifier(ctx, id, identifierOfKind(t, h, id, authn.IdentifierPhone).ID); err != nil {
			t.Errorf("removing a phone number from an account with no login name gave %v, want nil", err)
		}
	})
}
