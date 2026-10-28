package account

import (
	"context"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// The identifier-axes migration adds the two axes and the default contact, and
// backfills a database written before them without changing any stored fact.
// Every identifier written under the old behaviour becomes a sign-in way, every
// email identifier among them is also a contact, and each account's earliest
// proved email address, ties broken by identifier, is its default contact. An
// address only asserted, not proved, never becomes the default.
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

			// A row written after the migration starts outside both axes.
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

			// The account read carries the backfilled flags out.
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

			// At most one default contact per account.
			if err := handle.Exec("UPDATE "+tableIdentifiers+" SET default_contact = ? WHERE id = ?", true, "e-a2").Error; !duplicate(err) {
				t.Errorf("a second default contact gave %v, want the unique constraint", err)
			}
		})
	}
}
