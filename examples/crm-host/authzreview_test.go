package main

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// This file holds the cases for the host's authorization migration surface that
// the module's own cases do not reach: the tenant role matrix against a literal
// expectation, the identity system's management surface the host's roles must
// not open, the admin and command guards, and the ones the one-shot role import
// runs on. Each is written so an implementation that dropped the guard or moved
// a permission is answered differently.

// expectedTenantPairs is the tenant role matrix written out literally, one
// (resource, action) pair per grant row, so the provisioner is held
// independently of tenantRoleGrants: a permission moved between tiers, dropped
// from one, or added to one is caught without the case reading the structure it
// verifies. The platform-domain carried rows the host shares are not here — a
// tenant role carries only the host's own business permissions and the carried
// rows the tiers name.
var expectedTenantPairs = map[authz.Role][]string{
	authzRoleViewer: {
		"crm-account\x00read", "contact\x00read", "contact\x00export",
		"deal\x00read", "pipeline\x00read", "activity\x00read",
		"flow\x00read", "report\x00read", "member\x00read",
	},
	authzRoleRep: {
		"crm-account\x00read", "crm-account\x00write", "crm-account\x00own",
		"contact\x00read", "contact\x00export", "contact\x00write",
		"deal\x00read", "deal\x00write", "deal\x00delete",
		"pipeline\x00read", "activity\x00read", "activity\x00write", "activity\x00delete",
		"flow\x00read", "report\x00read", "member\x00read",
	},
	authzRoleAdmin: {
		"crm-account\x00read", "crm-account\x00write", "crm-account\x00own", "crm-account\x00delete",
		"contact\x00read", "contact\x00export", "contact\x00write", "contact\x00delete",
		"deal\x00read", "deal\x00write", "deal\x00delete",
		"pipeline\x00read", "pipeline\x00write",
		"activity\x00read", "activity\x00write", "activity\x00delete",
		"flow\x00read", "report\x00read", "member\x00read", "member\x00manage",
		"settings\x00read", "settings\x00write", "audit\x00read",
	},
	authzRoleOwner: {
		"crm-account\x00read", "crm-account\x00write", "crm-account\x00own", "crm-account\x00delete", "crm-account\x00purge",
		"contact\x00read", "contact\x00export", "contact\x00write", "contact\x00delete",
		"deal\x00read", "deal\x00write", "deal\x00delete",
		"pipeline\x00read", "pipeline\x00write",
		"activity\x00read", "activity\x00write", "activity\x00delete",
		"flow\x00read", "report\x00read", "member\x00read", "member\x00manage",
		"settings\x00read", "settings\x00write", "audit\x00read",
		"impersonation\x00start", "impersonation\x00read", "impersonation\x00revoke",
	},
}

// TestTheTenantRoleMatrixCarriesExactlyTheLiteralTiers holds the persister's
// rows to the literal table above, so a tier's content is verified rather than
// compared against the constant it was written from.
func TestTheTenantRoleMatrixCarriesExactlyTheLiteralTiers(t *testing.T) {
	t.Parallel()
	const tenant = "t_acme"
	mgr := newAuthzFakeManager()
	if err := provisionTenantRoles(t.Context(), mgr, tenant); err != nil {
		t.Fatalf("provisioning the tenant roles: %v", err)
	}
	for _, role := range tenantRoles {
		want := append([]string(nil), expectedTenantPairs[role]...)
		sort.Strings(want)
		if got := grantPairSet(t, mgr, tenant, role); !equalSets(got, want) {
			t.Errorf("the %s role carries %v, want exactly %v", role, got, want)
		}
	}
}

// TestTheTenantRolesCarryTheirOwnDescriptionKey holds each tenant role's
// persisted description key to a literal, so a key swapped for another role's is
// caught where the role rows are read.
func TestTheTenantRolesCarryTheirOwnDescriptionKey(t *testing.T) {
	t.Parallel()
	const tenant = "t_acme"
	mgr := newAuthzFakeManager()
	if err := provisionTenantRoles(t.Context(), mgr, tenant); err != nil {
		t.Fatalf("provisioning the tenant roles: %v", err)
	}
	wantKeys := map[authz.Role]string{
		authzRoleOwner:  "crm.role.owner",
		authzRoleAdmin:  "crm.role.admin",
		authzRoleRep:    "crm.role.rep",
		authzRoleViewer: "crm.role.viewer",
	}
	for role, want := range wantKeys {
		if got := mgr.roles[authzRoleKey{tenant, role}].DescriptionKey; got != want {
			t.Errorf("the %s role's description key is %q, want %q", role, got, want)
		}
	}
}

// TestTheHostsBusinessPermissionsHaveDistinctDescriptionsAndPairs holds two
// invariants of the host's vocabulary: every business permission carries a
// non-empty, distinct description key, and no two translate to the same
// (object, action) pair. Two permissions sharing a pair would let one tier's
// grant satisfy a route another tier declares — a cross-tier collision the
// per-tier set comparison cannot see.
func TestTheHostsBusinessPermissionsHaveDistinctDescriptionsAndPairs(t *testing.T) {
	t.Parallel()
	spec := businessPermissionSpec()
	seenKey := map[string]authz.Permission{}
	seenPair := map[string]authz.Permission{}
	for _, p := range businessPermissions {
		key := spec.DescriptionKeys[p]
		if key == "" {
			t.Errorf("the permission %q carries no description key", p)
		} else if other, dup := seenKey[key]; dup {
			t.Errorf("the permissions %q and %q share the description key %q", other, p, key)
		} else {
			seenKey[key] = p
		}
		object, action, ok := resourceActionOf(p)
		if !ok {
			t.Errorf("the permission %q has no translation row", p)
			continue
		}
		pair := object + "\x00" + action
		if other, dup := seenPair[pair]; dup {
			t.Errorf("the permissions %q and %q both translate to (%s, %s), so a grant of one satisfies a route declaring the other",
				other, p, object, action)
		} else {
			seenPair[pair] = p
		}
	}
}

// TestATenantAdminIsRefusedTheIdentityMemberSurface is the case for the identity
// system's management membership routes, which the authorization layer's Decider
// decides in the request's tenant domain on authn.membership.manage. No tenant
// role carries that permission, so a tenant admin is refused — a role that held
// it would let any tenant's admin add a member to any tenant through a route on
// the account endpoint the enforcement middleware does not guard.
func TestATenantAdminIsRefusedTheIdentityMemberSurface(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		jenny := c.createUser("jenny")
		c.grant(acme, alice, roleOwner)
		c.grant(acme, jenny, roleAdmin)
		h := c.serve()
		asAdmin := bearer(h.login(t, jenny))
		expect(t, "a tenant admin listing members on the identity system's route",
			h.get(t, accountEndpoint, "/auth/admin/tenants/"+acme.ID+"/members", "", asAdmin), nethttp.StatusForbidden)
		expect(t, "a tenant admin writing a membership on the identity system's route",
			h.send(t, request{
				method: nethttp.MethodPut, endpoint: accountEndpoint,
				path: "/auth/admin/tenants/" + acme.ID + "/members/" + alice.ID,
				cred: asAdmin, body: map[string]any{"active": true},
			}), nethttp.StatusForbidden)
	})
}

// TestTheOperatorReachesTheAuthorizationAdministration is the case for the
// host's mount of the authorization management routes: the platform operator,
// who holds authz.role.read in the platform domain, reads the permission catalog
// and is admitted; a tenant owner and an anonymous caller are refused by the
// admin endpoint's operator check before any decision runs.
func TestTheOperatorReachesTheAuthorizationAdministration(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleOwner)
		h := c.serve()
		expect(t, "the operator reading the authorization permission catalog",
			h.get(t, adminEndpoint, "/authz/admin/permissions", "", operator()), nethttp.StatusOK)
		expect(t, "a tenant owner on the authorization administration",
			h.get(t, adminEndpoint, "/authz/admin/permissions", "", signedIn(alice)), nethttp.StatusForbidden)
		expect(t, "an anonymous caller on the authorization administration",
			h.get(t, adminEndpoint, "/authz/admin/permissions", "", noCredential{}), nethttp.StatusUnauthorized)
	})
}

// TestTheAdminEndpointRefusesAMalformedTenantRequest is the case for the
// registration body's validation: an identifier or an alias the roster would
// refuse is answered 422 naming the field, rather than passed to the roster and
// answered by its own error.
func TestTheAdminEndpointRefusesAMalformedTenantRequest(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		h := c.serve()
		expect(t, "registering a tenant with a malformed identifier",
			h.post(t, adminEndpoint, "/tenants", "", operator(),
				map[string]string{"id": "bad id", "alias": "ok-alias"}), nethttp.StatusUnprocessableEntity)
		expect(t, "registering a tenant with a malformed alias",
			h.post(t, adminEndpoint, "/tenants", "", operator(),
				map[string]string{"id": "t_ok", "alias": "bad alias"}), nethttp.StatusUnprocessableEntity)
	})
}

// TestTheAdminEndpointRefusesGrantingIntoAnUnregisteredTenant is the case for
// the admin grant's roster read: a membership named for a tenant the roster does
// not hold is answered 404, and neither a membership nor a policy binding is
// written for it.
func TestTheAdminEndpointRefusesGrantingIntoAnUnregisteredTenant(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		alice := c.createUser("alice")
		h := c.serve()
		unknown := c.tenantNamed("nosuch")
		expect(t, "the operator granting into a tenant nobody registered",
			h.post(t, adminEndpoint, "/tenants/"+unknown.ID+"/members", "", operator(),
				map[string]string{"email": alice.Email, "role": roleRep}), nethttp.StatusNotFound)
		if got := c.count("SELECT count(*) FROM authn_memberships WHERE tenant_id = ?", unknown.ID); got != 0 {
			t.Errorf("the refused grant wrote %d memberships in the unregistered tenant, want none", got)
		}
		if got := c.count("SELECT count(*) FROM authz_bindings WHERE tenant_id = ?", unknown.ID); got != 0 {
			t.Errorf("the refused grant wrote %d bindings in the unregistered tenant, want none", got)
		}
	})
}

// TestGrantingAMembershipToAnUnknownAddressIsRefused is the case for the API
// grant's account look-up: an email no account owns is answered 404, and no
// membership or binding is written under a zero account.
func TestGrantingAMembershipToAnUnknownAddressIsRefused(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleOwner)
		h := c.serve()
		asOwner := bearer(h.login(t, alice))
		resp := h.send(t, api(nethttp.MethodPost, "/members", acme, asOwner,
			map[string]any{"email": "ghost@" + c.suffix + ".crm.test", "role": roleRep}))
		expect(t, "granting a membership to an address no account owns", resp, nethttp.StatusNotFound)
		if got := c.count("SELECT count(*) FROM authn_memberships WHERE tenant_id = ?", acme.ID); got != 1 {
			t.Errorf("the refused grant left %d memberships in the tenant, want the owner's one", got)
		}
		if got := c.count("SELECT count(*) FROM authz_bindings WHERE tenant_id = ?", acme.ID); got != 1 {
			t.Errorf("the refused grant left %d bindings in the tenant, want the owner's one", got)
		}
	})
}

// TestTheMembersListingShowsEachAccountsEmail is the case for the listing's
// display: a member's row carries the email among the account's identifiers and
// not another identifier's value.
func TestTheMembersListingShowsEachAccountsEmail(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleOwner)
		h := c.serve()
		list := h.members(t, acme.Alias, bearer(h.login(t, alice)))
		found := false
		for _, m := range list.Members {
			if m.AccountID != alice.ID {
				continue
			}
			found = true
			if m.Email != alice.Email {
				t.Errorf("the listing shows alice under email %q, want %q", m.Email, alice.Email)
			}
		}
		if !found {
			t.Fatalf("the listing holds no row for alice: %+v", list.Members)
		}
	})
}

// TestACommandLineGrantIntoAnUnregisteredTenantIsRefused is the case for the
// command-line grant's roster read: a grant naming a tenant nobody registered
// fails naming no such tenant, and writes neither a membership nor a binding.
func TestACommandLineGrantIntoAnUnregisteredTenantIsRefused(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		alice := c.createUser("alice")
		unknown := c.tenantNamed("nosuch")
		res := c.run(nil, "", "user", "grant", "--tenant-id", unknown.ID, "--email", alice.Email, "--role", roleRep)
		if res.err == nil {
			t.Fatalf("granting into an unregistered tenant succeeded; output:\n%s", res.combined())
		}
		if !strings.Contains(res.combined(), errNoSuchTenant.Error()) {
			t.Errorf("the refusal does not name a missing tenant: %s", res.combined())
		}
		if got := c.count("SELECT count(*) FROM authn_memberships"); got != 0 {
			t.Errorf("the refused grant wrote %d memberships, want none", got)
		}
		if got := c.count("SELECT count(*) FROM authz_bindings"); got != 0 {
			t.Errorf("the refused grant wrote %d bindings, want none", got)
		}
	})
}

// TestACommandLineGrantRecordsTheAdminSource is the case for the membership a
// command-line grant writes: its source is the administrator's, not the
// import's, so a later import leaves it as an administrator's change.
func TestACommandLineGrantRecordsTheAdminSource(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleRep)
		if got := c.text("SELECT source FROM authn_memberships WHERE tenant_id = ? AND account_id = ?", acme.ID, alice.ID); got != "admin" {
			t.Errorf("the granted membership's source is %q, want admin", got)
		}
	})
}

// The role-unknown branch of carryMemberRole is not exercised here: the legacy
// member_roles table's migration carries CHECK (role IN ('owner','admin','rep',
// 'viewer')), so a row with an unknown role cannot be inserted and the branch is
// unreachable through the table — a defense-in-depth guard, like the import
// command's own validRole check, which the schema already enforces. The
// membership import's role-unknown branch IS reachable (its legacy table carries
// no CHECK) and is covered by TestTheImportRefusesRowsItCannotCarryAndGoesOn.

// TestTheRoleImportRefusesAMalformedAccountID is the case for a member_roles row
// whose account identifier breaks the account shape: it is counted failed with
// the reason identifier-not-an-account-id, and no binding is written for it.
func TestTheRoleImportRefusesAMalformedAccountID(t *testing.T) {
	t.Parallel()
	forImportStacks(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		c.insertMemberRole(acme.ID, "bad id!", roleViewer)

		var rep dataImportReport
		c.resultOf(c.mustRun("", "role", "import"), &rep)
		if rep.Failed != 1 || rep.Reasons["identifier-not-an-account-id"] != 1 {
			t.Fatalf("the import answered failed %d reasons %v, want one identifier-not-an-account-id", rep.Failed, rep.Reasons)
		}
		if got := c.count("SELECT count(*) FROM authz_bindings"); got != 0 {
			t.Errorf("the import wrote %d bindings for the malformed row, want none", got)
		}
	})
}

// TestTheRoleImportDryRunResumesAHalfWrittenRole is the case for a --dry-run
// over the state a real run would resume — the membership written, the binding
// missing: the dry run answers resumed, and writes no binding, role or grant
// row.
func TestTheRoleImportDryRunResumesAHalfWrittenRole(t *testing.T) {
	t.Parallel()
	forImportStacks(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.insertMemberRole(acme.ID, alice.ID, roleRep)
		c.grant(acme, alice, roleViewer)
		c.exec("DELETE FROM authz_bindings WHERE tenant_id = ? AND account_id = ?", acme.ID, alice.ID)
		rolesBefore := c.count("SELECT count(*) FROM authz_roles")
		grantsBefore := c.count("SELECT count(*) FROM authz_grants")

		var dry dataImportReport
		c.resultOf(c.mustRun("", "role", "import", "--dry-run"), &dry)
		if dry.Resumed != 1 || dry.Created != 0 || dry.Skipped != 0 || dry.Failed != 0 {
			t.Fatalf("the dry run answered created %d resumed %d skipped %d failed %d, want resumed 1",
				dry.Created, dry.Resumed, dry.Skipped, dry.Failed)
		}
		if got := c.count("SELECT count(*) FROM authz_bindings"); got != 0 {
			t.Errorf("the dry run wrote %d bindings, want none", got)
		}
		if got := c.count("SELECT count(*) FROM authz_roles"); got != rolesBefore {
			t.Errorf("the dry run changed the role rows from %d to %d, want no change", rolesBefore, got)
		}
		if got := c.count("SELECT count(*) FROM authz_grants"); got != grantsBefore {
			t.Errorf("the dry run changed the grant rows from %d to %d, want no change", grantsBefore, got)
		}
	})
}

// TestTheRoleImportLeavesARaisedRoleAsItIs is the case for a re-run over a
// binding an administrator raised after the first run: the held role is left as
// it is and counted skipped, so a repeated import never reverts the raise.
func TestTheRoleImportLeavesARaisedRoleAsItIs(t *testing.T) {
	t.Parallel()
	forImportStacks(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.insertMemberRole(acme.ID, alice.ID, roleViewer)

		c.mustRun("", "role", "import")
		c.exec("UPDATE authz_bindings SET role = ? WHERE tenant_id = ? AND account_id = ?", roleOwner, acme.ID, alice.ID)

		var again dataImportReport
		c.resultOf(c.mustRun("", "role", "import"), &again)
		if again.Skipped != 1 || again.Created != 0 || again.Resumed != 0 || again.Failed != 0 {
			t.Errorf("the re-run answered created %d resumed %d skipped %d failed %d, want skipped 1",
				again.Created, again.Resumed, again.Skipped, again.Failed)
		}
		if got := c.text("SELECT role FROM authz_bindings WHERE tenant_id = ? AND account_id = ?", acme.ID, alice.ID); got != roleOwner {
			t.Errorf("the re-run changed the raised role to %q, want %q left as it is", got, roleOwner)
		}
	})
}

// TestTheRoleImportCommandRunsUnderTheHostsCommandGroup is the case for the
// shape the role-migration command is registered with: it is a leaf of the host's
// command-group mechanism, drives to Start because its write is recorded through
// the audit seam, and its module names the manager, the membership store and the
// roster it uses, so a command that took it in without one fails at startup.
func TestTheRoleImportCommandRunsUnderTheHostsCommandGroup(t *testing.T) {
	t.Parallel()
	if roleImportGroup.word != "role" || len(roleImportGroup.commands) != 1 {
		t.Fatalf("the role group is %q with %d commands, want role with import", roleImportGroup.word, len(roleImportGroup.commands))
	}
	if roleImportGroup.commands[0].use != "import" || roleImportGroup.commands[0].depth != core.StageStart {
		t.Errorf("the role import command is %q at %v, want import at Start: its write is recorded through the audit seam",
			roleImportGroup.commands[0].use, roleImportGroup.commands[0].depth)
	}
	var found *application
	for i := range applications {
		if applications[i].module.Name == roleImportModuleName {
			found = &applications[i]
		}
	}
	if found == nil {
		t.Fatalf("no application is registered under %q", roleImportModuleName)
	}
	if !contains(found.runsOn, roleImportGroup.word) {
		t.Errorf("%q runs on %v, which does not name the role group", roleImportModuleName, found.runsOn)
	}
	if !contains(modulesOn(roleImportGroup.word), roleImportModuleName) {
		t.Errorf("the command tree does not take %q into the role group", roleImportModuleName)
	}
	for _, token := range []core.Token{(*authz.Manager)(nil), (*authn.Memberships)(nil), (*tenant.Catalog)(nil)} {
		if !requiresCapability(found.module, token) {
			t.Errorf("%q does not require the capability %v it uses", roleImportModuleName, token)
		}
	}
}

// TestTheRoleImportRecordsItsCounts is the case for the journal row the role
// import writes: its object names the roles and its detail carries the counts
// the report answered, so the recorded seam matches the answer.
func TestTheRoleImportRecordsItsCounts(t *testing.T) {
	t.Parallel()
	forImportStacks(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.insertMemberRole(acme.ID, alice.ID, roleOwner)

		var rep dataImportReport
		c.resultOf(c.mustRun("", "role", "import"), &rep)
		object := c.text("SELECT object FROM audit_entries WHERE action = 'role.import'")
		if object != "roles" {
			t.Errorf("the role.import row's object is %q, want roles", object)
		}
		var detail map[string]string
		if err := json.Unmarshal([]byte(c.text("SELECT detail FROM audit_entries WHERE action = 'role.import'")), &detail); err != nil {
			t.Fatalf("decoding the role.import detail: %v", err)
		}
		for _, key := range []string{"created", "resumed", "skipped", "failed"} {
			var want int
			switch key {
			case "created":
				want = rep.Created
			case "resumed":
				want = rep.Resumed
			case "skipped":
				want = rep.Skipped
			case "failed":
				want = rep.Failed
			}
			if detail[key] != strconv.Itoa(want) {
				t.Errorf("the role.import detail %q is %q, want %d", key, detail[key], want)
			}
		}
	})
}

// TestTheOperatorProvisionAnswersItsReportAndRecordsIt is the case for the
// operator-provision command's answer and its journal row: the answer names the
// platform domain, the operator's account and the operator role, and the seam
// records the same under operator.provision.
func TestTheOperatorProvisionAnswersItsReportAndRecordsIt(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()

	var rep operatorProvisionReport
	c.resultOf(c.mustRun("", "operator", "provision"), &rep)
	if rep.Domain != string(authz.PlatformDomain) {
		t.Errorf("the answer's domain is %q, want %q", rep.Domain, authz.PlatformDomain)
	}
	if rep.Account != string(operatorAccount) {
		t.Errorf("the answer's account is %q, want %q", rep.Account, operatorAccount)
	}
	if rep.Role != string(authzRoleOperator) {
		t.Errorf("the answer's role is %q, want %q", rep.Role, authzRoleOperator)
	}
	row := c.awaitJournal("the operator provision", func(r auditRow) bool {
		return r.action == "operator.provision"
	})
	wantObject := "bindings/" + string(authz.PlatformDomain) + "/" + string(operatorAccount)
	if row.object != wantObject {
		t.Errorf("the operator.provision row's object is %q, want %q", row.object, wantObject)
	}
	var detail map[string]string
	if err := json.Unmarshal([]byte(c.text("SELECT detail FROM audit_entries WHERE action = 'operator.provision'")), &detail); err != nil {
		t.Fatalf("decoding the operator.provision detail: %v", err)
	}
	if detail["role"] != string(authzRoleOperator) {
		t.Errorf("the operator.provision detail role is %q, want %q", detail["role"], authzRoleOperator)
	}
}

// TestTheOperatorProvisionCommandRequiresItsCapabilities is the case for the
// command module's declared requirements and the two contracts it names: the
// policy manager and the logger, and the operator role and the module name the
// command writes under.
func TestTheOperatorProvisionCommandRequiresItsCapabilities(t *testing.T) {
	t.Parallel()
	if !requiresCapability(operatorCommandsModule(), (*authz.Manager)(nil)) {
		t.Errorf("the operator-provision module does not require the policy manager it writes through")
	}
	if !requiresCapability(operatorCommandsModule(), (*log.Logger)(nil)) {
		t.Errorf("the operator-provision module does not require the logger its release records use")
	}
	if string(authzRoleOperator) != "operator" {
		t.Errorf("the operator role is %q, want operator", authzRoleOperator)
	}
	if authzRolesModuleName != "authz-roles" {
		t.Errorf("the platform-role provisioner's module name is %q, want authz-roles", authzRolesModuleName)
	}
}

// TestAGrantDoesNotReprovisionTheTenantMatrix is the case for the grant path's
// provisioning guard: the first grant in a tenant writes the role matrix, and a
// later grant on the same tenant writes its binding alone rather than re-issuing
// every matrix row — each of which is a manager write that reloads the whole
// cross-tenant policy and publishes a change event.
func TestAGrantDoesNotReprovisionTheTenantMatrix(t *testing.T) {
	t.Parallel()
	const domain = "t_acme"
	mgr := newAuthzFakeManager()
	if _, err := grantRole(t.Context(), mgr, tenant.ID(domain), authn.AccountID("acct-1"), roleViewer); err != nil {
		t.Fatalf("the first grant: %v", err)
	}
	provisioned := mgr.grantPuts
	if provisioned == 0 {
		t.Fatalf("the first grant wrote no grant rows, so the guard's effect is not observable")
	}
	if _, err := grantRole(t.Context(), mgr, tenant.ID(domain), authn.AccountID("acct-2"), roleRep); err != nil {
		t.Fatalf("the second grant: %v", err)
	}
	if got := mgr.grantPuts - provisioned; got != 0 {
		t.Errorf("a second grant in an already-provisioned tenant issued %d PutGrant calls, want none", got)
	}
}

// builtinFlipManager is a policy manager that answers ErrBuiltinRole when a role
// is marked builtin, standing in for the second of two replicas that provision
// the platform operator at the same moment: whichever marks the role builtin
// first makes the other's flip refuse.
type builtinFlipManager struct{ *authzFakeManager }

func (m *builtinFlipManager) PutRole(ctx context.Context, r authz.RoleRecord) error {
	if r.Builtin {
		return authz.ErrBuiltinRole
	}
	return m.authzFakeManager.PutRole(ctx, r)
}

// TestProvisioningToleratesAFlippedBuiltinOperatorRole is the case for the
// concurrent-provisioning tolerance: a manager that refuses the builtin flip
// with ErrBuiltinRole leaves the run succeeding and the binding written, the end
// state a second replica reaches.
func TestProvisioningToleratesAFlippedBuiltinOperatorRole(t *testing.T) {
	t.Parallel()
	mgr := &builtinFlipManager{authzFakeManager: newAuthzFakeManager()}
	if err := provisionPlatformRoles(t.Context(), mgr); err != nil {
		t.Fatalf("provisioning against a manager that refused the builtin flip: %v", err)
	}
	domain := string(authz.PlatformDomain)
	bindings, err := mgr.Bindings(t.Context(), domain)
	if err != nil {
		t.Fatalf("reading the platform bindings: %v", err)
	}
	if len(bindings) != 1 || bindings[0].Account != string(operatorAccount) {
		t.Errorf("the binding was not written after a tolerated builtin flip: %+v", bindings)
	}
}

// TestTheImportRefusesAStaticMembership is the case for a legacy membership row
// naming an account under the static prefix: it is counted failed with the reason
// identifier-static, mirroring the users-side row, and no membership is written.
func TestTheImportRefusesAStaticMembership(t *testing.T) {
	t.Parallel()
	forImportStacks(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		seedLegacySchema(t, c)
		c.insertLegacyMembership(acme.ID, authn.StaticPrefix+"someone", roleViewer)

		var rep dataImportReport
		c.resultOf(c.mustRun("", "import", "run"), &rep)
		if rep.Failed != 1 || rep.Reasons["identifier-static"] != 1 {
			t.Fatalf("the import answered failed %d reasons %v, want one identifier-static", rep.Failed, rep.Reasons)
		}
		if got := c.count("SELECT count(*) FROM authn_memberships"); got != 0 {
			t.Errorf("the import wrote %d memberships for the static row, want none", got)
		}
	})
}

// TestAnAdminCreatedAccountRecordsItsProvenance is the case for an
// administrator-created account's email identifier: it is written verified-via
// admin and not the import's unverified assertion, so the two provenances are
// told apart like the import path's.
func TestAnAdminCreatedAccountRecordsItsProvenance(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		h := c.serve()
		u := h.adminCreateUser(t, c.userNamed("carol"))
		got := c.text("SELECT verified_via FROM authn_identifiers WHERE account_id = ? AND kind = 'email'", u.ID)
		if got != "admin" {
			t.Errorf("the admin-created account's email identifier is verified %q, want admin", got)
		}
	})
}
