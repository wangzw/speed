package main

// The host tenancy seams: the membership checker the platform administrator
// binding asks, the write guard protecting the tenants an operator may not
// take out of service, and the provisioner that opens a tenant together with
// its first tenant administrator.

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// tenancyProductOf builds the host product over the unit fakes.
func tenancyProductOf(
	roster *fakeRoster,
	memberships *fakeMemberships,
	manager *fakeManager,
	seeder *fakeSeeder,
	catalog authz.Catalog,
) *tenancyProduct {
	return &tenancyProduct{
		roster:      roster,
		memberships: memberships,
		manager:     manager,
		seeder:      seeder,
		catalog:     catalog,
		logger:      log.Default(),
	}
}

// TestTheMembershipCheckerReadsTheMembershipSeam holds the checker to the
// account membership store: an active membership answers true, an absent one
// false, and a subject no account identifier can name is false rather than a
// failure.
func TestTheMembershipCheckerReadsTheMembershipSeam(t *testing.T) {
	memberships := newFakeMemberships()
	if err := memberships.Put(context.Background(), authn.Membership{
		Account: authn.AccountID("acct-1"), Tenant: "system", Active: true, Source: "admin",
	}); err != nil {
		t.Fatalf("writing the stand-in membership: %v", err)
	}
	product := tenancyProductOf(newFakeRoster(), memberships, nil, nil, nil)

	member, err := product.IsMember(context.Background(), "system", "acct-1")
	if err != nil {
		t.Fatalf("IsMember: %v", err)
	}
	if !member {
		t.Error("acct-1 holds an active membership of system, so IsMember answered false")
	}
	member, err = product.IsMember(context.Background(), "system", "acct-2")
	if err != nil {
		t.Fatalf("IsMember for an account with no membership: %v", err)
	}
	if member {
		t.Error("acct-2 holds no membership of system, so IsMember answered true")
	}
	member, err = product.IsMember(context.Background(), "system", "not an account id")
	if err != nil {
		t.Errorf("a subject no account identifier can name answered an error: %v", err)
	}
	if member {
		t.Error("a subject no account identifier can name read as a member")
	}
}

// TestTheWriteGuardProtectsTheSystemTenantAndPersonalTenants holds the two
// protected classes apart from an ordinary tenant, for both protected writes.
func TestTheWriteGuardProtectsTheSystemTenantAndPersonalTenants(t *testing.T) {
	product := tenancyProductOf(newFakeRoster(), newFakeMemberships(), nil, nil, nil)
	ctx := context.Background()
	for _, id := range []tenant.ID{
		tenant.ID(authz.PlatformDomain),
		tenant.ID(personalIDPrefix + "acct-1"),
	} {
		for _, op := range []tenant.WriteOp{tenant.WriteDisable, tenant.WriteDelete} {
			denied, err := product.Denied(ctx, id, op)
			if err != nil {
				t.Errorf("Denied(%q, %d): %v", id, op, err)
			}
			if !denied {
				t.Errorf("Denied(%q, %d) = false, want true", id, op)
			}
		}
	}
	for _, op := range []tenant.WriteOp{tenant.WriteDisable, tenant.WriteDelete} {
		denied, err := product.Denied(ctx, "acme", op)
		if err != nil {
			t.Errorf("Denied(acme, %d): %v", op, err)
		}
		if denied {
			t.Errorf("Denied(acme, %d) = true, want false", op)
		}
	}
}

// TestTheProvisionerWritesTheTenantAdminAndTheCreatorBinding holds the order
// the seam promises: the role, its declared grants and the creator's binding
// exist before the roster row, and the row is the last write.
func TestTheProvisionerWritesTheTenantAdminAndTheCreatorBinding(t *testing.T) {
	roster := newFakeRoster()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	seeder := newFakeSeeder(grants)
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read":  {"deal", "read"},
		"crm.deal.write": {"deal", "write"},
	}}
	product := tenancyProductOf(roster, newFakeMemberships(), manager, seeder, catalog)

	record := tenant.Tenant{ID: "acme", Alias: "acme", Status: tenant.StatusActive}
	if err := product.Provision(context.Background(), record, "acct-1"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if _, found, _ := roster.Get(context.Background(), "acme"); !found {
		t.Error("the provisioner did not register the tenant")
	}
	roles, _ := manager.Roles(context.Background(), "acme")
	if len(roles) != 1 || roles[0].Role != authz.TenantAdminRole {
		t.Errorf("the tenant's roles = %+v, want one tenant-admin", roles)
	}
	held := grants.held("acme", authz.TenantAdminRole)
	if len(held) != 2 {
		t.Errorf("the tenant-admin holds %d grant(s), want the two declared pairs", len(held))
	}
	bindings, _ := manager.BindingsOf(context.Background(), "acme", "acct-1")
	if len(bindings) != 1 || bindings[0].Role != authz.TenantAdminRole {
		t.Errorf("the creator's bindings = %+v, want one tenant-admin", bindings)
	}
}

// TestTheProvisionerWritesTheCreatorMembership holds the requirement that
// opening a tenant writes the creator's membership together with the
// tenant-admin binding: a binding without the membership names an administrator
// who cannot enter the tenant it administers.
func TestTheProvisionerWritesTheCreatorMembership(t *testing.T) {
	roster := newFakeRoster()
	memberships := newFakeMemberships()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	seeder := newFakeSeeder(grants)
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	product := tenancyProductOf(roster, memberships, manager, seeder, catalog)

	if err := product.Provision(context.Background(),
		tenant.Tenant{ID: "acme", Alias: "acme", Status: tenant.StatusActive}, "acct-1"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	member, err := memberships.IsMember(context.Background(), authn.AccountID("acct-1"), "acme")
	if err != nil {
		t.Fatalf("reading the creator's membership: %v", err)
	}
	if !member {
		t.Error("the creator is not an active member of the tenant it created")
	}
}

// TestTheProvisionerLeavesTheWinnersPolicyWhenItLosesTheRace holds the race
// between two creations of one identifier: this replica's snapshot predates the
// row another replica wrote, so the pre-check misses it and Create is the write
// that refuses. The losing call must leave the winner's role and binding in
// place rather than tear the domain down, and remove the rows it added itself.
func TestTheProvisionerLeavesTheWinnersPolicyWhenItLosesTheRace(t *testing.T) {
	roster := newFakeRoster()
	roster.rows["acme"] = tenant.Tenant{ID: "acme", Alias: "acme", Status: tenant.StatusActive}
	roster.lagging["acme"] = true
	memberships := newFakeMemberships()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	manager.roles["acme"] = []authz.RoleRecord{{Tenant: "acme", Role: authz.TenantAdminRole}}
	manager.bindings["acme"] = []authz.Binding{
		{Tenant: "acme", Account: "winner", Role: authz.TenantAdminRole},
	}
	seeder := newFakeSeeder(grants)
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	product := tenancyProductOf(roster, memberships, manager, seeder, catalog)

	err := product.Provision(context.Background(),
		tenant.Tenant{ID: "acme", Alias: "acme", Status: tenant.StatusActive}, "acct-loser")
	if !errors.Is(err, tenant.ErrTenantExists) {
		t.Fatalf("Provision that lost the race = %v, want ErrTenantExists", err)
	}
	roles, _ := manager.Roles(context.Background(), "acme")
	if len(roles) != 1 || roles[0].Role != authz.TenantAdminRole {
		t.Errorf("the winner's role = %+v, want the tenant-admin role left in place", roles)
	}
	bindings, _ := manager.Bindings(context.Background(), "acme")
	if len(bindings) != 1 || bindings[0].Account != "winner" || bindings[0].Role != authz.TenantAdminRole {
		t.Errorf("the tenant's bindings = %+v, want only the winner's", bindings)
	}
	member, err := memberships.IsMember(context.Background(), authn.AccountID("acct-loser"), "acme")
	if err != nil {
		t.Fatalf("reading the losing creator's membership: %v", err)
	}
	if member {
		t.Error("the losing provision left the creator's membership behind")
	}
}

// TestTheProvisionerMarksTheTenantAdminRoleBuiltin holds the role's frozen
// state: the management surface may neither rewrite nor delete it, as with the
// administrator roles the seed writes.
func TestTheProvisionerMarksTheTenantAdminRoleBuiltin(t *testing.T) {
	roster := newFakeRoster()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	seeder := newFakeSeeder(grants)
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	product := tenancyProductOf(roster, newFakeMemberships(), manager, seeder, catalog)

	if err := product.Provision(context.Background(),
		tenant.Tenant{ID: "acme", Alias: "acme", Status: tenant.StatusActive}, "acct-1"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	roles, err := manager.Roles(context.Background(), "acme")
	if err != nil {
		t.Fatalf("reading the tenant's roles: %v", err)
	}
	if len(roles) != 1 || roles[0].Role != authz.TenantAdminRole || !roles[0].Builtin {
		t.Errorf("the tenant's roles = %+v, want one builtin tenant-admin", roles)
	}
}

// TestTheProvisionerLeavesAPersonalPrefixesRoleUnfrozen holds the other half of
// the builtin distinction: the host identifies a personal domain by the
// identifier's prefix in every seam, so a tenant opened with such an identifier
// carries the same unfrozen role the host-built personal tenant does.
func TestTheProvisionerLeavesAPersonalPrefixesRoleUnfrozen(t *testing.T) {
	roster := newFakeRoster()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	seeder := newFakeSeeder(grants)
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	product := tenancyProductOf(roster, newFakeMemberships(), manager, seeder, catalog)

	id := tenant.ID(personalIDPrefix + "acme")
	if err := product.Provision(context.Background(),
		tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}, "acct-1"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	roles, err := manager.Roles(context.Background(), string(id))
	if err != nil {
		t.Fatalf("reading the tenant's roles: %v", err)
	}
	if len(roles) != 1 || roles[0].Role != authz.TenantAdminRole {
		t.Fatalf("the tenant's roles = %+v, want one tenant-admin", roles)
	}
	if roles[0].Builtin {
		t.Error("the personal-prefixed tenant's tenant-admin role is builtin")
	}
}

// TestTheProvisionerAnswersATakenIdentifierWhenTheWinnerFrozeTheRole holds the
// same race one step later: the winning replica had already marked its role
// builtin, so the losing call's PutRole is refused before Create is reached.
// The identifier is registered, so the answer is the already-exists refusal, not
// a builtin-role failure, and the winner's policy is untouched.
func TestTheProvisionerAnswersATakenIdentifierWhenTheWinnerFrozeTheRole(t *testing.T) {
	roster := newFakeRoster()
	roster.rows["acme"] = tenant.Tenant{ID: "acme", Alias: "acme", Status: tenant.StatusActive}
	roster.lagging["acme"] = true
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	manager.roles["acme"] = []authz.RoleRecord{
		{Tenant: "acme", Role: authz.TenantAdminRole, Builtin: true},
	}
	manager.bindings["acme"] = []authz.Binding{
		{Tenant: "acme", Account: "winner", Role: authz.TenantAdminRole},
	}
	seeder := newFakeSeeder(grants)
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	product := tenancyProductOf(roster, newFakeMemberships(), manager, seeder, catalog)

	err := product.Provision(context.Background(),
		tenant.Tenant{ID: "acme", Alias: "acme", Status: tenant.StatusActive}, "acct-loser")
	if !errors.Is(err, tenant.ErrTenantExists) {
		t.Fatalf("Provision of an identifier whose role is already builtin = %v, want ErrTenantExists", err)
	}
	roles, _ := manager.Roles(context.Background(), "acme")
	if len(roles) != 1 || !roles[0].Builtin {
		t.Errorf("the winner's role = %+v, want the builtin tenant-admin left in place", roles)
	}
	bindings, _ := manager.Bindings(context.Background(), "acme")
	if len(bindings) != 1 || bindings[0].Account != "winner" {
		t.Errorf("the winner's bindings = %+v, want only its own binding", bindings)
	}
}

// TestTheProvisionerCleansItsPolicyWhenTheRosterRefuses holds the failure
// path: a roster that refuses the row leaves no role, grant, binding or
// membership behind.
func TestTheProvisionerCleansItsPolicyWhenTheRosterRefuses(t *testing.T) {
	roster := newFakeRoster()
	roster.createErr = errors.New("the roster is unavailable")
	memberships := newFakeMemberships()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	seeder := newFakeSeeder(grants)
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	product := tenancyProductOf(roster, memberships, manager, seeder, catalog)

	err := product.Provision(context.Background(),
		tenant.Tenant{ID: "acme", Alias: "acme", Status: tenant.StatusActive}, "acct-1")
	if err == nil {
		t.Fatal("Provision answered nil for a roster that refused the row")
	}
	roles, _ := manager.Roles(context.Background(), "acme")
	if len(roles) != 0 {
		t.Errorf("the failed provision left %d role(s) behind", len(roles))
	}
	bindings, _ := manager.Bindings(context.Background(), "acme")
	if len(bindings) != 0 {
		t.Errorf("the failed provision left %d binding(s) behind", len(bindings))
	}
	if held := grants.held("acme", authz.TenantAdminRole); len(held) != 0 {
		t.Errorf("the failed provision left %d grant(s) behind", len(held))
	}
	if member, _ := memberships.IsMember(context.Background(), authn.AccountID("acct-1"), "acme"); member {
		t.Error("the failed provision left the creator's membership behind")
	}
}

// TestTheMembershipCheckerTrustsAConfiguredStaticPrincipal holds the host's
// exception for the static credential table: its subjects are operators the
// deployment configured, not accounts that registered themselves, so they hold
// no membership row and the checker answers true rather than reading a row that
// does not exist.
func TestTheMembershipCheckerTrustsAConfiguredStaticPrincipal(t *testing.T) {
	memberships := newFakeMemberships()
	product := tenancyProductOf(newFakeRoster(), memberships, nil, nil, nil)

	for _, tenantID := range []string{"system", "acme"} {
		member, err := product.IsMember(context.Background(), tenantID, "static.ops")
		if err != nil {
			t.Fatalf("IsMember(static.ops, %q): %v", tenantID, err)
		}
		if !member {
			t.Errorf("the configured static principal static.ops is not a member of %q", tenantID)
		}
	}
	// The exception is the static namespace, not every unparseable subject.
	member, err := product.IsMember(context.Background(), "system", "not an account id")
	if err != nil {
		t.Fatalf("IsMember of an unparseable subject: %v", err)
	}
	if member {
		t.Error("a subject outside the account-identifier domain read as a member")
	}
}

// TestThePolicyWriteGuardRefusesEveryPersonalDomainWrite holds the host's
// protection of a personal tenant's policy data: every management write of a
// role, a grant or a binding is refused in a domain whose identifier starts
// with the personal prefix, and an ordinary or system domain is left alone.
func TestThePolicyWriteGuardRefusesEveryPersonalDomainWrite(t *testing.T) {
	guard := policyWriteGuard{}
	ctx := context.Background()
	ops := []authz.WriteOp{authz.WriteRole, authz.WriteGrant, authz.WriteBinding}
	for _, op := range ops {
		for _, domain := range []string{personalIDPrefix + "acct-1", personalIDPrefix} {
			denied, err := guard.Denied(ctx, domain, op)
			if err != nil {
				t.Fatalf("Denied(%q, %d): %v", domain, op, err)
			}
			if !denied {
				t.Errorf("Denied(%q, %d) = false, want the personal domain refused", domain, op)
			}
		}
		for _, domain := range []string{"acme", string(authz.PlatformDomain)} {
			denied, err := guard.Denied(ctx, domain, op)
			if err != nil {
				t.Fatalf("Denied(%q, %d): %v", domain, op, err)
			}
			if denied {
				t.Errorf("Denied(%q, %d) = true, want an ordinary domain allowed", domain, op)
			}
		}
	}
}

// TestTheMembershipGuardRefusesAPersonalDomainRemoval holds the personal tenant
// as a single-member space: a removal of any membership in it is refused, even
// when another tenant-administrator binding would satisfy the last-admin floor.
func TestTheMembershipGuardRefusesAPersonalDomainRemoval(t *testing.T) {
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	personalID := personalIDPrefix + "acct-1"
	manager.bindings[personalID] = []authz.Binding{
		{Tenant: personalID, Account: "acct-2", Role: authz.TenantAdminRole},
	}
	product := tenancyProductOf(newFakeRoster(), newFakeMemberships(), manager, newFakeSeeder(grants), nil)

	err := product.CheckMembershipRemoval(context.Background(), authn.AccountID("acct-2"), personalID)
	if !errors.Is(err, authn.ErrLastTenantAdmin) {
		t.Fatalf("CheckMembershipRemoval in a personal domain = %v, want a refusal", err)
	}
}

// TestTheMembershipGuardRefusesTheLastTenantAdmin holds the floor outside the
// personal prefix: the removal of a tenant's only tenant-administrator is
// refused, while another administrator or a role that is not tenant-admin keeps
// it satisfied.
func TestTheMembershipGuardRefusesTheLastTenantAdmin(t *testing.T) {
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	manager.bindings["acme"] = []authz.Binding{
		{Tenant: "acme", Account: "acct-1", Role: authz.TenantAdminRole},
	}
	product := tenancyProductOf(newFakeRoster(), newFakeMemberships(), manager, newFakeSeeder(grants), nil)
	ctx := context.Background()

	err := product.CheckMembershipRemoval(ctx, authn.AccountID("acct-1"), "acme")
	if !errors.Is(err, authn.ErrLastTenantAdmin) {
		t.Fatalf("CheckMembershipRemoval of the last tenant administrator = %v, want ErrLastTenantAdmin", err)
	}

	manager.bindings["acme"] = append(manager.bindings["acme"],
		authz.Binding{Tenant: "acme", Account: "acct-2", Role: authz.TenantAdminRole})
	if err := product.CheckMembershipRemoval(ctx, authn.AccountID("acct-1"), "acme"); err != nil {
		t.Errorf("CheckMembershipRemoval beside another tenant administrator = %v, want nil", err)
	}

	manager.bindings["beta"] = []authz.Binding{
		{Tenant: "beta", Account: "acct-3", Role: "viewer"},
	}
	if err := product.CheckMembershipRemoval(ctx, authn.AccountID("acct-3"), "beta"); err != nil {
		t.Errorf("CheckMembershipRemoval of an account with no tenant-admin binding = %v, want nil", err)
	}
}

// TestTheHostModulesProvideTheTwoGuards holds the wiring: the tenancy module
// delivers the membership guard, and a module of its own delivers the policy
// write guard, because one Go type cannot carry both Denied methods.
func TestTheHostModulesProvideTheTwoGuards(t *testing.T) {
	provides := func(module core.Module, token any) bool {
		want := reflect.TypeOf(token)
		for _, provision := range module.Provides {
			if reflect.TypeOf(provision.Token) == want {
				return true
			}
		}
		return false
	}
	if !provides(tenancyModule(), (*authn.MembershipGuard)(nil)) {
		t.Error("the tenancy module does not provide authn.MembershipGuard")
	}
	if !provides(policyWriteGuardModule(), (*authz.WriteGuard)(nil)) {
		t.Error("the policy write guard module does not provide authz.WriteGuard")
	}
	if !strings.Contains(policyWriteGuardModuleName, tenancyModuleName) {
		t.Errorf("the policy write guard module is named %q, want a name under %q",
			policyWriteGuardModuleName, tenancyModuleName)
	}
}

// TestTheProvisionerRefusesATakenIdentifierWithoutTouchingItsPolicy holds the
// check ahead of every policy write: a tenant the roster already holds is
// ErrTenantExists, and its policy rows are not touched.
func TestTheProvisionerRefusesATakenIdentifierWithoutTouchingItsPolicy(t *testing.T) {
	roster := newFakeRoster()
	roster.rows["acme"] = tenant.Tenant{ID: "acme", Alias: "acme", Status: tenant.StatusActive}
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	seeder := newFakeSeeder(grants)
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	product := tenancyProductOf(roster, newFakeMemberships(), manager, seeder, catalog)

	err := product.Provision(context.Background(),
		tenant.Tenant{ID: "acme", Alias: "acme", Status: tenant.StatusActive}, "acct-1")
	if !errors.Is(err, tenant.ErrTenantExists) {
		t.Fatalf("Provision of a taken identifier = %v, want ErrTenantExists", err)
	}
	roles, _ := manager.Roles(context.Background(), "acme")
	if len(roles) != 0 {
		t.Errorf("the refused provision wrote %d role(s) into the existing tenant", len(roles))
	}
	bindings, _ := manager.Bindings(context.Background(), "acme")
	if len(bindings) != 0 {
		t.Errorf("the refused provision wrote %d binding(s) into the existing tenant", len(bindings))
	}
}
