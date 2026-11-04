//go:build review

package main

// The review seed's account fixtures, held to the product's own seams.
//
// A seed entry marked `proved-email` has its address proved the way a completed
// email-code flow proves it, and an entry under `system-admins` becomes an
// active member of the system tenant while the platform-administrator role is
// not written for it. Both are read back through the real account module, so a
// renamed column or a path that only logged its intent fails here.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
)

// reviewSeedTenantAdmin is the account the configuration below marks
// proved-email; it is acme's only administrator.
const (
	reviewSeedTenantAdminAddress = "tenant-admin@demo.test"
	reviewSeedTenantAdminLogin   = "tenant-admin"
	reviewSeedSystemAdminLogin   = "system-admin"
)

const reviewSeedAccountSection = `seed:
  accounts:
    - identifier: "operator"
      password: "operator-dev-secret"
      display-name: "Demo Operator"
      platform-admin: true
    - identifier: "tenant-admin@demo.test"
      username: "tenant-admin"
      password: "tenant-admin-secret"
      display-name: "Tenant Administrator"
      proved-email: true
    - identifier: "system-admin"
      password: "system-admin-secret"
      display-name: "System Tenant Administrator"
  tenants:
    - name: acme
      admins:
        - tenant-admin@demo.test
  system-admins:
    - system-admin`

func TestReviewSeedProvesAFlaggedEmailAndSeedsASystemAdministrator(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "review-seed.db")
	config := writeConfigReplacing(t, freePorts(t, listenerCount), dsn,
		demoSeedSection, reviewSeedAccountSection)
	res := runCommandOn(t, seedHostBinary(), config, "sqlite", seedCommand)
	if res.err != nil {
		t.Fatalf("seeding the review account fixtures: %v\n%s", res.err, res.combined())
	}

	withSeededAccounts(t, dsn,
		func(ctx context.Context, accounts authn.Accounts, _ authn.AccountAdmin, memberships authn.Memberships) {
			tenantAdmin, err := accounts.Lookup(ctx, authn.IdentifierEmail, reviewSeedTenantAdminAddress)
			if err != nil {
				t.Fatalf("looking up the tenant administrator: %v", err)
			}
			held, err := accounts.Identifiers(ctx, tenantAdmin)
			if err != nil {
				t.Fatalf("reading the tenant administrator's identifiers: %v", err)
			}
			var proved bool
			for _, identifier := range held {
				if identifier.Kind == authn.IdentifierEmail &&
					identifier.Value == reviewSeedTenantAdminAddress &&
					(authn.Verification{Via: identifier.VerifiedVia}).Proves() {
					proved = true
				}
			}
			if !proved {
				t.Errorf("the tenant administrator's address is not proved: %+v", held)
			}

			acme, found, err := memberships.Membership(ctx, "acme", tenantAdmin)
			if err != nil {
				t.Fatalf("reading the tenant administrator's membership: %v", err)
			}
			if !found || !acme.Active {
				t.Errorf("the tenant administrator's acme membership = %+v, found %v; want an active row",
					acme, found)
			}

			systemAdmin, err := accounts.Lookup(ctx, authn.IdentifierUsername, reviewSeedSystemAdminLogin)
			if err != nil {
				t.Fatalf("looking up the system administrator: %v", err)
			}
			system, found, err := memberships.Membership(ctx, string(authz.PlatformDomain), systemAdmin)
			if err != nil {
				t.Fatalf("reading the system administrator's membership: %v", err)
			}
			if !found || !system.Active {
				t.Errorf("the system administrator's system membership = %+v, found %v; want an active row",
					system, found)
			}
		})
}

// TestReviewSeedRefusesAProvedEmailOnAUsername holds the fixture's own shape:
// the flag asks for the address named by the identifier to be proved, so an
// entry that names a username and marks it proved is a fixture error rather
// than a silent no-op.
func TestReviewSeedRefusesAProvedEmailOnAUsername(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "review-seed-bad.db")
	section := strings.Replace(reviewSeedAccountSection,
		"      display-name: \"System Tenant Administrator\"",
		"      display-name: \"System Tenant Administrator\"\n      proved-email: true", 1)
	config := writeConfigReplacing(t, freePorts(t, listenerCount), dsn,
		demoSeedSection, section)
	res := runCommandOn(t, seedHostBinary(), config, "sqlite", seedCommand)
	if res.err == nil {
		t.Fatalf("the seed accepted proved-email on a username:\n%s", res.combined())
	}
	if !strings.Contains(res.combined(), "proved-email") {
		t.Errorf("the refusal does not name the flag:\n%s", res.combined())
	}
}
