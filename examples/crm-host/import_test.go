package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	nethttp "net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/static"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/tenant"
)

// forImportStacks runs a case on the stack that needs nothing installed and on a
// stack whose database is a different engine, since the import reads the old
// tables and writes the new ones through DDL, booleans and timestamps that the
// two engines handle differently.
func forImportStacks(t *testing.T, body func(t *testing.T, c *crmCase)) {
	t.Helper()
	for _, name := range []string{localStack, "postgres"} {
		s := stackNamed(t, name)
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			body(t, newCase(t, s))
		})
	}
}

// seedLegacySchema builds the users and memberships tables the previous
// migration set left behind, which the current set does not create. The host
// reads them by hand and writes them nowhere.
func seedLegacySchema(t *testing.T, c *crmCase) {
	t.Helper()
	c.exec(`CREATE TABLE users (
		id            TEXT      NOT NULL PRIMARY KEY,
		email         TEXT      NOT NULL UNIQUE,
		display_name  TEXT      NOT NULL,
		password_hash TEXT      NOT NULL,
		disabled      BOOLEAN   NOT NULL DEFAULT FALSE,
		created_at    TIMESTAMP NOT NULL
	)`)
	c.exec(`CREATE TABLE memberships (
		tenant_id  TEXT      NOT NULL,
		user_id    TEXT      NOT NULL,
		role       TEXT      NOT NULL,
		granted_at TIMESTAMP NOT NULL,
		PRIMARY KEY (tenant_id, user_id)
	)`)
}

// insertLegacyUser writes one row of the old users table.
func (c *crmCase) insertLegacyUser(id, email, name, entry string, disabled bool) {
	c.exec("INSERT INTO users (id, email, display_name, password_hash, disabled, created_at) "+
		"VALUES (?, ?, ?, ?, ?, ?)", id, email, name, entry, disabled, time.Now().UTC())
}

// insertLegacyMembership writes one row of the old memberships table.
func (c *crmCase) insertLegacyMembership(tenantID, userID, role string) {
	c.exec("INSERT INTO memberships (tenant_id, user_id, role, granted_at) VALUES (?, ?, ?, ?)",
		tenantID, userID, role, time.Now().UTC())
}

// legacyPasswordEntry builds a password entry in the envelope the previous host
// wrote: the scheme, the iteration count, and the raw-base64 salt and key, with
// no leading dollar sign. The account module reads it as a legacy entry and
// converts it to the PHC form without loss.
func legacyPasswordEntry(t *testing.T, password string, iterations int) string {
	t.Helper()
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		t.Fatalf("drawing a legacy salt: %v", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		t.Fatalf("deriving a legacy entry: %v", err)
	}
	return "pbkdf2-sha256$" + strconv.Itoa(iterations) +
		"$" + base64.RawStdEncoding.EncodeToString(salt) +
		"$" + base64.RawStdEncoding.EncodeToString(key)
}

// intFrom reads one integer from the case's database.
func (c *crmCase) intFrom(query string, args ...any) int {
	c.t.Helper()
	var n int
	if err := c.sql().QueryRow(c.rebind(query), args...).Scan(&n); err != nil {
		c.t.Fatalf("reading %q: %v", query, err)
	}
	return n
}

// text reads one string from the case's database. A NULL reads as the empty
// string.
func (c *crmCase) text(query string, args ...any) string {
	c.t.Helper()
	var s string
	if err := c.sql().QueryRow(c.rebind(query), args...).Scan(&s); err != nil {
		c.t.Fatalf("reading %q: %v", query, err)
	}
	return s
}

// TestTheImportCarriesTheOldUsersAndMemberships is the case for the one-shot
// import: the old users and memberships become accounts, memberships and roles.
// The identifier is carried over, the address is written as an unverified import
// assertion that is no proof of ownership, a switched-off user stays switched
// off, and a user is signed in with the password the old system held.
//
// The dry run comes first, over a database the import has not touched: it must
// report the same rows without writing any of them. Then the real run, then a
// second real run, which must create nothing and leave the state as it was.
func TestTheImportCarriesTheOldUsersAndMemberships(t *testing.T) {
	forImportStacks(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		globex := c.createTenant("globex")
		seedLegacySchema(t, c)

		const (
			aliceID = "u_11111111111111111111111111111111"
			bobID   = "u_22222222222222222222222222222222"
			carolID = "u_33333333333333333333333333333333"
		)
		aliceEmail := "alice@" + c.suffix + ".crm.test"
		bobEmail := "bob@" + c.suffix + ".crm.test"
		carolEmail := "carol@" + c.suffix + ".crm.test"
		alicePassword := "alice password " + c.suffix
		bobPassword := "bob password " + c.suffix

		aliceEntry := legacyPasswordEntry(t, alicePassword, 1000)
		bobEntry := legacyPasswordEntry(t, bobPassword, 2000)
		c.insertLegacyUser(aliceID, aliceEmail, "Alice", aliceEntry, false)
		c.insertLegacyUser(bobID, bobEmail, "Bob", bobEntry, true)
		// Carol's stored entry is not one the account module can read.
		c.insertLegacyUser(carolID, carolEmail, "Carol", "pbkdf2-sha256$notanumber$AAAA$BBBB", false)

		c.insertLegacyMembership(acme.ID, aliceID, roleOwner)
		c.insertLegacyMembership(globex.ID, aliceID, roleRep)
		c.insertLegacyMembership(acme.ID, bobID, roleAdmin)
		// A membership whose tenant nobody registered.
		c.insertLegacyMembership("t_gone_"+c.suffix, aliceID, roleViewer)
		// A membership naming the user whose entry could not be read.
		c.insertLegacyMembership(acme.ID, carolID, roleViewer)

		// The dry run decides from reads alone, so it counts the two rows whose
		// only fault the write reveals — Carol's unreadable entry, and the
		// membership naming the account it never created — as creations it
		// would attempt. Its created count is therefore an upper bound the real
		// run narrows; what it must never do is write.
		var dry dataImportReport
		c.resultOf(c.mustRun("", "import", "run", "--dry-run"), &dry)
		if !dry.DryRun {
			t.Fatalf("the dry run answered dry_run %v, want true", dry.DryRun)
		}
		if dry.Created != 7 || dry.Skipped != 0 || dry.Failed != 1 {
			t.Errorf("the dry run answered created %d skipped %d failed %d, want 7, 0, 1",
				dry.Created, dry.Skipped, dry.Failed)
		}
		if got := c.intFrom("SELECT COUNT(*) FROM authn_accounts"); got != 0 {
			t.Errorf("the dry run wrote %d accounts, want none", got)
		}
		if got := c.intFrom("SELECT COUNT(*) FROM authn_memberships"); got != 0 {
			t.Errorf("the dry run wrote %d memberships, want none", got)
		}
		if got := c.intFrom("SELECT COUNT(*) FROM audit_entries WHERE action = 'account.import'"); got != 0 {
			t.Errorf("the dry run wrote %d import rows to the journal, want none", got)
		}

		res := c.mustRun("", "import", "run")
		var rep dataImportReport
		c.resultOf(res, &rep)
		if rep.DryRun {
			t.Errorf("the real run answered dry_run true")
		}
		if rep.Created != 5 || rep.Skipped != 0 || rep.Failed != 3 {
			t.Errorf("the import answered created %d skipped %d failed %d, want 5, 0, 3",
				rep.Created, rep.Skipped, rep.Failed)
		}
		for _, secret := range []string{aliceEmail, bobEmail, carolEmail, alicePassword, bobPassword, aliceEntry, bobEntry} {
			if strings.Contains(res.combined(), secret) {
				t.Errorf("the import's output carries %q, which must never leave the database", secret)
			}
		}
		if got := c.intFrom("SELECT COUNT(*) FROM audit_entries WHERE action = 'account.import'"); got != 1 {
			t.Errorf("the import wrote %d journal rows, want one", got)
		}
		detail := c.text("SELECT detail FROM audit_entries WHERE action = 'account.import'")
		for _, secret := range []string{aliceEmail, bobEmail, carolEmail, aliceEntry, bobEntry} {
			if strings.Contains(detail, secret) {
				t.Errorf("the import's journal row carries %q, which must never leave the database", secret)
			}
		}

		// The identifiers are carried over, so the host's own rows still name them.
		if got := c.intFrom("SELECT COUNT(*) FROM authn_accounts WHERE id = ? AND kind = 'user'", aliceID); got != 1 {
			t.Errorf("the old identifier %s is not the account identifier", aliceID)
		}
		if got := c.text("SELECT display_name FROM authn_accounts WHERE id = ?", aliceID); got != "Alice" {
			t.Errorf("the account's display name is %q, want Alice", got)
		}
		if got := c.intFrom("SELECT status FROM authn_accounts WHERE id = ?", aliceID); got != 1 {
			t.Errorf("Alice's account status is %d, want active", got)
		}
		if got := c.intFrom("SELECT status FROM authn_accounts WHERE id = ?", bobID); got != 0 {
			t.Errorf("the switched-off user's account status is %d, want disabled", got)
		}

		// The address is an unverified import assertion, and no proof of ownership.
		verifiedVia := c.text("SELECT verified_via FROM authn_identifiers WHERE account_id = ? AND kind = 'email'", aliceID)
		if verifiedVia != authn.VerifiedViaImportUnverified {
			t.Errorf("the imported address is verified via %q, want %q", verifiedVia, authn.VerifiedViaImportUnverified)
		}
		if (authn.Verification{Via: verifiedVia}).Proves() {
			t.Errorf("the imported address reads as a proof of ownership, so a social login or an enterprise merge could act on it")
		}
		if !(authn.Verification{Via: authn.VerifiedViaIDPPrefix + "google"}).Proves() {
			t.Errorf("an idp assertion does not read as a proof, so the check above distinguishes nothing")
		}
		if got := c.text("SELECT verified_via FROM authn_identifiers WHERE account_id = ? AND kind = 'email'", bobID); got != authn.VerifiedViaImportUnverified {
			t.Errorf("the imported address of the switched-off user is verified via %q, want %q", got, authn.VerifiedViaImportUnverified)
		}

		// The entry was converted without loss: the same iteration count, in PHC.
		importedPhc := c.text("SELECT phc FROM authn_password_credentials WHERE account_id = ?", aliceID)
		if !strings.HasPrefix(importedPhc, "$pbkdf2-sha256$i=1000,") {
			t.Errorf("the imported entry is %q, want the legacy entry's count in the PHC form", importedPhc)
		}

		// Memberships and roles came over, one for one.
		if got := c.intFrom("SELECT COUNT(*) FROM authn_memberships"); got != 3 {
			t.Errorf("the import wrote %d memberships, want 3", got)
		}
		if got := c.intFrom("SELECT COUNT(*) FROM member_roles"); got != 3 {
			t.Errorf("the import wrote %d roles, want one per membership", got)
		}
		if got := c.text("SELECT role FROM member_roles WHERE tenant_id = ? AND account_id = ?", acme.ID, aliceID); got != roleOwner {
			t.Errorf("Alice's role in acme is %q, want %q", got, roleOwner)
		}
		if got := c.text("SELECT role FROM member_roles WHERE tenant_id = ? AND account_id = ?", globex.ID, aliceID); got != roleRep {
			t.Errorf("Alice's role in globex is %q, want %q", got, roleRep)
		}
		if got := c.text("SELECT role FROM member_roles WHERE tenant_id = ? AND account_id = ?", acme.ID, bobID); got != roleAdmin {
			t.Errorf("Bob's role in acme is %q, want %q", got, roleAdmin)
		}
		if got := c.text("SELECT source FROM authn_memberships WHERE tenant_id = ? AND account_id = ?", acme.ID, aliceID); got != importVia {
			t.Errorf("the imported membership's source is %q, want %q", got, importVia)
		}

		// A second run creates nothing and leaves the state as it was.
		accountsBefore := c.intFrom("SELECT COUNT(*) FROM authn_accounts")
		membershipsBefore := c.intFrom("SELECT COUNT(*) FROM authn_memberships")
		rolesBefore := c.intFrom("SELECT COUNT(*) FROM member_roles")
		var again dataImportReport
		c.resultOf(c.mustRun("", "import", "run"), &again)
		if again.Created != 0 || again.Skipped != 5 || again.Failed != 3 {
			t.Errorf("the second run answered created %d skipped %d failed %d, want 0, 5, 3",
				again.Created, again.Skipped, again.Failed)
		}
		if got := c.intFrom("SELECT COUNT(*) FROM authn_accounts"); got != accountsBefore {
			t.Errorf("the second run changed the account count from %d to %d", accountsBefore, got)
		}
		if got := c.intFrom("SELECT COUNT(*) FROM authn_memberships"); got != membershipsBefore {
			t.Errorf("the second run changed the membership count from %d to %d", membershipsBefore, got)
		}
		if got := c.intFrom("SELECT COUNT(*) FROM member_roles"); got != rolesBefore {
			t.Errorf("the second run changed the role count from %d to %d", rolesBefore, got)
		}

		h := c.serve()
		defer h.stop(t)

		// Alice signs in with the password the old system held.
		cookie := h.login(t, userRef{Email: aliceEmail, Password: alicePassword})
		expect(t, "Alice in acme after the import",
			h.get(t, apiEndpoint, "/members", acme.Alias, bearer(cookie)), nethttp.StatusOK)

		// The successful check re-hashed the entry to the current parameters.
		if !eventually(settleLimit, func() bool {
			return strings.HasPrefix(c.text("SELECT phc FROM authn_password_credentials WHERE account_id = ?", aliceID), "$argon2id$")
		}) {
			t.Errorf("the entry after a successful sign-in is %q, want it re-hashed to argon2id",
				c.text("SELECT phc FROM authn_password_credentials WHERE account_id = ?", aliceID))
		}

		// The switched-off user cannot sign in.
		refused, err := h.do(request{
			method: nethttp.MethodPost, endpoint: accountEndpoint, path: loginFlowPath,
			body: map[string]any{
				"method": "password", "delivery": "session",
				"input": map[string]string{"identifier": bobEmail, "password": bobPassword},
			},
		})
		if err != nil {
			t.Fatalf("starting a login for the switched-off user: %v", err)
		}
		if refused.status == nethttp.StatusOK {
			t.Errorf("the switched-off user was signed in; body %q", refused.body)
		}
		if got := c.intFrom("SELECT status FROM authn_accounts WHERE id = ?", bobID); got != 0 {
			t.Errorf("the switched-off user's status after a sign-in attempt is %d, want disabled", got)
		}
	})
}

// TestTheImportRefusesRowsItCannotCarryAndGoesOn is the case for the per-row
// failures of the import: a row it cannot carry is counted and reported, and the
// rows around it are still imported. It also holds the address a different
// account already owns, which the access store's uniqueness constraint refuses.
func TestTheImportRefusesRowsItCannotCarryAndGoesOn(t *testing.T) {
	forImportStacks(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		seedLegacySchema(t, c)

		goodEmail := "good@" + c.suffix + ".crm.test"
		takenEmail := "taken@" + c.suffix + ".crm.test"
		takenPassword := "taken password " + c.suffix

		// An account already owns the address a legacy row will name.
		takenEntry, err := static.HashPBKDF2(takenPassword, casePBKDF2Iterations)
		if err != nil {
			t.Fatalf("deriving the taken address's entry: %v", err)
		}
		c.mustRun(takenEntry+"\n", "user", "create", "--email", takenEmail, "--display-name", "Taken", "--password-hash")

		const goodID = "u_44444444444444444444444444444444"
		c.insertLegacyUser(goodID, goodEmail, "Good", legacyPasswordEntry(t, "good password", 1000), false)
		// An identifier that begins with the static prefix.
		c.insertLegacyUser(authn.StaticPrefix+"ghost", "ghost@"+c.suffix+".crm.test", "Ghost",
			legacyPasswordEntry(t, "ghost password", 1000), false)
		// An identifier that is not an account identifier.
		c.insertLegacyUser("bad id!", "bad@"+c.suffix+".crm.test", "Bad",
			legacyPasswordEntry(t, "bad password", 1000), false)
		// An entry that is out of bounds: the iteration count is above the bound.
		c.insertLegacyUser("u_55555555555555555555555555555555", "over@"+c.suffix+".crm.test", "Over",
			"pbkdf2-sha256$99999999$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", false)
		// A legacy row whose address a different account already owns.
		c.insertLegacyUser("u_66666666666666666666666666666666", takenEmail, "Taken",
			legacyPasswordEntry(t, "taken legacy password", 1000), false)

		c.insertLegacyMembership(acme.ID, goodID, roleViewer)
		// A membership for a tenant nobody registered.
		c.insertLegacyMembership("t_gone_"+c.suffix, goodID, roleViewer)
		// A membership whose role is none of the four.
		c.insertLegacyMembership(acme.ID, "u_77777777777777777777777777777777", "superuser")
		// A membership naming a user the import could not carry.
		c.insertLegacyMembership(acme.ID, "u_55555555555555555555555555555555", roleViewer)

		res := c.mustRun("", "import", "run")
		var rep dataImportReport
		c.resultOf(res, &rep)

		if rep.Created != 2 {
			t.Errorf("the import created %d, want the good user and its one good membership", rep.Created)
		}
		wantReasons := map[string]int{
			"identifier-static":               1,
			"identifier-not-an-account-id":    1,
			"account-not-created":             1,
			"address-held-by-another-account": 1,
			"tenant-not-in-roster":            1,
			"role-unknown":                    1,
			"account-missing":                 1,
		}
		for reason, want := range wantReasons {
			if got := rep.Reasons[reason]; got != want {
				t.Errorf("the import reported reason %q %d times, want %d; reasons %v", reason, got, want, rep.Reasons)
			}
		}
		if rep.Failed != 7 {
			t.Errorf("the import failed %d rows, want 7; reasons %v", rep.Failed, rep.Reasons)
		}
		for _, secret := range []string{goodEmail, takenEmail, "ghost@", "bad@", "over@"} {
			if strings.Contains(res.combined(), secret) {
				t.Errorf("the import's output carries %q, which must never leave the database", secret)
			}
		}

		// The rows around the failures were still imported.
		if got := c.intFrom("SELECT COUNT(*) FROM authn_accounts WHERE id = ?", goodID); got != 1 {
			t.Errorf("the good user was not imported")
		}
		if got := c.text("SELECT role FROM member_roles WHERE tenant_id = ? AND account_id = ?", acme.ID, goodID); got != roleViewer {
			t.Errorf("the good member's role is %q, want %q", got, roleViewer)
		}
		if got := c.intFrom("SELECT COUNT(*) FROM authn_accounts WHERE id = ?", authn.StaticPrefix+"ghost"); got != 0 {
			t.Errorf("an account was written under the static prefix")
		}
	})
}

// TestTheImportCommandRunsUnderTheHostsCommandGroup is the case for the shape
// the import command is registered with: it is one leaf of the host's own
// command-group mechanism, it drives to Start because its write is recorded
// through the audit seam, and its module names the role table it writes and the
// roster that scopes it, so a command that took it in without either fails at
// startup rather than writing an empty tenant.
func TestTheImportCommandRunsUnderTheHostsCommandGroup(t *testing.T) {
	if dataImportGroup.word != "import" || len(dataImportGroup.commands) != 1 {
		t.Fatalf("the import group is %q with %d commands, want import with one", dataImportGroup.word, len(dataImportGroup.commands))
	}
	if dataImportGroup.commands[0].depth != core.StageStart {
		t.Errorf("the import command drives to %v, want Start: its write is recorded through the audit seam",
			dataImportGroup.commands[0].depth)
	}

	var found *application
	for i := range applications {
		if applications[i].module.Name == dataImportModuleName {
			found = &applications[i]
		}
	}
	if found == nil {
		t.Fatalf("no application is registered under %q", dataImportModuleName)
	}
	if !contains(found.runsOn, dataImportGroup.word) {
		t.Errorf("%q runs on %v, which does not name the import group", dataImportModuleName, found.runsOn)
	}
	if !contains(modulesOn(dataImportGroup.word), dataImportModuleName) {
		t.Errorf("the command tree does not take %q into the import group", dataImportModuleName)
	}
	if !requiresCapability(found.module, (*authn.Memberships)(nil)) {
		t.Errorf("%q does not require the membership store", dataImportModuleName)
	}
	if !requiresCapability(found.module, (*tenant.Catalog)(nil)) {
		t.Errorf("%q does not require the roster, which it reads to refuse a tenant nobody registered", dataImportModuleName)
	}
	scoped := false
	for _, model := range found.models {
		if _, ok := model.(MemberRole); ok {
			scoped = true
		}
	}
	if !scoped {
		t.Errorf("%q does not list the role table it writes among its models", dataImportModuleName)
	}
}

// contains reports whether a string slice holds a value.
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// TestTheImportLeavesAChangedRoleAsItIs is the case for a re-run over a
// membership whose role was raised after the import: the role the tenant's own
// table holds is left as it is and the row is counted skipped, so a repeated
// import never reverts an administrator's change behind their back.
func TestTheImportLeavesAChangedRoleAsItIs(t *testing.T) {
	forImportStacks(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		seedLegacySchema(t, c)
		const aliceID = "u_11111111111111111111111111111111"
		c.insertLegacyUser(aliceID, "alice@"+c.suffix+".crm.test", "Alice",
			legacyPasswordEntry(t, "alice password "+c.suffix, 1000), false)
		c.insertLegacyMembership(acme.ID, aliceID, roleViewer)

		var first dataImportReport
		c.resultOf(c.mustRun("", "import", "run"), &first)
		if first.Created != 2 {
			t.Fatalf("the first import created %d, want the user and its membership", first.Created)
		}
		c.exec("UPDATE member_roles SET role = ? WHERE tenant_id = ? AND account_id = ?", roleOwner, acme.ID, aliceID)

		var again dataImportReport
		c.resultOf(c.mustRun("", "import", "run"), &again)
		if again.Created != 0 || again.Resumed != 0 || again.Failed != 0 {
			t.Errorf("the second run answered created %d resumed %d failed %d, want none",
				again.Created, again.Resumed, again.Failed)
		}
		if got := c.text("SELECT role FROM member_roles WHERE tenant_id = ? AND account_id = ?", acme.ID, aliceID); got != roleOwner {
			t.Errorf("the second import changed the role to %q, want the out-of-band change %q left as it is", got, roleOwner)
		}
	})
}

// TestTheImportLeavesAReEnabledAccountEnabled is the case for a re-run over an
// account an administrator re-enabled after the import: the status is left as it
// is, so a repeated import never switches an account off behind the
// administrator's back.
func TestTheImportLeavesAReEnabledAccountEnabled(t *testing.T) {
	forImportStacks(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		seedLegacySchema(t, c)
		const bobID = "u_22222222222222222222222222222222"
		c.insertLegacyUser(bobID, "bob@"+c.suffix+".crm.test", "Bob",
			legacyPasswordEntry(t, "bob password "+c.suffix, 1000), true)

		c.mustRun("", "import", "run")
		if got := c.intFrom("SELECT status FROM authn_accounts WHERE id = ?", bobID); got != int(authn.StatusDisabled) {
			t.Fatalf("the imported switched-off user's status is %d, want disabled", got)
		}
		c.exec("UPDATE authn_accounts SET status = ? WHERE id = ?", int(authn.StatusActive), bobID)

		c.mustRun("", "import", "run")
		if got := c.intFrom("SELECT status FROM authn_accounts WHERE id = ?", bobID); got != int(authn.StatusActive) {
			t.Errorf("the second import set the status to %d, want the re-enabled account left active", got)
		}
	})
}

// TestTheImportFinishesAHalfWrittenMembership is the case for the one row a
// re-run finishes: a membership an earlier run wrote and then failed before
// writing its role. The next run writes the missing role and counts the row
// resumed, and the dry run over the same state answers the same outcome without
// writing. A role row that is present is never overwritten, so this is the only
// write a re-run makes.
func TestTheImportFinishesAHalfWrittenMembership(t *testing.T) {
	forImportStacks(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		seedLegacySchema(t, c)
		const aliceID = "u_11111111111111111111111111111111"
		c.insertLegacyUser(aliceID, "alice@"+c.suffix+".crm.test", "Alice",
			legacyPasswordEntry(t, "alice password "+c.suffix, 1000), false)
		c.insertLegacyMembership(acme.ID, aliceID, roleRep)

		c.mustRun("", "import", "run")
		// An earlier run wrote the membership and died before writing its role.
		c.exec("DELETE FROM member_roles WHERE tenant_id = ? AND account_id = ?", acme.ID, aliceID)

		var dry dataImportReport
		c.resultOf(c.mustRun("", "import", "run", "--dry-run"), &dry)
		if dry.Resumed != 1 || dry.Skipped != 1 || dry.Created != 0 || dry.Failed != 0 {
			t.Errorf("the dry run answered created %d resumed %d skipped %d failed %d, want the 1 resumed and 1 skipped the real run would",
				dry.Created, dry.Resumed, dry.Skipped, dry.Failed)
		}
		if got := c.count("SELECT count(*) FROM member_roles WHERE tenant_id = ? AND account_id = ?", acme.ID, aliceID); got != 0 {
			t.Errorf("the dry run wrote the role")
		}

		var again dataImportReport
		c.resultOf(c.mustRun("", "import", "run"), &again)
		if again.Resumed != 1 || again.Skipped != 1 || again.Created != 0 || again.Failed != 0 {
			t.Errorf("the resuming run answered created %d resumed %d skipped %d failed %d, want the 1 resumed (the membership) and 1 skipped (the account)",
				again.Created, again.Resumed, again.Skipped, again.Failed)
		}
		if got := c.text("SELECT role FROM member_roles WHERE tenant_id = ? AND account_id = ?", acme.ID, aliceID); got != roleRep {
			t.Errorf("the missing role was not written; the row holds %q, want %q", got, roleRep)
		}
	})
}

// TestTheImportNamesADeletedTargetForWhatItIs is the case for a legacy user
// whose account was deleted after an earlier run imported it. The account's row
// stays as a tombstone and its identifier still collides with the create; the
// run names that collision for what it is rather than as an address another
// account owns.
func TestTheImportNamesADeletedTargetForWhatItIs(t *testing.T) {
	forImportStacks(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		seedLegacySchema(t, c)
		const aliceID = "u_11111111111111111111111111111111"
		c.insertLegacyUser(aliceID, "alice@"+c.suffix+".crm.test", "Alice",
			legacyPasswordEntry(t, "alice password "+c.suffix, 1000), false)

		c.mustRun("", "import", "run")
		// The account is deleted after the import.
		c.exec("UPDATE authn_accounts SET status = ? WHERE id = ?", int(authn.StatusDeleted), aliceID)

		var again dataImportReport
		c.resultOf(c.mustRun("", "import", "run"), &again)
		if got := again.Reasons["identifier-tombstoned"]; got != 1 {
			t.Errorf("the re-run reported reason \"identifier-tombstoned\" %d times, want 1; reasons %v",
				got, again.Reasons)
		}
	})
}
