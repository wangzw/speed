package account

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestAVerifiedRegistrationAddressBecomesALoginWay holds the password-signup
// path: the reserved address is no login name before its holder proves it, and
// the proof, on the account's only address, makes it one in the same transaction.
func TestAVerifiedRegistrationAddressBecomesALoginWay(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		const password = "correct horse battery"
		h.openSelfRegistration()
		account := h.mustRegister("alice", "alice@example.com", password)

		if _, _, err := h.p.Verify(ctx, "alice@example.com", password); !errors.Is(err, authn.ErrInvalidCredentials) {
			t.Errorf("a password login under the unproved address gave %v, want ErrInvalidCredentials", err)
		}

		if err := h.p.AddIdentifier(ctx, account.ID, authn.IdentifierEmail, "alice@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving the address: %v", err)
		}
		email := identifierByValue(t, h, account.ID, authn.IdentifierEmail, "alice@example.com")
		if !email.SignIn {
			t.Errorf("the sole proved address reads sign_in=false, want true")
		}
		if got, _, err := h.p.Verify(ctx, "alice@example.com", password); err != nil || got != account.ID {
			t.Errorf("a password login under the proved address gave %q, %v, want the account", got, err)
		}
	})
}

// TestOnlyASoleEmailIsChosenAsALoginWayAtItsFirstProof holds both sides of the
// rule: an account's only address is chosen when it is proved, while an address
// among several leaves the choice to its holder.
func TestOnlyASoleEmailIsChosenAsALoginWayAtItsFirstProof(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		emailCode := authn.Verification{Via: authn.VerifiedViaEmailCode}

		alone, err := h.p.Create(ctx, authn.CreateAccount{Username: "alone", Via: "admin"})
		if err != nil {
			t.Fatalf("creating the account with no address: %v", err)
		}
		if err = h.p.AddIdentifier(ctx, alone.ID, authn.IdentifierEmail, "alone@example.com", emailCode); err != nil {
			t.Fatalf("proving the only address: %v", err)
		}
		if got := identifierByValue(t, h, alone.ID, authn.IdentifierEmail, "alone@example.com"); !got.SignIn {
			t.Errorf("the account's only proved address reads sign_in=false, want true")
		}

		pair, err := h.p.Create(ctx, authn.CreateAccount{
			Username: "pair", Via: "admin",
			Email: "pair@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin},
		})
		if err != nil {
			t.Fatalf("creating the account with a first asserted address: %v", err)
		}
		if err = h.p.AddIdentifier(ctx, pair.ID, authn.IdentifierEmail, "second@example.com",
			authn.Verification{Via: authn.VerifiedViaAdmin}); err != nil {
			t.Fatalf("adding a second asserted address: %v", err)
		}
		if err = h.p.AddIdentifier(ctx, pair.ID, authn.IdentifierEmail, "pair@example.com", emailCode); err != nil {
			t.Fatalf("proving one of two addresses: %v", err)
		}
		for _, value := range []string{"pair@example.com", "second@example.com"} {
			if got := identifierByValue(t, h, pair.ID, authn.IdentifierEmail, value); got.SignIn {
				t.Errorf("%s reads sign_in=true among two addresses, want false", value)
			}
		}
	})
}

// TestAnImportKeepsTheSignInAxisOfItsSourceRecord holds the import path outside
// the rule: a source record that did not choose its proved address keeps it
// unchosen, while the same address on an account that is not an import is chosen.
func TestAnImportKeepsTheSignInAxisOfItsSourceRecord(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		imported, err := h.p.Create(ctx, authn.CreateAccount{
			ID: "legacy_unchosen", Via: "import",
			Email: "legacy@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode},
			EmailFlags: authn.IdentifierFlags{Contact: true},
		})
		if err != nil {
			t.Fatalf("importing the account: %v", err)
		}
		if got := identifierByValue(t, h, imported.ID, authn.IdentifierEmail, "legacy@example.com"); got.SignIn {
			t.Errorf("the imported address reads sign_in=true, want the source record's false")
		}

		fresh, err := h.p.Create(ctx, authn.CreateAccount{
			Username: "fresh", Via: "admin",
			Email: "fresh@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode},
		})
		if err != nil {
			t.Fatalf("creating the account: %v", err)
		}
		if got := identifierByValue(t, h, fresh.ID, authn.IdentifierEmail, "fresh@example.com"); !got.SignIn {
			t.Errorf("the created account's only proved address reads sign_in=false, want true")
		}
	})
}

// TestCreateChoosesASoleProvedEmailAsALoginWay holds the channels that prove an
// address at creation: each makes the account's only address a login way, while
// the unproved channels leave both axes for the holder.
func TestCreateChoosesASoleProvedEmailAsALoginWay(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		for _, c := range []struct {
			username string
			via      string
			signIn   bool
		}{
			{"by-idp", authn.VerifiedViaIDPPrefix + "google", true},
			{"by-code", authn.VerifiedViaEmailCode, true},
			{"by-invite", authn.VerifiedViaInvite, true},
			{"by-admin", authn.VerifiedViaAdmin, false},
			{"by-self", authn.VerifiedViaSelf, false},
			{"by-import-unverified", authn.VerifiedViaImportUnverified, false},
		} {
			email := c.username + "@example.com"
			created, err := h.p.Create(ctx, authn.CreateAccount{
				Username: c.username, Via: "admin",
				Email: email, EmailVerifiedVia: authn.Verification{Via: c.via},
			})
			if err != nil {
				t.Fatalf("%s: creating the account: %v", c.via, err)
			}
			if got := identifierByValue(t, h, created.ID, authn.IdentifierEmail, email); got.SignIn != c.signIn {
				t.Errorf("%s: the only address reads sign_in=%v, want %v", c.via, got.SignIn, c.signIn)
			}
		}
	})
}
