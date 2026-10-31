package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// identifierByValue reads one identifier of a kind and value the account holds.
func identifierByValue(t *testing.T, h *harness, id authn.AccountID, kind authn.IdentifierKind,
	value string,
) authn.Identifier {
	t.Helper()
	list, err := h.p.Identifiers(context.Background(), id)
	if err != nil {
		t.Fatalf("listing identifiers: %v", err)
	}
	for _, item := range list {
		if item.Kind == kind && item.Value == value {
			return item
		}
	}
	t.Fatalf("account %s holds no %s %q", id, kind, value)
	return authn.Identifier{}
}

// defaultContactValue is the value of the one identifier carrying the default
// contact, and how many carry it. No identifier is both an empty value and one.
func defaultContactValue(t *testing.T, h *harness, id authn.AccountID) (string, int) {
	t.Helper()
	list, err := h.p.Identifiers(context.Background(), id)
	if err != nil {
		t.Fatalf("listing identifiers: %v", err)
	}
	value, count := "", 0
	for _, item := range list {
		if item.DefaultContact {
			value, count = item.Value, count+1
		}
	}
	return value, count
}

// TestAddIdentifierWritesTheAxesAndEstablishesTheFirstDefaultContact holds how a
// declared identifier enters the account: an address is a contact from the
// moment it is registered, whether it is proved or only asserted, and a username
// never is. The account's only email address, proved here, becomes a login way
// and the default contact in the same transaction while a later one is no login
// way and leaves the default alone.
func TestAddIdentifierWritesTheAxesAndEstablishesTheFirstDefaultContact(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		created, err := h.p.Create(ctx, authn.CreateAccount{Username: "axes", Via: "admin"})
		if err != nil {
			t.Fatalf("creating the account: %v", err)
		}
		emailCode := authn.Verification{Via: authn.VerifiedViaEmailCode}
		if err = h.p.AddIdentifier(ctx, created.ID, authn.IdentifierEmail, "first@example.com", emailCode); err != nil {
			t.Fatalf("adding the first email address: %v", err)
		}
		first := identifierByValue(t, h, created.ID, authn.IdentifierEmail, "first@example.com")
		if !first.SignIn || !first.Contact || !first.DefaultContact {
			t.Errorf("the first proved email reads sign_in=%v contact=%v default_contact=%v, want all true",
				first.SignIn, first.Contact, first.DefaultContact)
		}

		h.clock.advance(time.Hour)
		if err = h.p.AddIdentifier(ctx, created.ID, authn.IdentifierEmail, "second@example.com", emailCode); err != nil {
			t.Fatalf("adding the second email address: %v", err)
		}
		second := identifierByValue(t, h, created.ID, authn.IdentifierEmail, "second@example.com")
		if !second.Contact {
			t.Error("the second proved email is not a contact")
		}
		if second.DefaultContact {
			t.Error("the second proved email became the default contact")
		}
		if value, count := defaultContactValue(t, h, created.ID); value != "first@example.com" || count != 1 {
			t.Errorf("the default contact is %q (%d rows), want first@example.com on one row", value, count)
		}

		// An address only asserted is a contact but neither a sign-in way nor the
		// default, and proving it later keeps the default where it was.
		if err = h.p.AddIdentifier(ctx, created.ID, authn.IdentifierEmail, "asserted@example.com",
			authn.Verification{Via: authn.VerifiedViaAdmin}); err != nil {
			t.Fatalf("adding an asserted email address: %v", err)
		}
		asserted := identifierByValue(t, h, created.ID, authn.IdentifierEmail, "asserted@example.com")
		if asserted.SignIn || !asserted.Contact || asserted.DefaultContact {
			t.Errorf("the asserted email reads sign_in=%v contact=%v default_contact=%v, want false true false",
				asserted.SignIn, asserted.Contact, asserted.DefaultContact)
		}
		if err = h.p.AddIdentifier(ctx, created.ID, authn.IdentifierEmail, "asserted@example.com", emailCode); err != nil {
			t.Fatalf("proving the asserted address: %v", err)
		}
		if value, _ := defaultContactValue(t, h, created.ID); value != "first@example.com" {
			t.Errorf("proving a later address moved the default contact to %q", value)
		}

		// A phone number is a contact and not a sign-in way, and a username is a
		// sign-in way and never a contact.
		if err = h.p.AddIdentifier(ctx, created.ID, authn.IdentifierPhone, "+8613800000031",
			authn.Verification{Via: authn.VerifiedViaSMSCode}); err != nil {
			t.Fatalf("adding a phone number: %v", err)
		}
		phone := identifierOfKind(t, h, created.ID, authn.IdentifierPhone)
		if phone.SignIn || !phone.Contact || phone.DefaultContact {
			t.Errorf("the proved phone reads sign_in=%v contact=%v default_contact=%v, want false true false",
				phone.SignIn, phone.Contact, phone.DefaultContact)
		}
		username := identifierOfKind(t, h, created.ID, authn.IdentifierUsername)
		if !username.SignIn || username.Contact || username.DefaultContact {
			t.Errorf("the username reads sign_in=%v contact=%v default_contact=%v, want true false false",
				username.SignIn, username.Contact, username.DefaultContact)
		}
	})
}

// TestProvingAnAssertedAddressMakesItTheDefaultContact holds the other way the
// first proved address appears: the account was opened with an asserted address,
// which is not the default, and the proof written over that row establishes the
// choice without adding a second row.
func TestProvingAnAssertedAddressMakesItTheDefaultContact(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		created, err := h.p.Create(ctx, authn.CreateAccount{
			Username: "asserted", Via: "admin",
			Email: "asserted@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin},
		})
		if err != nil {
			t.Fatalf("creating the account: %v", err)
		}
		if value, count := defaultContactValue(t, h, created.ID); value != "" || count != 0 {
			t.Fatalf("an asserted address is the default contact %q on %d row(s)", value, count)
		}
		if err = h.p.AddIdentifier(ctx, created.ID, authn.IdentifierEmail, "asserted@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving the address: %v", err)
		}
		list, err := h.p.Identifiers(ctx, created.ID)
		if err != nil {
			t.Fatalf("listing identifiers: %v", err)
		}
		var emails int
		for _, item := range list {
			if item.Kind != authn.IdentifierEmail {
				continue
			}
			emails++
			if !item.DefaultContact {
				t.Errorf("the proved address reads default_contact=false")
			}
		}
		if emails != 1 {
			t.Errorf("the account holds %d email address(es), want the proved one alone", emails)
		}
	})
}

// TestSetIdentifierFlagsKeepsTheAxesIndependent holds the write surface of the two
// axes: contact may be set on a row whose ownership is not proved, sign-in may
// not, a username has no axes, a row of another account is not this account's,
// and choosing a proved row leaves its contact alone.
func TestSetIdentifierFlagsKeepsTheAxesIndependent(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("flags", "correct horse battery")
		emailID := identifierOfKind(t, h, id, authn.IdentifierEmail).ID

		if err := h.p.SetIdentifierFlags(ctx, id, emailID,
			authn.IdentifierFlags{Contact: true}); err != nil {
			t.Fatalf("making an unproved address a contact gave %v, want nil", err)
		}
		row := identifierOfKind(t, h, id, authn.IdentifierEmail)
		if !row.Contact || row.SignIn {
			t.Errorf("the unproved address reads contact=%v sign_in=%v, want true false", row.Contact, row.SignIn)
		}

		if err := h.p.SetIdentifierFlags(ctx, id, emailID,
			authn.IdentifierFlags{Contact: true, SignIn: true}); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("making an unproved address a sign-in way gave %v, want ErrInvalidArgument", err)
		}
		if row = identifierOfKind(t, h, id, authn.IdentifierEmail); row.SignIn {
			t.Error("the refused write made the unproved address a sign-in way")
		}

		usernameID := identifierOfKind(t, h, id, authn.IdentifierUsername).ID
		if err := h.p.SetIdentifierFlags(ctx, id, usernameID,
			authn.IdentifierFlags{SignIn: true}); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("writing the axes of a username gave %v, want ErrInvalidArgument", err)
		}

		other := h.user("another", "correct horse battery")
		if err := h.p.SetIdentifierFlags(ctx, id,
			identifierOfKind(t, h, other, authn.IdentifierEmail).ID,
			authn.IdentifierFlags{Contact: true}); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("writing another account's identifier gave %v, want ErrInvalidArgument", err)
		}

		// Proving the row makes sign-in possible, and does not clear the contact
		// axis it already carries.
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierEmail, "flags@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving the address: %v", err)
		}
		if err := h.p.SetIdentifierFlags(ctx, id, emailID,
			authn.IdentifierFlags{Contact: true, SignIn: true}); err != nil {
			t.Fatalf("choosing the proved address as a sign-in way gave %v, want nil", err)
		}
		row = identifierOfKind(t, h, id, authn.IdentifierEmail)
		if !row.SignIn || !row.Contact {
			t.Errorf("the proved address reads sign_in=%v contact=%v, want both true", row.SignIn, row.Contact)
		}
	})
}

// TestSetIdentifierFlagsRefusesTakingAwayTheContactAxis holds that the contact
// axis is not a choice: a registered address is always a contact, so asking for
// false is refused and changes nothing, whether the row is the default contact or
// not. Asking for true is accepted and leaves the row as it is.
func TestSetIdentifierFlagsRefusesTakingAwayTheContactAxis(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("contactfixed", "correct horse battery")
		emailCode := authn.Verification{Via: authn.VerifiedViaEmailCode}
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierEmail, "default@example.com", emailCode); err != nil {
			t.Fatalf("adding the default address: %v", err)
		}
		h.clock.advance(time.Hour)
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierEmail, "other@example.com", emailCode); err != nil {
			t.Fatalf("adding another address: %v", err)
		}

		for _, value := range []string{"default@example.com", "other@example.com"} {
			row := identifierByValue(t, h, id, authn.IdentifierEmail, value)
			if err := h.p.SetIdentifierFlags(ctx, id, row.ID,
				authn.IdentifierFlags{SignIn: row.SignIn}); !errors.Is(err, authn.ErrInvalidArgument) {
				t.Errorf("taking the contact away from %q gave %v, want ErrInvalidArgument", value, err)
			}
			got := identifierByValue(t, h, id, authn.IdentifierEmail, value)
			if !got.Contact || got.SignIn != row.SignIn || got.DefaultContact != row.DefaultContact {
				t.Errorf("%q after the refused write reads sign_in=%v contact=%v default_contact=%v, want %v true %v",
					value, got.SignIn, got.Contact, got.DefaultContact, row.SignIn, row.DefaultContact)
			}

			if err := h.p.SetIdentifierFlags(ctx, id, row.ID,
				authn.IdentifierFlags{Contact: true, SignIn: row.SignIn}); err != nil {
				t.Errorf("keeping the contact on %q gave %v, want nil", value, err)
			}
		}
		if value, _ := defaultContactValue(t, h, id); value != "default@example.com" {
			t.Errorf("the default contact is %q, want default@example.com", value)
		}
	})
}

// TestReprovingAHeldAddressRestoresItsContactAxis holds the write made when an
// address the account already holds is proved again: the row is an address the
// account owns, so it carries the contact axis even when it was written before
// the invariant and reads false.
func TestReprovingAHeldAddressRestoresItsContactAxis(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("reprove", "correct horse battery")
		emailID := identifierOfKind(t, h, id, authn.IdentifierEmail).ID
		// A row written before the invariant is a contact that reads false.
		if err := h.handle.Exec("UPDATE "+tableIdentifiers+" SET contact = ? WHERE id = ?", false, emailID).Error; err != nil {
			t.Fatalf("clearing the contact axis: %v", err)
		}
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierEmail, "reprove@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving the held address again: %v", err)
		}
		if row := identifierOfKind(t, h, id, authn.IdentifierEmail); !row.Contact {
			t.Error("the re-proved address reads contact=false, want true")
		}
	})
}

// TestSetIdentifierFlagsRefusesTheLastLoginNameAndTheLastWay holds the two
// refusals of taking a row out of sign-in, in the order the removal uses, and
// that neither takes effect: a password account that would lose its last login
// name is ErrIdentifierRequired, and a passwordless account that would lose every
// way in is ErrLastLoginMethod.
func TestSetIdentifierFlagsRefusesTheLastLoginNameAndTheLastWay(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()

		id := h.user("lastname", "correct horse battery")
		emailID := identifierOfKind(t, h, id, authn.IdentifierEmail).ID
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierEmail, "lastname@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving the address: %v", err)
		}
		if err := h.p.SetIdentifierFlags(ctx, id, emailID,
			authn.IdentifierFlags{Contact: true, SignIn: true}); err != nil {
			t.Fatalf("choosing the address as a sign-in way: %v", err)
		}
		if err := h.p.RemoveIdentifier(ctx, id,
			identifierOfKind(t, h, id, authn.IdentifierUsername).ID); err != nil {
			t.Fatalf("removing the username beside the chosen address gave %v, want nil", err)
		}
		if err := h.p.SetIdentifierFlags(ctx, id, emailID, authn.IdentifierFlags{Contact: true}); !errors.Is(err, authn.ErrIdentifierRequired) {
			t.Errorf("taking away the last login name gave %v, want ErrIdentifierRequired", err)
		}
		if row := identifierOfKind(t, h, id, authn.IdentifierEmail); !row.SignIn || !row.DefaultContact {
			t.Errorf("the refused write left sign_in=%v default_contact=%v, want both true", row.SignIn, row.DefaultContact)
		}

		passwordless, err := h.p.Create(ctx, authn.CreateAccount{Username: "nowayleft", Via: "admin"})
		if err != nil {
			t.Fatalf("creating an account without a password: %v", err)
		}
		if err = h.p.AddIdentifier(ctx, passwordless.ID, authn.IdentifierPhone, "+8613800000041",
			authn.Verification{Via: authn.VerifiedViaSMSCode}); err != nil {
			t.Fatalf("adding a phone number: %v", err)
		}
		phoneID := identifierOfKind(t, h, passwordless.ID, authn.IdentifierPhone).ID
		if err = h.p.SetIdentifierFlags(ctx, passwordless.ID, phoneID,
			authn.IdentifierFlags{Contact: true, SignIn: true}); err != nil {
			t.Fatalf("choosing the phone number: %v", err)
		}
		if err = h.p.SetIdentifierFlags(ctx, passwordless.ID, phoneID,
			authn.IdentifierFlags{Contact: true}); !errors.Is(err, authn.ErrLastLoginMethod) {
			t.Errorf("taking away the last way in gave %v, want ErrLastLoginMethod", err)
		}
		if phone := identifierOfKind(t, h, passwordless.ID, authn.IdentifierPhone); !phone.SignIn {
			t.Error("the refused write took the phone number out of sign-in")
		}
	})
}

// TestSetDefaultContactSwitchesTheChoiceAndRefusesWhatCannotBeOne holds the
// default contact's write: only a proved email address of the account may be
// chosen, and choosing one clears the previous choice in the same transaction.
func TestSetDefaultContactSwitchesTheChoiceAndRefusesWhatCannotBeOne(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("switch", "correct horse battery")
		emailCode := authn.Verification{Via: authn.VerifiedViaEmailCode}
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierEmail, "one@example.com", emailCode); err != nil {
			t.Fatalf("adding the first address: %v", err)
		}
		h.clock.advance(time.Hour)
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierEmail, "two@example.com", emailCode); err != nil {
			t.Fatalf("adding the second address: %v", err)
		}
		if value, _ := defaultContactValue(t, h, id); value != "one@example.com" {
			t.Fatalf("the default contact is %q, want one@example.com", value)
		}

		twoID := identifierByValue(t, h, id, authn.IdentifierEmail, "two@example.com").ID
		if err := h.p.SetDefaultContact(ctx, id, twoID); err != nil {
			t.Fatalf("choosing the second address gave %v, want nil", err)
		}
		if got := identifierByValue(t, h, id, authn.IdentifierEmail, "one@example.com"); got.DefaultContact {
			t.Error("the previous default contact kept the choice")
		}
		if got := identifierByValue(t, h, id, authn.IdentifierEmail, "two@example.com"); !got.DefaultContact {
			t.Error("the chosen address is not the default contact")
		}
		if value, count := defaultContactValue(t, h, id); value != "two@example.com" || count != 1 {
			t.Errorf("the default contact is %q on %d rows, want two@example.com on one", value, count)
		}

		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierEmail, "asserted@example.com",
			authn.Verification{Via: authn.VerifiedViaAdmin}); err != nil {
			t.Fatalf("adding an asserted address: %v", err)
		}
		assertedID := identifierByValue(t, h, id, authn.IdentifierEmail, "asserted@example.com").ID
		if err := h.p.SetDefaultContact(ctx, id, assertedID); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("choosing an unproved address gave %v, want ErrInvalidArgument", err)
		}

		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierPhone, "+8613800000051",
			authn.Verification{Via: authn.VerifiedViaSMSCode}); err != nil {
			t.Fatalf("adding a phone number: %v", err)
		}
		phoneID := identifierOfKind(t, h, id, authn.IdentifierPhone).ID
		if err := h.p.SetDefaultContact(ctx, id, phoneID); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("choosing a phone number gave %v, want ErrInvalidArgument", err)
		}

		other := h.user("elsewhere", "correct horse battery")
		otherID := identifierOfKind(t, h, other, authn.IdentifierEmail).ID
		if err := h.p.SetDefaultContact(ctx, id, otherID); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("choosing another account's address gave %v, want ErrInvalidArgument", err)
		}
		if value, _ := defaultContactValue(t, h, id); value != "two@example.com" {
			t.Errorf("a refused write moved the default contact to %q", value)
		}
	})
}

// TestRemovingTheDefaultContactHandsItToTheEarliestRemaining holds that removing
// the default address transfers the choice to the earliest proved address left,
// that removing another address leaves the choice where it was, and that the last
// proved address takes the choice away with it.
func TestRemovingTheDefaultContactHandsItToTheEarliestRemaining(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("transfer", "correct horse battery")
		emailCode := authn.Verification{Via: authn.VerifiedViaEmailCode}
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierEmail, "early@example.com", emailCode); err != nil {
			t.Fatalf("adding the early address: %v", err)
		}
		h.clock.advance(time.Hour)
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierEmail, "late@example.com", emailCode); err != nil {
			t.Fatalf("adding the late address: %v", err)
		}
		h.clock.advance(time.Hour)
		if err := h.p.AddIdentifier(ctx, id, authn.IdentifierEmail, "latest@example.com", emailCode); err != nil {
			t.Fatalf("adding the latest address: %v", err)
		}

		if err := h.p.RemoveIdentifier(ctx, id,
			identifierByValue(t, h, id, authn.IdentifierEmail, "late@example.com").ID); err != nil {
			t.Fatalf("removing a non-default address gave %v, want nil", err)
		}
		if value, _ := defaultContactValue(t, h, id); value != "early@example.com" {
			t.Errorf("removing another address moved the default contact to %q", value)
		}

		if err := h.p.RemoveIdentifier(ctx, id,
			identifierByValue(t, h, id, authn.IdentifierEmail, "early@example.com").ID); err != nil {
			t.Fatalf("removing the default address gave %v, want nil", err)
		}
		if value, count := defaultContactValue(t, h, id); value != "latest@example.com" || count != 1 {
			t.Errorf("the default contact is %q on %d rows, want latest@example.com on one", value, count)
		}

		if err := h.p.RemoveIdentifier(ctx, id,
			identifierByValue(t, h, id, authn.IdentifierEmail, "latest@example.com").ID); err != nil {
			t.Fatalf("removing the last proved address gave %v, want nil", err)
		}
		if value, count := defaultContactValue(t, h, id); value != "" || count != 0 {
			t.Errorf("the default contact is %q on %d rows, want none", value, count)
		}
	})
}
