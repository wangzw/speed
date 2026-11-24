package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// The identifier-axes migration adds the two axes and the default contact, and backfills an older database without changing any stored fact.
func TestTheIdentifierAxesMigrationBackfillsExistingRows(t *testing.T) {
	for _, dialect := range dialectsUnderTest {
		t.Run(string(dialect), func(t *testing.T) {
			handle := quietly(databaseOpeners[dialect](t).DB())
			applyMigrationFile(t, handle, dialect, "0001_authn_account.sql")
			applyMigrationFile(t, handle, dialect, "0002_authn_signup.sql")
			applyMigrationFile(t, handle, dialect, "0003_contact_claim.sql")
			at := newTestClock().read()
			for _, id := range []string{"account-a", "account-b", "account-c", "account-d"} {
				if err := handle.Exec("INSERT INTO "+tableAccounts+" (id, kind, status, signup_state, display_name, attributes, "+
					"mfa_required, protected, valid_after, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
					id, string(authn.KindUser), statusActive, signupNone, "", "{}", false, false, at, at, at).Error; err != nil {
					t.Fatalf("writing account %s of the earlier declaration: %v", id, err)
				}
			}
			insert := func(id, account, kind, value, via string, provenAt time.Time) {
				t.Helper()
				if err := handle.Exec("INSERT INTO "+tableIdentifiers+" (id, account_id, kind, value, verified_at, verified_via, created_at) "+
					"VALUES (?, ?, ?, ?, ?, ?, ?)", id, account, kind, value, provenAt, via, at).Error; err != nil {
					t.Fatalf("writing identifier %s of the earlier declaration: %v", id, err)
				}
			}
			insert("u-a", "account-a", string(authn.IdentifierUsername), "alice", authn.VerifiedViaInvite, at)
			insert("e-a1", "account-a", string(authn.IdentifierEmail), "first@example.com", authn.VerifiedViaEmailCode, at)
			insert("e-a2", "account-a", string(authn.IdentifierEmail), "second@example.com", authn.VerifiedViaEmailCode, at.Add(time.Hour))
			insert("p-a", "account-a", string(authn.IdentifierPhone), "+12025550123", authn.VerifiedViaSMSCode, at)
			insert("e-b1", "account-b", string(authn.IdentifierEmail), "admin@example.com", authn.VerifiedViaAdmin, at)
			insert("e-b2", "account-b", string(authn.IdentifierEmail), "later@example.com", authn.VerifiedViaEmailCode, at.Add(2*time.Hour))
			insert("e-c", "account-c", string(authn.IdentifierEmail), "self@example.com", authn.VerifiedViaSelf, at)
			insert("e-d2", "account-d", string(authn.IdentifierEmail), "second-tie@example.com", authn.VerifiedViaEmailCode, at)
			insert("e-d1", "account-d", string(authn.IdentifierEmail), "first-tie@example.com", authn.VerifiedViaEmailCode, at)

			applyMigrationFile(t, handle, dialect, "0004_identifier_axes.sql")

			want := map[string]struct {
				signIn, contact, defaultContact bool
				via                             string
				provenAt                        time.Time
			}{
				"u-a":  {true, false, false, authn.VerifiedViaInvite, at},
				"e-a1": {true, true, true, authn.VerifiedViaEmailCode, at},
				"e-a2": {true, true, false, authn.VerifiedViaEmailCode, at.Add(time.Hour)},
				"p-a":  {true, false, false, authn.VerifiedViaSMSCode, at},
				"e-b1": {true, true, false, authn.VerifiedViaAdmin, at},
				"e-b2": {true, true, true, authn.VerifiedViaEmailCode, at.Add(2 * time.Hour)},
				"e-c":  {true, true, false, authn.VerifiedViaSelf, at},
				"e-d1": {true, true, true, authn.VerifiedViaEmailCode, at},
				"e-d2": {true, true, false, authn.VerifiedViaEmailCode, at},
			}
			var rows []struct {
				ID             string
				SignIn         bool
				Contact        bool
				DefaultContact bool
				VerifiedVia    string
				VerifiedAt     time.Time
			}
			if err := handle.Raw("SELECT id, sign_in, contact, default_contact, verified_via, verified_at FROM " +
				tableIdentifiers).Scan(&rows).Error; err != nil {
				t.Fatalf("reading the backfilled rows: %v", err)
			}
			if len(rows) != len(want) {
				t.Fatalf("the backfill left %d row(s), want %d", len(rows), len(want))
			}
			for _, row := range rows {
				expect, ok := want[row.ID]
				if !ok {
					t.Errorf("the backfill kept an unexpected row %q", row.ID)
					continue
				}
				if row.SignIn != expect.signIn || row.Contact != expect.contact || row.DefaultContact != expect.defaultContact {
					t.Errorf("row %q reads sign_in=%v contact=%v default_contact=%v, want %v %v %v",
						row.ID, row.SignIn, row.Contact, row.DefaultContact,
						expect.signIn, expect.contact, expect.defaultContact)
				}
				if row.VerifiedVia != expect.via || !utc(row.VerifiedAt).Equal(expect.provenAt) {
					t.Errorf("row %q reads verified_via=%q verified_at=%v, want the stored facts %q %v",
						row.ID, row.VerifiedVia, row.VerifiedAt, expect.via, expect.provenAt)
				}
			}

			if err := handle.Exec("INSERT INTO "+tableIdentifiers+" (id, account_id, kind, value, verified_at, verified_via, created_at) "+
				"VALUES (?, ?, ?, ?, ?, ?, ?)", "e-new", "account-c", string(authn.IdentifierEmail),
				"new@example.com", at, authn.VerifiedViaSelf, at).Error; err != nil {
				t.Fatalf("writing a row of the new declaration: %v", err)
			}
			var fresh []struct {
				SignIn         bool
				Contact        bool
				DefaultContact bool
			}
			if err := handle.Raw("SELECT sign_in, contact, default_contact FROM "+tableIdentifiers+" WHERE id = ?", "e-new").
				Scan(&fresh).Error; err != nil {
				t.Fatalf("reading the new row: %v", err)
			}
			if len(fresh) != 1 || fresh[0].SignIn || fresh[0].Contact || fresh[0].DefaultContact {
				t.Errorf("the new row reads %+v, want both axes and the default contact false", fresh)
			}

			cfg, err := configDefaults.resolve()
			if err != nil {
				t.Fatalf("resolving the default configuration: %v", err)
			}
			p := newProduct(stubDatabase{handle: handle, dialect: dialect}, cfg)
			p.now = newTestClock().read
			listed, err := p.Identifiers(context.Background(), "account-a")
			if err != nil {
				t.Fatalf("reading the migrated account: %v", err)
			}
			got := make(map[string]authn.Identifier, len(listed))
			for _, identifier := range listed {
				got[identifier.ID] = identifier
			}
			if len(got) != 4 {
				t.Fatalf("the migrated account reads %d identifier(s), want 4", len(got))
			}
			for _, id := range []string{"u-a", "e-a1", "e-a2", "p-a"} {
				if got[id].SignIn != want[id].signIn || got[id].Contact != want[id].contact ||
					got[id].DefaultContact != want[id].defaultContact {
					t.Errorf("identifier %q reads sign_in=%v contact=%v default_contact=%v, want %v %v %v",
						id, got[id].SignIn, got[id].Contact, got[id].DefaultContact,
						want[id].signIn, want[id].contact, want[id].defaultContact)
				}
			}

			if err := handle.Exec("UPDATE "+tableIdentifiers+" SET default_contact = ? WHERE id = ?", true, "e-a2").Error; !duplicate(err) {
				t.Errorf("a second default contact gave %v, want the unique constraint", err)
			}
		})
	}
}

// The contact migration makes every stored email and phone a contact, including rows written between migrations, and changes nothing else.
func TestTheRegisteredAddressContactMigrationBackfillsExistingRows(t *testing.T) {
	for _, dialect := range dialectsUnderTest {
		t.Run(string(dialect), func(t *testing.T) {
			handle := quietly(databaseOpeners[dialect](t).DB())
			applyMigrationFile(t, handle, dialect, "0001_authn_account.sql")
			applyMigrationFile(t, handle, dialect, "0002_authn_signup.sql")
			applyMigrationFile(t, handle, dialect, "0003_contact_claim.sql")
			at := newTestClock().read()
			for _, id := range []string{"account-1", "account-2"} {
				if err := handle.Exec("INSERT INTO "+tableAccounts+" (id, kind, status, signup_state, display_name, attributes, "+
					"mfa_required, protected, valid_after, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
					id, string(authn.KindUser), statusActive, signupNone, "", "{}", false, false, at, at, at).Error; err != nil {
					t.Fatalf("writing account %s: %v", id, err)
				}
			}
			insert := func(account, id, kind, value, via string, provenAt time.Time) {
				t.Helper()
				if err := handle.Exec("INSERT INTO "+tableIdentifiers+" (id, account_id, kind, value, verified_at, verified_via, created_at) "+
					"VALUES (?, ?, ?, ?, ?, ?, ?)", id, account, kind, value, provenAt, via, at).Error; err != nil {
					t.Fatalf("writing identifier %s: %v", id, err)
				}
			}
			insert("account-1", "u-old", string(authn.IdentifierUsername), "someone", authn.VerifiedViaInvite, at)
			insert("account-1", "e-old", string(authn.IdentifierEmail), "old@example.com", authn.VerifiedViaEmailCode, at)
			insert("account-1", "p-old", string(authn.IdentifierPhone), "+12025550123", authn.VerifiedViaSMSCode, at)

			applyMigrationFile(t, handle, dialect, "0004_identifier_axes.sql")

			insert("account-1", "e-new", string(authn.IdentifierEmail), "new@example.com", authn.VerifiedViaEmailCode, at)
			insert("account-2", "p-new", string(authn.IdentifierPhone), "+12025550124", authn.VerifiedViaSMSCode, at)

			applyMigrationFile(t, handle, dialect, "0005_registered_address_contact.sql")

			var rows []struct {
				ID             string
				SignIn         bool
				Contact        bool
				DefaultContact bool
			}
			if err := handle.Raw("SELECT id, sign_in, contact, default_contact FROM " + tableIdentifiers).Scan(&rows).Error; err != nil {
				t.Fatalf("reading the backfilled rows: %v", err)
			}
			want := map[string]struct {
				signIn, contact, defaultContact bool
			}{
				"u-old": {true, false, false},
				"e-old": {true, true, true},
				"p-old": {true, true, false},
				"e-new": {false, true, false},
				"p-new": {false, true, false},
			}
			if len(rows) != len(want) {
				t.Fatalf("the backfill left %d row(s), want %d", len(rows), len(want))
			}
			for _, row := range rows {
				expect, ok := want[row.ID]
				if !ok {
					t.Errorf("the backfill kept an unexpected row %q", row.ID)
					continue
				}
				if row.SignIn != expect.signIn || row.Contact != expect.contact || row.DefaultContact != expect.defaultContact {
					t.Errorf("row %q reads sign_in=%v contact=%v default_contact=%v, want %v %v %v",
						row.ID, row.SignIn, row.Contact, row.DefaultContact,
						expect.signIn, expect.contact, expect.defaultContact)
				}
			}
		})
	}
}

// Every path that opens a user account names a username, and one that does not creates neither the account nor any identifier.
func TestCreateRequiresAUsernameWhereItOpensAnAccount(t *testing.T) {
	for name, in := range map[string]authn.CreateAccount{
		"an administrator": {
			Email: "a@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode},
			Via: "admin",
		},
		"the initial assembly": {
			Email: "b@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode},
		},
		"a first login": {
			Phone: "+8613800000001", PhoneVerifiedVia: authn.Verification{Via: authn.VerifiedViaSMSCode},
			Via: "signup",
		},
		"an accepted invitation": {
			Email: "c@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaInvite},
			Via: "admin",
		},
	} {
		t.Run(name, func(t *testing.T) {
			forEachDialect(t, func(t *testing.T, h *harness) {
				_, err := h.p.Create(context.Background(), in)
				if !errors.Is(err, authn.ErrInvalidIdentifier) {
					t.Fatalf("%s with no username gave %v, want ErrInvalidIdentifier", name, err)
				}
				if n := h.count(tableAccounts, ""); n != 0 {
					t.Errorf("%s with no username left %d account row(s)", name, n)
				}
				if n := h.count(tableIdentifiers, ""); n != 0 {
					t.Errorf("%s with no username left %d identifier row(s)", name, n)
				}
			})
		})
	}
}

// An import names a username like every other creation, while a service account is exempt.
func TestCreateRequiresAUsernameOfAnImportAndExemptsAServiceAccount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		if _, err := h.p.Create(ctx, authn.CreateAccount{
			ID: "legacy_import", Kind: authn.KindUser, Via: "import",
			Email: "restored@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaImportUnverified},
			EmailFlags: authn.IdentifierFlags{Contact: true},
		}); !errors.Is(err, authn.ErrInvalidIdentifier) {
			t.Errorf("an import with no username gave %v, want ErrInvalidIdentifier", err)
		}
		if n := h.count(tableAccounts, ""); n != 0 {
			t.Errorf("the refused import left %d account row(s)", n)
		}

		if _, err := h.p.Create(ctx, authn.CreateAccount{Kind: authn.KindService, Via: "admin"}); err != nil {
			t.Fatalf("a service account with no username gave %v, want an account", err)
		}
		if _, err := h.p.Create(ctx, authn.CreateAccount{
			Kind: authn.KindService, Username: "admin", Via: "admin",
		}); !errors.Is(err, authn.ErrUsernameReserved) {
			t.Errorf("a service account with a reserved username gave %v, want ErrUsernameReserved", err)
		}
		if _, err := h.p.Create(ctx, authn.CreateAccount{
			Kind: authn.KindService, Username: "no", Via: "admin",
		}); !errors.Is(err, authn.ErrInvalidIdentifier) {
			t.Errorf("a service account with a malformed username gave %v, want ErrInvalidIdentifier", err)
		}
	})
}

// Create writes each identifier's axes: a username is a login way only, a proved sole email is contact and login way and the default, and a proved phone is both.
func TestCreateWritesTheAxesOfTheIdentifiersItCreates(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		created, err := h.p.Create(ctx, authn.CreateAccount{
			Username: "alice", Via: "admin",
			Email: "alice@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode},
			Phone: "+8613812345678", PhoneVerifiedVia: authn.Verification{Via: authn.VerifiedViaSMSCode},
		})
		if err != nil {
			t.Fatalf("creating the account: %v", err)
		}
		identifiers, err := h.p.Identifiers(ctx, created.ID)
		if err != nil {
			t.Fatalf("listing the identifiers: %v", err)
		}
		byKind := map[authn.IdentifierKind]authn.Identifier{}
		for _, identifier := range identifiers {
			byKind[identifier.Kind] = identifier
		}
		for _, c := range []struct {
			kind                            authn.IdentifierKind
			signIn, contact, defaultContact bool
		}{
			{authn.IdentifierUsername, true, false, false},
			{authn.IdentifierEmail, true, true, true},
			{authn.IdentifierPhone, true, true, false},
		} {
			got := byKind[c.kind]
			if got.SignIn != c.signIn || got.Contact != c.contact || got.DefaultContact != c.defaultContact {
				t.Errorf("the %s row reads sign_in=%v contact=%v default_contact=%v, want %v %v %v",
					c.kind, got.SignIn, got.Contact, got.DefaultContact, c.signIn, c.contact, c.defaultContact)
			}
		}
	})
}

// An email only asserted, not proved, is a contact but neither a login way nor the default contact.
func TestCreateDoesNotMakeAnUnprovedEmailTheDefaultContact(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		created, err := h.p.Create(ctx, authn.CreateAccount{
			Username: "bob", Via: "admin",
			Email: "bob@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin},
		})
		if err != nil {
			t.Fatalf("creating the account: %v", err)
		}
		identifiers, err := h.p.Identifiers(ctx, created.ID)
		if err != nil {
			t.Fatalf("listing the identifiers: %v", err)
		}
		for _, identifier := range identifiers {
			if identifier.Kind != authn.IdentifierEmail {
				continue
			}
			if identifier.SignIn || !identifier.Contact || identifier.DefaultContact {
				t.Errorf("the unproved email reads sign_in=%v contact=%v default_contact=%v, want false true false",
					identifier.SignIn, identifier.Contact, identifier.DefaultContact)
			}
		}
	})
}

// An import restores the sign-in axis from its source record, and a proved email the source did not name as default becomes it while an unproved one never does.
func TestAnImportWritesTheSignInAxisOfItsSourceRecord(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		read := func(in authn.CreateAccount) map[authn.IdentifierKind]authn.Identifier {
			t.Helper()
			created, err := h.p.Create(ctx, in)
			if err != nil {
				t.Fatalf("importing %+v: %v", in, err)
			}
			identifiers, listErr := h.p.Identifiers(ctx, created.ID)
			if listErr != nil {
				t.Fatalf("listing the imported account's identifiers: %v", listErr)
			}
			byKind := map[authn.IdentifierKind]authn.Identifier{}
			for _, identifier := range identifiers {
				byKind[identifier.Kind] = identifier
			}
			return byKind
		}

		proved := read(authn.CreateAccount{
			ID: "legacy_proved", Username: "legacy-proved", Via: "import",
			Email: "proved@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode},
			EmailFlags: authn.IdentifierFlags{SignIn: true, Contact: true},
		})
		if email := proved[authn.IdentifierEmail]; !email.SignIn || !email.Contact || !email.DefaultContact {
			t.Errorf("the imported proved email reads sign_in=%v contact=%v default_contact=%v, want all true",
				email.SignIn, email.Contact, email.DefaultContact)
		}

		noContact := read(authn.CreateAccount{
			ID: "legacy_no_contact", Username: "legacy-no-contact", Via: "import",
			Email: "nocontact@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode},
			EmailFlags: authn.IdentifierFlags{SignIn: true},
		})
		if email := noContact[authn.IdentifierEmail]; !email.SignIn || !email.Contact {
			t.Errorf("the imported email whose source named no contact reads sign_in=%v contact=%v, want both true",
				email.SignIn, email.Contact)
		}

		unproved := read(authn.CreateAccount{
			ID: "legacy_unproved", Username: "legacy-unproved", Via: "import",
			Email: "unproved@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaImportUnverified},
			EmailFlags: authn.IdentifierFlags{Contact: true}, DefaultContact: true,
		})
		if email := unproved[authn.IdentifierEmail]; email.SignIn || !email.Contact || email.DefaultContact {
			t.Errorf("the imported unproved email reads sign_in=%v contact=%v default_contact=%v, want false true false",
				email.SignIn, email.Contact, email.DefaultContact)
		}

		phone := read(authn.CreateAccount{
			ID: "legacy_phone", Username: "legacy-phone", Via: "import",
			Phone: "+8613800000002", PhoneVerifiedVia: authn.Verification{Via: authn.VerifiedViaImportUnverified},
			PhoneFlags: authn.IdentifierFlags{},
		})
		if p := phone[authn.IdentifierPhone]; p.SignIn || !p.Contact {
			t.Errorf("the imported phone reads sign_in=%v contact=%v, want false true", p.SignIn, p.Contact)
		}
	})
}

// Self-registration writes the username as a login way.
func TestRegisterWritesItsUsernameAsALoginWay(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		h.openSelfRegistration()
		account := h.mustRegister("carol", "carol@example.com", "correct horse battery")
		identifiers, err := h.p.Identifiers(context.Background(), account.ID)
		if err != nil {
			t.Fatalf("listing the identifiers: %v", err)
		}
		var found bool
		for _, identifier := range identifiers {
			if identifier.Kind != authn.IdentifierUsername {
				continue
			}
			found = true
			if !identifier.SignIn || identifier.Contact || identifier.DefaultContact {
				t.Errorf("the registered username reads sign_in=%v contact=%v default_contact=%v, want true false false",
					identifier.SignIn, identifier.Contact, identifier.DefaultContact)
			}
		}
		if !found {
			t.Errorf("the registered account holds no username identifier: %+v", identifiers)
		}
	})
}
