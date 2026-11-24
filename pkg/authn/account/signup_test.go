package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/event"
)

func (h *harness) openSelfRegistration() {
	h.t.Helper()
	h.withSettings().set(passwordSignupPath, signupOpen)
}

func (h *harness) register(username, email, password string) (authn.Account, error) {
	return h.p.Register(context.Background(), authn.RegisterAccount{
		Username: username, Email: email, Password: password,
	})
}

func (h *harness) mustRegister(username, email, password string) authn.Account {
	h.t.Helper()
	account, err := h.register(username, email, password)
	if err != nil {
		h.t.Fatalf("registering %s: %v", username, err)
	}
	return account
}

func (h *harness) signupRow(id authn.AccountID) (state int16, verifiedAt *time.Time) {
	h.t.Helper()
	var rows []struct {
		SignupState      int16
		SignupVerifiedAt *time.Time
	}
	if err := h.handle.Raw("SELECT signup_state, signup_verified_at FROM "+tableAccounts+" WHERE id = ?", id.String()).
		Scan(&rows).Error; err != nil {
		h.t.Fatalf("reading the signup columns: %v", err)
	}
	if len(rows) == 0 {
		h.t.Fatalf("account %s has no row", id)
	}
	return rows[0].SignupState, rows[0].SignupVerifiedAt
}

func (h *harness) identifierIDOf(id authn.AccountID, value string) string {
	h.t.Helper()
	list, err := h.p.Identifiers(context.Background(), id)
	if err != nil {
		h.t.Fatalf("listing identifiers: %v", err)
	}
	for _, item := range list {
		if item.Value == value {
			return item.ID
		}
	}
	h.t.Fatalf("account %s has no identifier %q", id, value)
	return ""
}

// Register runs only while the switch is open, reading it as its authority, and never asks the caller for the state.
func TestRegisterReadsTheSwitchAsItsAuthority(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		in := authn.RegisterAccount{Username: "alice", Email: "alice@example.com", Password: "password123"}

		if _, err := h.p.Register(ctx, in); !errors.Is(err, authn.ErrSignupClosed) {
			t.Fatalf("registration without a settings store gave %v, want ErrSignupClosed", err)
		}
		store := h.withSettings()
		store.set(passwordSignupPath, signupClosed)
		if _, err := h.p.Register(ctx, in); !errors.Is(err, authn.ErrSignupClosed) {
			t.Fatalf("registration with the switch closed gave %v, want ErrSignupClosed", err)
		}
		if n := h.count(tableAccounts, ""); n != 0 {
			t.Errorf("a closed registration wrote %d accounts", n)
		}

		store.set(passwordSignupPath, signupOpen)
		account := h.mustRegister("alice", "alice@example.com", "password123")
		if account.Status != authn.StatusActive || !account.PasswordSet {
			t.Errorf("the registered account is %+v, want an enabled one with a password", account)
		}
		if state, at := h.signupRow(account.ID); state != signupPending || at != nil {
			t.Errorf("the registered account holds signup_state %d and verified_at %v, want 1 and NULL", state, at)
		}
		if n := h.count(tableIdentifiers, "account_id = ? AND kind = ? AND verified_via = ?",
			account.ID.String(), string(authn.IdentifierUsername), authn.VerifiedViaSelf); n != 1 {
			t.Errorf("the username identifier written through self is %d rows, want 1", n)
		}
		if n := h.count(tablePending, "account_id = ? AND kind = ? AND value = ?",
			account.ID.String(), string(authn.IdentifierEmail), "alice@example.com"); n != 1 {
			t.Errorf("the email pending contact is %d rows, want 1", n)
		}
		if n := h.count(tableIdentifiers, "account_id = ? AND kind = ?", account.ID.String(), string(authn.IdentifierEmail)); n != 0 {
			t.Errorf("registration wrote %d email identifier(s), want none: a pending contact is not an identifier", n)
		}
		if n := h.count(tableCredentials, "account_id = ? AND must_change = ?", account.ID.String(), false); n != 1 {
			t.Errorf("the password credential is %d rows with must_change false, want 1", n)
		}

		prefs, err := h.p.Register(ctx, authn.RegisterAccount{
			Username: "bob", Email: "bob@example.com", Password: "password123",
			DisplayName: "Bob", Locale: "zh-CN", TimeZone: "Asia/Shanghai",
		})
		if err != nil {
			t.Fatalf("registering with preferences: %v", err)
		}
		if prefs.DisplayName != "Bob" || prefs.Attributes["i18n.locale"] != "zh-CN" || prefs.Attributes["i18n.tz"] != "Asia/Shanghai" {
			t.Errorf("the registration wrote %+v, want the display name and both preference attributes", prefs)
		}
	})
}

// A registration that fails at its last insert leaves no row, so a half-written account would show.
func TestRegisterIsOneTransaction(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		h.handle.Callback().Create().Before("gorm:create").Register("account_test_fail_pending", func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == tablePending {
				tx.AddError(errors.New("injected failure"))
			}
		})
		account, err := h.register("alice", "alice@example.com", "password123")
		if !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("registration with a failing last insert gave %v, want ErrUnavailable", err)
		}
		if account.ID != "" {
			t.Errorf("a failed registration returned account %s, want no account", account.ID)
		}
		for _, table := range []string{tableAccounts, tableIdentifiers, tablePending, tableCredentials} {
			if n := h.count(table, ""); n != 0 {
				t.Errorf("the failed registration left %d row(s) in %s, want none", n, table)
			}
		}
	})
}

// A taken username is refused before a derivation, counted against the signup-username back-off, and leaves no row.
func TestRegisterRefusesATakenUsernameWithoutHalfRows(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		h.user("taken", "")
		before := h.count(tableAccounts, "")
		beforePending := h.count(tablePending, "")

		if _, err := h.register("taken", "other@example.com", "password123"); !errors.Is(err, authn.ErrAccountExists) {
			t.Fatalf("an occupied username gave %v, want ErrAccountExists", err)
		}
		if n := h.count(tableAccounts, ""); n != before {
			t.Errorf("the refused registration left %d account(s), want the %d that were there", n, before)
		}
		if n := h.count(tablePending, ""); n != beforePending {
			t.Errorf("the refused registration left %d reservation(s), want the %d that were there", n, beforePending)
		}
		if n, ok := h.failuresOf(authn.ScopeSignupUsername, "taken"); !ok || n != 1 {
			t.Errorf("the refused registration counted %d failure(s) (present %v), want 1", n, ok)
		}
		if _, err := h.p.Lookup(context.Background(), authn.IdentifierEmail, "other@example.com"); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("the email of the refused registration is an identifier (%v), want none", err)
		}
	})
}

// A password the policy refuses writes nothing.
func TestRegisterHoldsThePasswordToThePolicy(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		if _, err := h.register("alice", "alice@example.com", "short"); !errors.Is(err, authn.ErrPasswordPolicy) {
			t.Fatalf("a short password gave %v, want ErrPasswordPolicy", err)
		}
		if _, err := h.register("alice", "alice@example.com", "alice"); !errors.Is(err, authn.ErrPasswordPolicy) {
			t.Fatalf("a password equal to the username gave %v, want ErrPasswordPolicy", err)
		}
		for _, table := range []string{tableAccounts, tableIdentifiers, tablePending, tableCredentials} {
			if n := h.count(table, ""); n != 0 {
				t.Errorf("a refused password left %d row(s) in %s", n, table)
			}
		}
	})
}

// A contact is reserved once: the first registration owns it and a second naming it, in any case, is ErrContactTaken.
func TestAContactIsReservedOnceAcrossAccounts(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		first := h.mustRegister("first", "shared@example.com", "password123")
		if _, err := h.register("second", "Shared@Example.com", "password123"); !errors.Is(err, authn.ErrContactTaken) {
			t.Fatalf("a second registration of the address gave %v, want ErrContactTaken", err)
		}
		if n := h.count(tablePending, "kind = ? AND value = ?", string(authn.IdentifierEmail), "shared@example.com"); n != 1 {
			t.Errorf("the shared address is %d reservations, want the first account's one", n)
		}
		if n := h.count(tableAccounts, ""); n != 1 {
			t.Errorf("the losing registration left %d account(s), want the winner's one", n)
		}
		if n := h.count(tableIdentifiers, "account_id = ?", first.ID.String()); n != 1 {
			t.Errorf("the winner holds %d identifier(s), want its username alone", n)
		}
		if n := h.count(tableCredentials, ""); n != 1 {
			t.Errorf("the losing registration left %d credential(s), want the winner's one", n)
		}
	})
}

// An address an account holds as a proved identifier stays reserved, so a registration naming it is ErrContactTaken.
func TestRegisterRefusesAnAddressAnAccountAlreadyHolds(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		h.user("holder", "")
		if _, err := h.register("newcomer", "Holder@Example.com", "password123"); !errors.Is(err, authn.ErrContactTaken) {
			t.Fatalf("a registration of a proved address gave %v, want ErrContactTaken", err)
		}
		if n := h.count(tableAccounts, ""); n != 1 {
			t.Errorf("the refused registration left %d account(s), want the holder's one", n)
		}
	})
}

// Create writes the contact reservation in the same transaction, so an address another account reserved is ErrContactTaken.
func TestCreateRefusesAContactAnotherAccountHolds(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.user("holder", "")
		_, err := h.p.Create(context.Background(), authn.CreateAccount{
			Username: "other", Email: "holder@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin}, Via: "admin",
		})
		if !errors.Is(err, authn.ErrContactTaken) {
			t.Fatalf("creating an account on a held contact gave %v, want ErrContactTaken", err)
		}
		if n := h.count(tableAccounts, ""); n != 1 {
			t.Errorf("the refused create left %d account(s), want the holder's one", n)
		}
	})
}

// An unproved reservation occupies an address like a proved one, so Create naming it is ErrContactTaken.
func TestCreateRefusesAnUnprovedReservation(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		h.mustRegister("holder", "reserved@example.com", "password123")
		_, err := h.p.Create(context.Background(), authn.CreateAccount{
			Username: "other", Email: "reserved@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin}, Via: "admin",
		})
		if !errors.Is(err, authn.ErrContactTaken) {
			t.Fatalf("creating an account on an unproved reservation gave %v, want ErrContactTaken", err)
		}
		if n := h.count(tableAccounts, ""); n != 1 {
			t.Errorf("the refused create left %d account(s), want the registering account's one", n)
		}
	})
}

// Removing an email identifier deletes its reservation, releasing the address.
func TestRemovingAnEmailReleasesItsContact(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.openSelfRegistration()
		account := h.mustRegister("alice", "alice@example.com", "password123")
		if err := h.p.AddIdentifier(ctx, account.ID, authn.IdentifierEmail, "alice@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving the address: %v", err)
		}
		if _, err := h.register("carol", "alice@example.com", "password123"); !errors.Is(err, authn.ErrContactTaken) {
			t.Fatalf("a registration of the held address gave %v, want ErrContactTaken", err)
		}
		if err := h.p.RemoveIdentifier(ctx, account.ID, h.identifierIDOf(account.ID, "alice@example.com")); err != nil {
			t.Fatalf("removing the address: %v", err)
		}
		if n := h.count(tablePending, "account_id = ?", account.ID.String()); n != 0 {
			t.Errorf("the removed address left %d reservation(s), want none", n)
		}
		if _, err := h.register("bob", "alice@example.com", "password123"); err != nil {
			t.Fatalf("a registration of the released address gave %v, want it accepted", err)
		}
	})
}

// A second email written without proof leaves the verified state unchanged, and proving it later brings that row up to email-code without a second row.
func TestASecondEmailIsProvedInPlace(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.openSelfRegistration()
		account := h.mustRegister("alice", "alice@example.com", "password123")
		if err := h.p.AddIdentifier(ctx, account.ID, authn.IdentifierEmail, "alice@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving the first address: %v", err)
		}
		if err := h.p.AddIdentifier(ctx, account.ID, authn.IdentifierEmail, "second@example.com",
			authn.Verification{Via: authn.VerifiedViaAdmin}); err != nil {
			t.Fatalf("adding a second address asserted by an administrator: %v", err)
		}
		status, err := h.p.SignupStateOf(ctx, account.ID)
		if err != nil {
			t.Fatalf("reading the state after the second add: %v", err)
		}
		if status.State != authn.SignupVerified {
			t.Fatalf("the account reads %v with one proved address, want verified", status.State)
		}
		if err = h.p.AddIdentifier(ctx, account.ID, authn.IdentifierEmail, "second@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving the second address: %v", err)
		}
		if n := h.count(tableIdentifiers, "account_id = ? AND value = ?", account.ID.String(), "second@example.com"); n != 1 {
			t.Errorf("the second address is %d identifier(s), want one", n)
		}
		identifiers, err := h.p.Identifiers(ctx, account.ID)
		if err != nil {
			t.Fatalf("reading the identifiers: %v", err)
		}
		for _, identifier := range identifiers {
			if identifier.Value == "second@example.com" && identifier.VerifiedVia != authn.VerifiedViaEmailCode {
				t.Errorf("the second address reads via %q, want %q", identifier.VerifiedVia, authn.VerifiedViaEmailCode)
			}
		}
	})
}

// PendingAccountByContact judges by the unproved reservation, not the stored signup state.
func TestPendingAccountByContactDoesNotReadTheStoredSignupState(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("stored", "")
		if err := h.handle.Exec("UPDATE "+tablePending+" SET verified_at = NULL WHERE account_id = ?",
			id.String()).Error; err != nil {
			t.Fatalf("leaving an unproved reservation on the account: %v", err)
		}
		if n := h.count(tableAccounts, "id = ? AND signup_state = ?", id.String(), signupNone); n != 1 {
			t.Fatalf("the account's stored state is not none; the case no longer covers the column")
		}
		owner, ok, err := h.p.PendingAccountByContact(ctx, string(authn.IdentifierEmail), "stored@example.com")
		if err != nil {
			t.Fatalf("reading the pending owner: %v", err)
		}
		if !ok || owner != id.String() {
			t.Errorf("PendingAccountByContact = %q, %v; want the account holding the unproved reservation", owner, ok)
		}
	})
}

// Proving an email ends the pending state, keeps the contact row with its proof instant, and records the instant once.
func TestTheRegistrationStateMovesWithProvedEmailAddresses(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.openSelfRegistration()
		account := h.mustRegister("alice", "alice@example.com", "password123")

		status, err := h.p.SignupStateOf(ctx, account.ID)
		if err != nil {
			t.Fatalf("reading the signup state: %v", err)
		}
		if status.State != authn.SignupPending || status.Email != "alice@example.com" || !status.Since.IsZero() {
			t.Errorf("the fresh registration reads %+v, want pending on alice@example.com and no instant", status)
		}

		if err = h.p.AddIdentifier(ctx, account.ID, authn.IdentifierEmail, "admin@example.com",
			authn.Verification{Via: authn.VerifiedViaAdmin}); err != nil {
			t.Fatalf("adding an admin email: %v", err)
		}
		status, err = h.p.SignupStateOf(ctx, account.ID)
		if err != nil {
			t.Fatalf("reading the signup state: %v", err)
		}
		if status.State != authn.SignupPending {
			t.Errorf("an unproven email moved the state to %v, want pending", status.State)
		}
		if n := h.count(tablePending, "account_id = ?", account.ID.String()); n != 2 {
			t.Errorf("an added email left %d reservation(s), want the registration's and the added one", n)
		}
		if n := h.count(tablePending, "account_id = ? AND value = ? AND verified_at IS NOT NULL",
			account.ID.String(), "admin@example.com"); n != 1 {
			t.Errorf("the added address has %d proved reservation(s), want one", n)
		}

		provedAt := h.clock.read()
		if err = h.p.AddIdentifier(ctx, account.ID, authn.IdentifierEmail, "alice@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving the email: %v", err)
		}
		status, err = h.p.SignupStateOf(ctx, account.ID)
		if err != nil {
			t.Fatalf("reading the signup state: %v", err)
		}
		if status.State != authn.SignupVerified || status.Email != "alice@example.com" || !status.Since.Equal(provedAt) {
			t.Errorf("after proving the address the state reads %+v, want verified on alice@example.com since %v",
				status, provedAt)
		}
		if n := h.count(tablePending, "account_id = ? AND value = ? AND verified_at IS NOT NULL",
			account.ID.String(), "alice@example.com"); n != 1 {
			t.Errorf("the proved address left %d proved reservation(s), want the row kept and stamped", n)
		}

		h.clock.advance(time.Hour)
		if err = h.p.AddIdentifier(ctx, account.ID, authn.IdentifierEmail, "later@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving a second address: %v", err)
		}
		status, err = h.p.SignupStateOf(ctx, account.ID)
		if err != nil {
			t.Fatalf("reading the signup state: %v", err)
		}
		if !status.Since.Equal(provedAt) || status.Email != "alice@example.com" {
			t.Errorf("a second proved address reads %+v, want the first address and the first instant", status)
		}

		if err = h.p.AddIdentifier(ctx, account.ID, authn.IdentifierPhone, "+8613800000000",
			authn.Verification{Via: authn.VerifiedViaSMSCode}); err != nil {
			t.Fatalf("adding a phone: %v", err)
		}
		status, err = h.p.SignupStateOf(ctx, account.ID)
		if err != nil {
			t.Fatalf("reading the signup state: %v", err)
		}
		if status.State != authn.SignupVerified || !status.Since.Equal(provedAt) {
			t.Errorf("a proved phone moved the state to %+v, want verified and the same instant", status)
		}

		for _, address := range []string{"later@example.com", "alice@example.com"} {
			if err = h.p.RemoveIdentifier(ctx, account.ID, h.identifierIDOf(account.ID, address)); err != nil {
				t.Fatalf("removing %s: %v", address, err)
			}
		}
		status, err = h.p.SignupStateOf(ctx, account.ID)
		if err != nil {
			t.Fatalf("reading the signup state: %v", err)
		}
		if status.State != authn.SignupPending || status.Email != "admin@example.com" || !status.Since.Equal(provedAt) {
			t.Errorf("removing the last proved address reads %+v, want pending on admin@example.com and the kept instant",
				status)
		}
		if n := h.count(tablePending, "account_id = ? AND verified_at IS NULL", account.ID.String()); n != 0 {
			t.Errorf("removing the last proved address rebuilt %d unproved reservation(s), want none", n)
		}

		if err = h.p.AddIdentifier(ctx, account.ID, authn.IdentifierPhone, "+8613800000001",
			authn.Verification{Via: authn.VerifiedViaSMSCode}); err != nil {
			t.Fatalf("replacing the phone: %v", err)
		}
		if status, err = h.p.SignupStateOf(ctx, account.ID); err != nil || status.State != authn.SignupPending {
			t.Errorf("a proved phone after the fallback reads (%+v, %v), want pending", status, err)
		}
	})
}

// Two addresses verified at the same instant report the identifier that sorts first.
func TestTheVerifiedAddressBreaksATieByIdentifier(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.openSelfRegistration()
		account := h.mustRegister("alice", "alice@example.com", "password123")
		if err := h.handle.Exec("DELETE FROM "+tableIdentifiers+" WHERE account_id = ?", account.ID.String()).Error; err != nil {
			t.Fatalf("clearing the identifiers: %v", err)
		}
		at := h.clock.read()
		for _, row := range []identifierRow{
			{
				ID: "bbb", AccountID: account.ID.String(), Kind: string(authn.IdentifierEmail), Value: "b@example.com",
				VerifiedAt: at, VerifiedVia: authn.VerifiedViaEmailCode, CreatedAt: at,
			},
			{
				ID: "aaa", AccountID: account.ID.String(), Kind: string(authn.IdentifierEmail), Value: "a@example.com",
				VerifiedAt: at, VerifiedVia: authn.VerifiedViaIDPPrefix + "google", CreatedAt: at,
			},
		} {
			if err := h.handle.Create(&row).Error; err != nil {
				t.Fatalf("writing an identifier: %v", err)
			}
		}
		if err := h.handle.Exec("UPDATE "+tableAccounts+" SET signup_state = ? WHERE id = ?",
			signupVerified, account.ID.String()).Error; err != nil {
			t.Fatalf("setting the state: %v", err)
		}
		status, err := h.p.SignupStateOf(ctx, account.ID)
		if err != nil {
			t.Fatalf("reading the signup state: %v", err)
		}
		if status.Email != "a@example.com" {
			t.Errorf("the tied addresses report %q, want the identifier that sorts first", status.Email)
		}
	})
}

// The reverse lookup answers only when exactly one pending account lists the contact, and normalizes what it was asked for.
func TestPendingAccountByContactReturnsOnlyAnUnambiguousOwner(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.openSelfRegistration()

		if _, ok, err := h.p.PendingAccountByContact(ctx, "email", "nobody@example.com"); err != nil || ok {
			t.Fatalf("an address no account lists gave (ok %v, err %v), want no owner and no error", ok, err)
		}

		account := h.mustRegister("alice", "alice@example.com", "password123")
		got, ok, err := h.p.PendingAccountByContact(ctx, "email", "  ALICE@Example.COM ")
		if err != nil || !ok || got != account.ID.String() {
			t.Fatalf("the pending contact gave (%q, %v, %v), want the account that lists it", got, ok, err)
		}

		if _, err = h.register("second", "alice@example.com", "password123"); !errors.Is(err, authn.ErrContactTaken) {
			t.Fatalf("a second registration of the reserved address gave %v, want ErrContactTaken", err)
		}
		if got2, ok, lookupErr := h.p.PendingAccountByContact(ctx, "email", "alice@example.com"); lookupErr != nil ||
			!ok || got2 != account.ID.String() {
			t.Errorf("the reserved address gave (%q, %v, %v), want the account that reserved it", got2, ok, lookupErr)
		}

		if _, _, err = h.p.PendingAccountByContact(ctx, "username", "alice"); !errors.Is(err, authn.ErrInvalidIdentifier) {
			t.Errorf("a kind that is not email or phone gave %v, want ErrInvalidIdentifier", err)
		}
		if _, _, err = h.p.PendingAccountByContact(ctx, "email", "not an address"); !errors.Is(err, authn.ErrInvalidIdentifier) {
			t.Errorf("an address that cannot be normalized gave %v, want ErrInvalidIdentifier", err)
		}
	})
}

// A deleted account holds no pending row, so the reverse lookup finds no owner for its address.
func TestPendingAccountByContactIgnoresDeletedAccounts(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.openSelfRegistration()
		account := h.mustRegister("alice", "alice@example.com", "password123")
		if err := h.p.Delete(ctx, account.ID, authn.ReasonAdmin); err != nil {
			t.Fatalf("deleting the account: %v", err)
		}
		if n := h.count(tablePending, "account_id = ?", account.ID.String()); n != 0 {
			t.Errorf("a deleted account holds %d pending contact(s), want none", n)
		}
		if _, ok, err := h.p.PendingAccountByContact(ctx, "email", "alice@example.com"); err != nil || ok {
			t.Errorf("an address of a deleted account gave (ok %v, err %v), want no owner", ok, err)
		}
	})
}

// The signup-username back-off is read before the uniqueness check and answered as ErrThrottled, and a success clears it.
func TestRegisterIsHeldBackByTheSignupUsernameBackOff(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		h.user("taken", "")
		for i := range defaults.ThrottleFreeAttempts + 1 {
			if _, err := h.register("taken", "spare@example.com", "password123"); !errors.Is(err, authn.ErrAccountExists) {
				t.Fatalf("attempt %d gave %v, want ErrAccountExists", i, err)
			}
		}
		_, err := h.register("taken", "spare@example.com", "password123")
		var throttled *authn.ThrottledError
		if !errors.As(err, &throttled) || throttled.RetryAfter <= 0 {
			t.Fatalf("the attempt past the free ones gave %v, want a ThrottledError with a wait", err)
		}

		h.clock.advance(defaults.ThrottleMaxDelay + time.Minute)
		h.mustRegister("free", "free@example.com", "password123")
		if _, ok := h.failuresOf(authn.ScopeSignupUsername, "free"); ok {
			t.Error("a successful registration left its signup-username counter, want it cleared")
		}
	})
}

// A full self-registration width is ErrBusy and takes no password login's place.
func TestRegisterDerivesUnderAWidthOfItsOwn(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		h.p.signupWait = 0
		h.p.signupGov = password.NewWidthGovernor(1)
		release, err := h.p.signupGov.Hold(context.Background(), 0, 0)
		if err != nil {
			t.Fatalf("taking the whole signup width: %v", err)
		}
		defer release()
		if _, err := h.register("alice", "alice@example.com", "password123"); !errors.Is(err, authn.ErrBusy) {
			t.Fatalf("registration with the width full gave %v, want ErrBusy", err)
		}
		if n := h.count(tableAccounts, ""); n != 0 {
			t.Errorf("a busy registration wrote %d account(s)", n)
		}
	})
}

// SignupStateOf answers a missing account as ErrAccountNotFound and a failed read as ErrUnavailable, not SignupNone.
func TestSignupStateOfReportsAMissingAccount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		if _, err := h.p.SignupStateOf(context.Background(), "00000000-0000-7000-8000-000000000000"); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("a missing account gave %v, want ErrAccountNotFound", err)
		}
		h.handle.Exec("DROP TABLE " + tableAccounts)
		if _, err := h.p.SignupStateOf(context.Background(), "00000000-0000-7000-8000-000000000000"); !errors.Is(err, authn.ErrUnavailable) {
			t.Errorf("an unreadable account gave %v, want ErrUnavailable", err)
		}
		if _, _, err := h.p.PendingAccountByContact(context.Background(), "email", "a@example.com"); !errors.Is(err, authn.ErrUnavailable) {
			t.Errorf("an unreadable pending contact gave %v, want ErrUnavailable", err)
		}
	})
}

// An administrator-created email reads pending until the holder proves it, and the proof verifies the account without a second identifier.
func TestAnAdministratorCreatedEmailMustBeProved(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		account, err := h.p.Create(ctx, authn.CreateAccount{
			Username: "bob", Email: "bob@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin}, Via: "admin",
		})
		if err != nil {
			t.Fatalf("creating an account: %v", err)
		}
		status, err := h.p.SignupStateOf(ctx, account.ID)
		if err != nil {
			t.Fatalf("reading the state of a fresh account: %v", err)
		}
		if status.State != authn.SignupPending || status.Email != "bob@example.com" || !status.Since.IsZero() {
			t.Fatalf("the administrator-created account reads %+v, want pending on bob@example.com and no instant", status)
		}

		provedAt := h.clock.read()
		if err = h.p.AddIdentifier(ctx, account.ID, authn.IdentifierEmail, "bob@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving the address: %v", err)
		}
		status, err = h.p.SignupStateOf(ctx, account.ID)
		if err != nil {
			t.Fatalf("reading the proved state: %v", err)
		}
		if status.State != authn.SignupVerified || status.Email != "bob@example.com" || !status.Since.Equal(provedAt) {
			t.Errorf("the proved account reads %+v, want verified on bob@example.com since %v", status, provedAt)
		}
		if n := h.count(tableIdentifiers, "account_id = ? AND kind = ?", account.ID.String(), string(authn.IdentifierEmail)); n != 1 {
			t.Errorf("the account holds %d email identifier(s), want the one written by Create", n)
		}
		var via []string
		if err = h.handle.Raw("SELECT verified_via FROM "+tableIdentifiers+" WHERE account_id = ? AND kind = ?",
			account.ID.String(), string(authn.IdentifierEmail)).Scan(&via).Error; err != nil {
			t.Fatalf("reading the way of verification: %v", err)
		}
		if len(via) != 1 || via[0] != authn.VerifiedViaEmailCode {
			t.Errorf("the identifier was verified via %v, want email-code", via)
		}
		if state, at := h.signupRow(account.ID); state != signupNone || at == nil || !at.Equal(provedAt) {
			t.Errorf("proving wrote signup_state %d and verified_at %v, want 0 and %v", state, at, provedAt)
		}
	})
}

// An account with no email address reads none: there is nothing to verify.
func TestAnAccountWithoutAnEmailReadsNone(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		account, err := h.p.Create(ctx, authn.CreateAccount{Username: "solo", Via: "admin"})
		if err != nil {
			t.Fatalf("creating an account without an email: %v", err)
		}
		status, err := h.p.SignupStateOf(ctx, account.ID)
		if err != nil {
			t.Fatalf("reading the state: %v", err)
		}
		if status.State != authn.SignupNone || status.Email != "" || !status.Since.IsZero() {
			t.Errorf("an account with no email reads %+v, want none with no address and no instant", status)
		}
	})
}

// Proving the account's own unproved address brings its identifier up to proved once, and again changes nothing.
func TestProvingTheAccountsOwnEmailIsIdempotent(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		account, err := h.p.Create(ctx, authn.CreateAccount{
			Username: "bob", Email: "bob@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin}, Via: "admin",
		})
		if err != nil {
			t.Fatalf("creating an account: %v", err)
		}
		before := len(h.bus.of(authn.EventIdentifierChanged))
		codeVia := authn.Verification{Via: authn.VerifiedViaEmailCode}
		for i := range 2 {
			if err = h.p.AddIdentifier(ctx, account.ID, authn.IdentifierEmail, "bob@example.com", codeVia); err != nil {
				t.Fatalf("proof %d of the account's own address: %v", i+1, err)
			}
		}
		if n := h.count(tableIdentifiers, "account_id = ? AND kind = ?", account.ID.String(), string(authn.IdentifierEmail)); n != 1 {
			t.Errorf("proving twice left %d email identifier(s), want one", n)
		}
		for _, row := range h.identifierRows(account.ID) {
			if row.Kind != string(authn.IdentifierEmail) {
				continue
			}
			if row.VerifiedVia != authn.VerifiedViaEmailCode || !(authn.Verification{Via: row.VerifiedVia}).Proves() {
				t.Errorf("the proved identifier is %+v, want an email-code proof", row)
			}
		}
		if got := len(h.bus.of(authn.EventIdentifierChanged)); got != before {
			t.Errorf("proving the account's own address published %d identifier event(s), want none", got-before)
		}

		if err = h.p.AddIdentifier(ctx, account.ID, authn.IdentifierEmail, "bob@example.com",
			authn.Verification{Via: authn.VerifiedViaAdmin}); err != nil {
			t.Errorf("an unproved way for a proved address gave %v, want the proof kept", err)
		}
		for _, row := range h.identifierRows(account.ID) {
			if row.Kind == string(authn.IdentifierEmail) && row.VerifiedVia != authn.VerifiedViaEmailCode {
				t.Errorf("an unproved way downgraded the identifier to %q", row.VerifiedVia)
			}
		}

		other, err := h.p.Create(ctx, authn.CreateAccount{Username: "rival", Via: "admin"})
		if err != nil {
			t.Fatalf("creating another account: %v", err)
		}
		if err = h.p.AddIdentifier(ctx, other.ID, authn.IdentifierEmail, "BOB@example.com", codeVia); !errors.Is(err, authn.ErrContactTaken) {
			t.Errorf("another account naming the held address gave %v, want ErrContactTaken", err)
		}
	})
}

// Removing the last proved address leaves nothing to verify, which reads none rather than pending.
func TestRemovingTheLastEmailReadsNone(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.openSelfRegistration()
		account := h.mustRegister("alice", "alice@example.com", "password123")
		if err := h.p.AddIdentifier(ctx, account.ID, authn.IdentifierEmail, "alice@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving the address: %v", err)
		}
		if err := h.p.RemoveIdentifier(ctx, account.ID, h.identifierIDOf(account.ID, "alice@example.com")); err != nil {
			t.Fatalf("removing the address: %v", err)
		}
		status, err := h.p.SignupStateOf(ctx, account.ID)
		if err != nil {
			t.Fatalf("reading the state: %v", err)
		}
		if status.State != authn.SignupNone || status.Email != "" || !status.Since.IsZero() {
			t.Errorf("an account that lost its last email reads %+v, want none with no address and no instant", status)
		}
	})
}

func (h *harness) identifierRows(id authn.AccountID) []identifierRow {
	h.t.Helper()
	var rows []identifierRow
	if err := h.handle.Where("account_id = ?", id.String()).Find(&rows).Error; err != nil {
		h.t.Fatalf("reading the identifiers: %v", err)
	}
	return rows
}

// Create writes signup_state 0, and the caller states how the username came about, admin being the zero value.
func TestCreateWritesNoRegistration(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		admin := authn.CreateAccount{
			Username: "bob", Email: "bob@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin}, Via: "admin",
		}
		account, err := h.p.Create(ctx, admin)
		if err != nil {
			t.Fatalf("creating an account: %v", err)
		}
		if state, at := h.signupRow(account.ID); state != signupNone || at != nil {
			t.Errorf("Create wrote signup_state %d and verified_at %v, want 0 and NULL", state, at)
		}
		if n := h.count(tableIdentifiers, "account_id = ? AND kind = ? AND verified_via = ?",
			account.ID.String(), string(authn.IdentifierUsername), authn.VerifiedViaAdmin); n != 1 {
			t.Errorf("the zero UsernameVerifiedVia is %d admin rows, want 1", n)
		}
		if n := h.count(tablePending, "verified_at IS NOT NULL"); n != 1 {
			t.Errorf("Create wrote %d proved reservations, want the account's email address", n)
		}
		if n := h.count(tablePending, "verified_at IS NULL"); n != 0 {
			t.Errorf("Create wrote %d unproved reservation(s), want none", n)
		}

		self := admin
		self.Username = "carol"
		self.Email = "carol@example.com"
		self.UsernameVerifiedVia = authn.Verification{Via: authn.VerifiedViaSelf}
		account, err = h.p.Create(ctx, self)
		if err != nil {
			t.Fatalf("creating an account with a self username: %v", err)
		}
		if n := h.count(tableIdentifiers, "account_id = ? AND kind = ? AND verified_via = ?",
			account.ID.String(), string(authn.IdentifierUsername), authn.VerifiedViaSelf); n != 1 {
			t.Errorf("UsernameVerifiedVia self is %d rows, want 1", n)
		}
		if n := h.count(tablePending, "verified_at IS NOT NULL"); n != 2 {
			t.Errorf("two created accounts hold %d proved reservations, want one each", n)
		}

		bad := admin
		bad.Username = "dave"
		bad.Email = "dave@example.com"
		bad.UsernameVerifiedVia = authn.Verification{Via: "trust-me"}
		if _, err = h.p.Create(ctx, bad); !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("a username verification that is no form gave %v, want ErrInvalidArgument", err)
		}
	})
}

// The sweep deletes only a self-registration that never proved an address and has been idle past the ttl.
func TestTheUnverifiedSweepDeletesOnlyAnIdlePendingAccount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.openSelfRegistration()

		idle := h.mustRegister("idle", "idle@example.com", "password123")
		verified := h.mustRegister("verified", "verified@example.com", "password123")
		if err := h.p.AddIdentifier(ctx, verified.ID, authn.IdentifierEmail, "verified@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving the address: %v", err)
		}
		plain := h.user("plain", "")

		h.clock.advance(defaults.UnverifiedTTL + time.Hour)
		if _, err := h.register("early", "idle@example.com", "password123"); !errors.Is(err, authn.ErrContactTaken) {
			t.Fatalf("a registration of the idle address before the sweep gave %v, want ErrContactTaken", err)
		}
		recent := h.mustRegister("recent", "recent@example.com", "password123")

		deleted, err := h.p.sweepUnverified(ctx)
		if err != nil {
			t.Fatalf("sweeping: %v", err)
		}
		if deleted != 1 {
			t.Errorf("the sweep deleted %d account(s), want the one idle pending registration", deleted)
		}
		if n := h.count(tableAccounts, "id = ? AND status = ?", idle.ID.String(), statusDeleted); n != 1 {
			t.Errorf("the idle registration is %d tombstones, want 1", n)
		}
		if n := h.count(tableIdentifiers, "account_id = ?", idle.ID.String()); n != 0 {
			t.Errorf("the swept account holds %d identifier(s), want none", n)
		}
		if n := h.count(tablePending, "account_id = ?", idle.ID.String()); n != 0 {
			t.Errorf("the swept account holds %d pending contact(s), want none", n)
		}
		for _, kept := range []authn.AccountID{verified.ID, plain, recent.ID} {
			if n := h.count(tableAccounts, "id = ? AND status <> ?", kept.String(), statusDeleted); n != 1 {
				t.Errorf("the sweep deleted account %s, want it kept", kept)
			}
		}

		if _, err = h.register("reuser", "idle@example.com", "password123"); err != nil {
			t.Fatalf("a registration of the swept address gave %v, want it released", err)
		}
	})
}

// Registration publishes the account's creation via signup, the value the anonymous route reports.
func TestRegisterPublishesAnAccountCreatedEventWithSignup(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		account := h.mustRegister("alice", "alice@example.com", "password123")
		created := bindingEvent[authn.AccountCreated](t, h, authn.EventAccountCreated)
		if created.AccountID != account.ID.String() || created.Kind != string(authn.KindUser) || created.Via != "signup" {
			t.Errorf("the created event is %+v, want account %s, kind user, via signup", created, account.ID)
		}
	})
}

// A username or address that cannot be normalized is refused before the back-off and before any row is written.
func TestRegisterRefusesIdentifiersThatCannotBeNormalized(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		for _, in := range []authn.RegisterAccount{
			{Username: "not a username", Email: "alice@example.com", Password: "password123"},
			{Username: "alice", Email: "not an address", Password: "password123"},
		} {
			if _, err := h.p.Register(context.Background(), in); !errors.Is(err, authn.ErrInvalidIdentifier) {
				t.Errorf("registering %+v gave %v, want ErrInvalidIdentifier", in, err)
			}
		}
		for _, table := range []string{tableAccounts, tableIdentifiers, tablePending, tableCredentials} {
			if n := h.count(table, ""); n != 0 {
				t.Errorf("a registration of an identifier that cannot be normalized left %d row(s) in %s", n, table)
			}
		}
	})
}

// A provider claim through AddIdentifier moves a pending account to verified and consumes its pending contact.
func TestAddIdentifierProvesAPendingRegistration(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.openSelfRegistration()

		account := h.mustRegister("provider", "provider@example.com", "password123")
		provedAt := h.clock.read()
		if err := h.p.AddIdentifier(ctx, account.ID, authn.IdentifierEmail, "provider@example.com",
			authn.Verification{Via: authn.VerifiedViaIDPPrefix + "github"}); err != nil {
			t.Fatalf("proving the address: %v", err)
		}
		status, err := h.p.SignupStateOf(ctx, account.ID)
		if err != nil {
			t.Fatalf("reading the signup state: %v", err)
		}
		if status.State != authn.SignupVerified || status.Email != "provider@example.com" || !status.Since.Equal(provedAt) {
			t.Errorf("the state reads %+v, want verified on %s since %v", status, status.Email, provedAt)
		}
		if n := h.count(tablePending, "account_id = ? AND verified_at IS NOT NULL", account.ID.String()); n != 1 {
			t.Errorf("the proved address left %d proved reservation(s), want the row kept and stamped", n)
		}

		invited := h.mustRegister("invited", "invited@example.com", "password123")
		if err := h.p.AddIdentifier(ctx, invited.ID, authn.IdentifierEmail, "invited@example.com",
			authn.Verification{Via: authn.VerifiedViaInvite}); err != nil {
			t.Fatalf("writing an invitation channel: %v", err)
		}
		if status, err := h.p.SignupStateOf(ctx, invited.ID); err != nil || status.State != authn.SignupPending {
			t.Errorf("an invitation channel reads (%+v, %v), want pending: an invitation proves no address", status, err)
		}
	})
}

// A proved phone consumes a pending contact with the same number and changes no registration state.
func TestAProvedPhoneConsumesAPendingPhoneContact(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.openSelfRegistration()
		account := h.mustRegister("alice", "alice@example.com", "password123")
		now := h.clock.read()
		pending := pendingContactRow{
			ID: "pending-phone", AccountID: account.ID.String(), Kind: string(authn.IdentifierPhone),
			Value: "+8613800000000", CreatedAt: now, UpdatedAt: now,
		}
		if err := h.handle.Create(&pending).Error; err != nil {
			t.Fatalf("writing a phone contact: %v", err)
		}
		if err := h.p.AddIdentifier(ctx, account.ID, authn.IdentifierPhone, "+8613800000000",
			authn.Verification{Via: authn.VerifiedViaSMSCode}); err != nil {
			t.Fatalf("proving the phone: %v", err)
		}
		if n := h.count(tablePending, "account_id = ? AND kind = ?", account.ID.String(), string(authn.IdentifierPhone)); n != 0 {
			t.Errorf("the proved phone left %d pending contact(s), want the same value deleted", n)
		}
		if status, err := h.p.SignupStateOf(ctx, account.ID); err != nil || status.State != authn.SignupPending {
			t.Errorf("a proved phone reads (%+v, %v), want pending", status, err)
		}
	})
}

// The reverse lookup reports only an unproved reservation, not a verified account's contact row.
func TestPendingAccountByContactIgnoresAVerifiedAccount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.openSelfRegistration()
		account := h.mustRegister("alice", "alice@example.com", "password123")
		if err := h.p.AddIdentifier(ctx, account.ID, authn.IdentifierEmail, "alice@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving the address: %v", err)
		}
		if n := h.count(tablePending, "account_id = ? AND verified_at IS NOT NULL", account.ID.String()); n != 1 {
			t.Fatalf("the proved address holds %d proved reservation(s), want one", n)
		}
		if owner, ok, err := h.p.PendingAccountByContact(ctx, "email", "alice@example.com"); err != nil || ok {
			t.Errorf("the address of a verified account gave (%q, %v, %v), want no owner", owner, ok, err)
		}
	})
}

// The sweep keeps an account that proved an address through a provider claim, and one whose row says pending while a proving email identifier exists.
func TestTheUnverifiedSweepKeepsAProvedAccount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.openSelfRegistration()
		invited := h.mustRegister("invited", "invited@example.com", "password123")
		claimed := h.mustRegister("claimed", "claimed@example.com", "password123")
		inconsistent := h.mustRegister("inconsistent", "left@example.com", "password123")
		if err := h.p.AddIdentifier(ctx, invited.ID, authn.IdentifierEmail, "invited@example.com",
			authn.Verification{Via: authn.VerifiedViaInvite}); err != nil {
			t.Fatalf("writing an invitation channel: %v", err)
		}
		if err := h.p.AddIdentifier(ctx, claimed.ID, authn.IdentifierEmail, "claimed@example.com",
			authn.Verification{Via: authn.VerifiedViaIDPPrefix + "github"}); err != nil {
			t.Fatalf("proving by a provider claim: %v", err)
		}
		now := h.clock.read()
		if err := h.handle.Create(&identifierRow{
			ID: "leftover", AccountID: inconsistent.ID.String(), Kind: string(authn.IdentifierEmail),
			Value: "left@example.com", VerifiedAt: now, VerifiedVia: authn.VerifiedViaEmailCode, CreatedAt: now,
		}).Error; err != nil {
			t.Fatalf("writing the inconsistent identifier: %v", err)
		}

		h.clock.advance(defaults.UnverifiedTTL + time.Hour)
		deleted, err := h.p.sweepUnverified(ctx)
		if err != nil {
			t.Fatalf("sweeping: %v", err)
		}
		if deleted != 1 {
			t.Errorf("the sweep deleted %d account(s), want the invitation-only one alone", deleted)
		}
		if n := h.count(tableAccounts, "id = ? AND status <> ?", invited.ID.String(), statusDeleted); n != 0 {
			t.Error("the sweep kept an account whose only email channel is an invitation")
		}
		for _, kept := range []authn.AccountID{claimed.ID, inconsistent.ID} {
			if n := h.count(tableAccounts, "id = ? AND status <> ?", kept.String(), statusDeleted); n != 1 {
				t.Errorf("the sweep deleted account %s, want it kept", kept)
			}
		}
	})
}

// The deletion the sweep runs re-judges its conditions in its transaction, so an account that proved an address or logged in is skipped.
func TestTheUnverifiedDeletionSkipsAnAccountThatChangedState(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.openSelfRegistration()
		proved := h.mustRegister("proved", "proved@example.com", "password123")
		loggedIn := h.mustRegister("loggedin", "loggedin@example.com", "password123")
		leftover := h.mustRegister("leftover", "left@example.com", "password123")
		stamped := h.mustRegister("stamped", "stamped@example.com", "password123")

		h.clock.advance(defaults.UnverifiedTTL + time.Hour)
		horizon := h.clock.read().Add(-defaults.UnverifiedTTL)
		if err := h.p.AddIdentifier(ctx, proved.ID, authn.IdentifierEmail, "proved@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving the address: %v", err)
		}
		if err := h.p.Touch(ctx, loggedIn.ID, h.clock.read()); err != nil {
			t.Fatalf("recording a login: %v", err)
		}
		at := h.clock.read()
		if err := h.handle.Create(&identifierRow{
			ID: "leftover-email", AccountID: leftover.ID.String(), Kind: string(authn.IdentifierEmail),
			Value: "left@example.com", VerifiedAt: at, VerifiedVia: authn.VerifiedViaIDPPrefix + "github", CreatedAt: at,
		}).Error; err != nil {
			t.Fatalf("writing a proved email on a pending row: %v", err)
		}
		if err := h.handle.Exec("UPDATE "+tableAccounts+" SET signup_verified_at = ? WHERE id = ?",
			at, stamped.ID.String()).Error; err != nil {
			t.Fatalf("stamping the instant on a pending row: %v", err)
		}
		for _, c := range []struct {
			name string
			id   authn.AccountID
		}{{"proved", proved.ID}, {"logged in", loggedIn.ID}, {"a proved email", leftover.ID}, {"a stamped instant", stamped.ID}} {
			deleted, err := h.p.deleteUnverified(ctx, c.id, horizon)
			if err != nil {
				t.Fatalf("%s: deleting: %v", c.name, err)
			}
			if deleted {
				t.Errorf("%s: the deletion took an account that changed state, want it skipped", c.name)
			}
			if n := h.count(tableAccounts, "id = ? AND status <> ?", c.id.String(), statusDeleted); n != 1 {
				t.Errorf("%s: the account is %d live rows, want it kept", c.name, n)
			}
		}
	})
}

// A successful registration clears the failures the name had collected.
func TestASuccessfulRegistrationClearsTheUsernameBackOff(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		h.openSelfRegistration()
		taken := h.user("later", "")
		for i := range defaults.ThrottleFreeAttempts + 1 {
			if _, err := h.register("later", "spare@example.com", "password123"); !errors.Is(err, authn.ErrAccountExists) {
				t.Fatalf("attempt %d gave %v, want ErrAccountExists", i, err)
			}
		}
		if n, ok := h.failuresOf(authn.ScopeSignupUsername, "later"); !ok || n == 0 {
			t.Fatalf("the refused attempts left %d failure(s) (present %v), want the count the success clears", n, ok)
		}
		if err := h.p.Delete(ctx, taken, authn.ReasonAdmin); err != nil {
			t.Fatalf("freeing the name: %v", err)
		}
		h.clock.advance(defaults.ThrottleMaxDelay + time.Minute)
		h.mustRegister("later", "later@example.com", "password123")
		if n, ok := h.failuresOf(authn.ScopeSignupUsername, "later"); ok {
			t.Errorf("the successful registration left %d failure(s) under the name, want none", n)
		}
	})
}

// A back-off that begins on a registration reports the registration scope to the event and the counter.
func TestTheSignupBackOffReportsItsScope(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		meters := h.withMeters()
		h.user("taken", "")
		for i := range defaults.ThrottleFreeAttempts + 1 {
			if _, err := h.register("taken", "spare@example.com", "password123"); !errors.Is(err, authn.ErrAccountExists) {
				t.Fatalf("attempt %d gave %v, want ErrAccountExists", i, err)
			}
		}
		events := h.bus.of(authn.EventThrottleEngaged)
		if len(events) != 1 {
			t.Fatalf("%d engagement events, want one", len(events))
		}
		var payload authn.ThrottleEngaged
		if err := event.DecodePayload(events[0].Payload, &payload); err != nil {
			t.Fatalf("decoding the engagement: %v", err)
		}
		if payload.Scope != string(authn.ScopeSignupUsername) || payload.AccountID != "" {
			t.Errorf("the engagement is %+v, want the signup-username scope and no account", payload)
		}
		if n := meters.counter("authn.throttle.engagements", map[string]string{"scope": "signup-username"}); n != 1 {
			t.Errorf("the counter holds %d engagement(s) under signup-username, want one", n)
		}
	})
}

// The signup migration upgrades a database that already has the earlier declaration: its row reads as no registration, and the pending contact table exists afterwards.
func TestTheSignupMigrationUpgradesAnExistingDatabase(t *testing.T) {
	for _, dialect := range dialectsUnderTest {
		t.Run(string(dialect), func(t *testing.T) {
			handle := quietly(databaseOpeners[dialect](t).DB())
			applyMigrationFile(t, handle, dialect, "0001_authn_account.sql")
			at := newTestClock().read()
			if err := handle.Exec("INSERT INTO "+tableAccounts+" (id, kind, status, display_name, attributes, "+
				"mfa_required, protected, valid_after, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
				"existing", string(authn.KindUser), statusActive, "", "{}", false, false, at, at, at).Error; err != nil {
				t.Fatalf("writing a row of the earlier declaration: %v", err)
			}

			applyMigrationFile(t, handle, dialect, "0002_authn_signup.sql")

			if !handle.Migrator().HasTable(tablePending) {
				t.Errorf("the upgrade did not add %s", tablePending)
			}
			var rows []struct {
				SignupState      int16
				SignupVerifiedAt *time.Time
			}
			if err := handle.Raw("SELECT signup_state, signup_verified_at FROM "+tableAccounts+" WHERE id = ?",
				"existing").Scan(&rows).Error; err != nil {
				t.Fatalf("reading the upgraded row: %v", err)
			}
			if len(rows) != 1 || rows[0].SignupState != signupNone || rows[0].SignupVerifiedAt != nil {
				t.Errorf("the row that was there reads %+v, want signup_state 0 and no instant", rows)
			}
			applyMigrationFile(t, handle, dialect, "0003_contact_claim.sql")
			cfg, err := configDefaults.resolve()
			if err != nil {
				t.Fatalf("resolving the default configuration: %v", err)
			}
			p := newProduct(stubDatabase{handle: handle, dialect: dialect}, cfg)
			p.now = newTestClock().read
			status, err := p.SignupStateOf(context.Background(), "existing")
			if err != nil {
				t.Fatalf("reading the migrated account: %v", err)
			}
			if status.State != authn.SignupNone || status.Email != "" || !status.Since.IsZero() {
				t.Errorf("the migrated account reads %+v, want no registration", status)
			}
		})
	}
}

// The contact-claim migration turns the pending table into the reservation table, keeping its row with an empty verified_at.
func TestTheContactClaimMigrationUpgradesAnExistingDatabase(t *testing.T) {
	for _, dialect := range dialectsUnderTest {
		t.Run(string(dialect), func(t *testing.T) {
			handle := quietly(databaseOpeners[dialect](t).DB())
			applyMigrationFile(t, handle, dialect, "0001_authn_account.sql")
			applyMigrationFile(t, handle, dialect, "0002_authn_signup.sql")
			at := newTestClock().read()
			if err := handle.Exec("INSERT INTO "+tablePending+" (id, account_id, kind, value, created_at, updated_at) "+
				"VALUES (?, ?, ?, ?, ?, ?)", "existing", "account-a", string(authn.IdentifierEmail),
				"shared@example.com", at, at).Error; err != nil {
				t.Fatalf("writing a row of the earlier declaration: %v", err)
			}

			applyMigrationFile(t, handle, dialect, "0003_contact_claim.sql")

			var rows []struct {
				Value      string
				VerifiedAt *time.Time
			}
			if err := handle.Raw("SELECT value, verified_at FROM "+tablePending+" WHERE id = ?", "existing").
				Scan(&rows).Error; err != nil {
				t.Fatalf("reading the upgraded row: %v", err)
			}
			if len(rows) != 1 || rows[0].Value != "shared@example.com" || rows[0].VerifiedAt != nil {
				t.Errorf("the row that was there reads %+v, want the same value and no proof instant", rows)
			}
			insert := "INSERT INTO " + tablePending + " (id, account_id, kind, value, created_at, updated_at) " +
				"VALUES (?, ?, ?, ?, ?, ?)"
			if err := handle.Exec(insert, "second", "account-b", string(authn.IdentifierEmail),
				"shared@example.com", at, at).Error; !duplicate(err) {
				t.Errorf("a second row for one contact gave %v, want the unique constraint", err)
			}
			if err := handle.Exec(insert, "third", "account-a", string(authn.IdentifierEmail),
				"another@example.com", at, at).Error; err != nil {
				t.Errorf("a second address of one account gave %v, want it admitted", err)
			}
		})
	}
}

// A ttl of zero turns the sweep off.
func TestTheUnverifiedSweepCanBeTurnedOff(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		store := h.withSettings()
		store.set(passwordSignupPath, signupOpen)
		account := h.mustRegister("idle", "idle@example.com", "password123")
		h.clock.advance(defaults.UnverifiedTTL + time.Hour)
		store.set(unverifiedTTLPath, time.Duration(0))
		deleted, err := h.p.sweepUnverified(context.Background())
		if err != nil {
			t.Fatalf("sweeping: %v", err)
		}
		if deleted != 0 {
			t.Errorf("the sweep deleted %d account(s) with the ttl at zero, want none", deleted)
		}
		if n := h.count(tableAccounts, "id = ? AND status <> ?", account.ID.String(), statusDeleted); n != 1 {
			t.Errorf("the account with the sweep off is %d live rows, want 1", n)
		}
	})
}

// A registration that named no display name is named after the username it chose.
func TestRegisterNamesAnAccountWithoutADisplayNameAfterItsUsername(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		account := h.mustRegister("nameless-register", "nameless@example.com", "password123")
		if account.DisplayName != "nameless-register" {
			t.Errorf("display name = %q, want the username %q", account.DisplayName, "nameless-register")
		}
	})
}
