package account

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

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

// Removing the last login name of a password account is refused under both login-name rules.
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
		if err = h.p.RemoveIdentifier(ctx, usernameOnly.ID, usernameID); !errors.Is(err, authn.ErrUsernameImmutable) {
			t.Fatalf("removing the only login name gave %v, want ErrUsernameImmutable", err)
		}
		if errors.Is(err, authn.ErrUnavailable) {
			t.Errorf("the immutable-username refusal also reads as ErrUnavailable, so a caller cannot tell them apart")
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
			Username:         "onemail",
			Email:            "only@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin},
			PasswordHash:     cheapHash(t, "correct horse battery"),
			Via:              "admin",
		})
		if err != nil {
			t.Fatalf("creating an account with a username and an email address: %v", err)
		}
		if err = h.handle.Exec("DELETE FROM "+tableIdentifiers+" WHERE account_id = ? AND kind = ?",
			emailOnly.ID.String(), string(authn.IdentifierUsername)).Error; err != nil {
			t.Fatalf("clearing the username: %v", err)
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

// A username is never removable, while the chosen email may go as long as the username keeps a login name.
func TestRemovingTheUsernameIsAlwaysRefusedAndAnEmailGoesWhileItRemains(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("both", "correct horse battery")

		usernameID := identifierOfKind(t, h, id, authn.IdentifierUsername).ID
		if err := h.p.RemoveIdentifier(ctx, id, usernameID); !errors.Is(err, authn.ErrUsernameImmutable) {
			t.Fatalf("removing the username beside another login name gave %v, want ErrUsernameImmutable", err)
		}
		if _, err := h.p.Lookup(ctx, authn.IdentifierUsername, "both"); err != nil {
			t.Fatalf("the refused removal left the username unfound: %v", err)
		}

		emailID := identifierOfKind(t, h, id, authn.IdentifierEmail).ID
		if err := h.p.RemoveIdentifier(ctx, id, emailID); err != nil {
			t.Fatalf("removing the email address while the username remains gave %v, want nil", err)
		}
		if _, err := h.p.Lookup(ctx, authn.IdentifierEmail, "both@example.com"); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("the removed email address is still found: %v", err)
		}
	})
}

// A passwordless account's username is its only identifier, so removing it is ErrUsernameImmutable, rolls back and publishes no event.
func TestAnAccountWithoutAPasswordCannotLoseItsUsername(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id, err := h.p.Create(ctx, authn.CreateAccount{Username: "nopass", Via: "admin"})
		if err != nil {
			t.Fatalf("creating an account without a password: %v", err)
		}
		usernameID := identifierOfKind(t, h, id.ID, authn.IdentifierUsername).ID
		before := len(h.bus.of(authn.EventIdentifierChanged))
		if err = h.p.RemoveIdentifier(ctx, id.ID, usernameID); !errors.Is(err, authn.ErrUsernameImmutable) {
			t.Fatalf("removing the username of an account without a password gave %v, want ErrUsernameImmutable", err)
		}
		if held, listErr := h.p.Identifiers(ctx, id.ID); listErr != nil || len(held) != 1 {
			t.Errorf("the refused removal left %v, %v, want the identifier kept", held, listErr)
		}
		if got := len(h.bus.of(authn.EventIdentifierChanged)); got != before {
			t.Errorf("the refused removal published %d event(s)", got-before)
		}
	})
}

// A passwordless account keeps its username, and a chosen phone that is its last way in cannot be removed either.
func TestAPasswordlessAccountKeepsItsUsernameAndItsLastWayIn(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()

		withPhone, err := h.p.Create(ctx, authn.CreateAccount{Username: "phoneway", Via: "admin"})
		if err != nil {
			t.Fatalf("creating an account without a password: %v", err)
		}
		if err = h.p.AddIdentifier(ctx, withPhone.ID, authn.IdentifierPhone, "+8613800000011",
			authn.Verification{Via: authn.VerifiedViaSMSCode}); err != nil {
			t.Fatalf("adding a phone number: %v", err)
		}
		phoneID := identifierOfKind(t, h, withPhone.ID, authn.IdentifierPhone).ID
		if err = h.p.SetIdentifierFlags(ctx, withPhone.ID, phoneID,
			authn.IdentifierFlags{Contact: true, SignIn: true}); err != nil {
			t.Fatalf("choosing the phone number as a sign-in way: %v", err)
		}
		if err = h.p.RemoveIdentifier(ctx, withPhone.ID,
			identifierOfKind(t, h, withPhone.ID, authn.IdentifierUsername).ID); !errors.Is(err, authn.ErrUsernameImmutable) {
			t.Fatalf("removing the username gave %v, want ErrUsernameImmutable", err)
		}
		if err = h.p.RemoveIdentifier(ctx, withPhone.ID,
			identifierOfKind(t, h, withPhone.ID, authn.IdentifierPhone).ID); !errors.Is(err, authn.ErrLastLoginMethod) {
			t.Fatalf("removing the last phone number gave %v, want ErrLastLoginMethod", err)
		}

		withExternal, err := h.p.Create(ctx, authn.CreateAccount{Username: "externalway", Via: "admin"})
		if err != nil {
			t.Fatalf("creating an account without a password: %v", err)
		}
		if err = h.p.LinkExternal(ctx, withExternal.ID,
			authn.ExternalLink{Provider: "google", Subject: "subject-" + withExternal.ID.String()}); err != nil {
			t.Fatalf("linking an external identity: %v", err)
		}
		if err = h.p.RemoveIdentifier(ctx, withExternal.ID,
			identifierOfKind(t, h, withExternal.ID, authn.IdentifierUsername).ID); !errors.Is(err, authn.ErrUsernameImmutable) {
			t.Fatalf("removing the username beside an external identity gave %v, want ErrUsernameImmutable", err)
		}
	})
}

// A phone counts as a way in only once proved and chosen; an unproved one, even proved in place, leaves no login way until chosen.
func TestAnUnverifiedPhoneDoesNotKeepTheAccountReachable(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id, err := h.p.Create(ctx, authn.CreateAccount{Username: "unproven-phone", Via: "admin"})
		if err != nil {
			t.Fatalf("creating an account without a password: %v", err)
		}
		if err = h.p.AddIdentifier(ctx, id.ID, authn.IdentifierPhone, "+8613800000021",
			authn.Verification{Via: authn.VerifiedViaAdmin}); err != nil {
			t.Fatalf("adding an unverified phone number: %v", err)
		}
		if err = h.p.RemoveIdentifier(ctx, id.ID,
			identifierOfKind(t, h, id.ID, authn.IdentifierUsername).ID); !errors.Is(err, authn.ErrUsernameImmutable) {
			t.Fatalf("removing the username beside an unverified phone gave %v, want ErrUsernameImmutable", err)
		}
		if way, wayErr := h.p.HasLoginWayOtherThanPasskey(ctx, id.ID); wayErr != nil || way {
			t.Errorf("an unverified phone reports a way in: %v, %v", way, wayErr)
		}

		if err = h.p.AddIdentifier(ctx, id.ID, authn.IdentifierPhone, "+8613800000021",
			authn.Verification{Via: authn.VerifiedViaSMSCode}); err != nil {
			t.Fatalf("proving the phone number: %v", err)
		}
		if way, wayErr := h.p.HasLoginWayOtherThanPasskey(ctx, id.ID); wayErr != nil || way {
			t.Errorf("a proved but unchosen phone reports a way in: %v, %v", way, wayErr)
		}

		phoneID := identifierOfKind(t, h, id.ID, authn.IdentifierPhone).ID
		if err = h.p.SetIdentifierFlags(ctx, id.ID, phoneID, authn.IdentifierFlags{Contact: true, SignIn: true}); err != nil {
			t.Fatalf("choosing the proved phone number: %v", err)
		}
		if way, wayErr := h.p.HasLoginWayOtherThanPasskey(ctx, id.ID); wayErr != nil || !way {
			t.Errorf("a chosen phone does not report a way in: %v, %v", way, wayErr)
		}
	})
}

// HasLoginWayOtherThanPasskey counts a password, a proved chosen phone and an external identity, and none of them does not.
func TestHasLoginWayOtherThanPasskeyCountsTheThreeWays(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()

		none, err := h.p.Create(ctx, authn.CreateAccount{Username: "noway", Via: "admin"})
		if err != nil {
			t.Fatalf("creating an account without a password: %v", err)
		}
		if way, wayErr := h.p.HasLoginWayOtherThanPasskey(ctx, none.ID); wayErr != nil || way {
			t.Errorf("an account with only a login name reports %v, %v, want false, nil", way, wayErr)
		}

		withPassword := h.user("has-a-password", "correct horse battery")
		if way, wayErr := h.p.HasLoginWayOtherThanPasskey(ctx, withPassword); wayErr != nil || !way {
			t.Errorf("an account with a password reports %v, %v, want true, nil", way, wayErr)
		}

		withPhone, err := h.p.Create(ctx, authn.CreateAccount{Username: "has-a-phone", Via: "admin"})
		if err != nil {
			t.Fatalf("creating an account without a password: %v", err)
		}
		if err = h.p.AddIdentifier(ctx, withPhone.ID, authn.IdentifierPhone, "+8613800000012",
			authn.Verification{Via: authn.VerifiedViaSMSCode}); err != nil {
			t.Fatalf("adding a phone number: %v", err)
		}
		if way, wayErr := h.p.HasLoginWayOtherThanPasskey(ctx, withPhone.ID); wayErr != nil || way {
			t.Errorf("an account with a proved but unchosen phone number reports %v, %v, want false, nil", way, wayErr)
		}
		phoneID := identifierOfKind(t, h, withPhone.ID, authn.IdentifierPhone).ID
		if err = h.p.SetIdentifierFlags(ctx, withPhone.ID, phoneID,
			authn.IdentifierFlags{Contact: true, SignIn: true}); err != nil {
			t.Fatalf("choosing the phone number as a sign-in way: %v", err)
		}
		if way, wayErr := h.p.HasLoginWayOtherThanPasskey(ctx, withPhone.ID); wayErr != nil || !way {
			t.Errorf("an account with a chosen phone number reports %v, %v, want true, nil", way, wayErr)
		}

		unproven, err := h.p.Create(ctx, authn.CreateAccount{Username: "has-an-unproven-phone", Via: "admin"})
		if err != nil {
			t.Fatalf("creating an account without a password: %v", err)
		}
		if err = h.p.AddIdentifier(ctx, unproven.ID, authn.IdentifierPhone, "+8613800000013",
			authn.Verification{Via: authn.VerifiedViaAdmin}); err != nil {
			t.Fatalf("adding an unverified phone number: %v", err)
		}
		if way, wayErr := h.p.HasLoginWayOtherThanPasskey(ctx, unproven.ID); wayErr != nil || way {
			t.Errorf("an account with only an unverified phone number reports %v, %v, want false, nil", way, wayErr)
		}
		if err = h.p.AddIdentifier(ctx, unproven.ID, authn.IdentifierPhone, "+8613800000013",
			authn.Verification{Via: authn.VerifiedViaSMSCode}); err != nil {
			t.Fatalf("proving the phone number: %v", err)
		}
		if way, wayErr := h.p.HasLoginWayOtherThanPasskey(ctx, unproven.ID); wayErr != nil || way {
			t.Errorf("an account with a proved but unchosen phone number reports %v, %v, want false, nil", way, wayErr)
		}
		provedID := identifierOfKind(t, h, unproven.ID, authn.IdentifierPhone).ID
		if err = h.p.SetIdentifierFlags(ctx, unproven.ID, provedID,
			authn.IdentifierFlags{Contact: true, SignIn: true}); err != nil {
			t.Fatalf("choosing the proved phone number: %v", err)
		}
		if way, wayErr := h.p.HasLoginWayOtherThanPasskey(ctx, unproven.ID); wayErr != nil || !way {
			t.Errorf("an account with a proved phone number reports %v, %v, want true, nil", way, wayErr)
		}

		withExternal, err := h.p.Create(ctx, authn.CreateAccount{Username: "has-an-external", Via: "admin"})
		if err != nil {
			t.Fatalf("creating an account without a password: %v", err)
		}
		if err = h.p.LinkExternal(ctx, withExternal.ID,
			authn.ExternalLink{Provider: "github", Subject: "subject-" + withExternal.ID.String()}); err != nil {
			t.Fatalf("linking an external identity: %v", err)
		}
		if way, err := h.p.HasLoginWayOtherThanPasskey(ctx, withExternal.ID); err != nil || !way {
			t.Errorf("an account with an external identity reports %v, %v, want true, nil", way, err)
		}
	})
}

// A phone number is removed even when the account has a password credential and nothing else serves as a login name.
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
