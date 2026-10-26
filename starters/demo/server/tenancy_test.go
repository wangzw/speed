package main

// The host tenancy seams: the membership checker the platform administrator
// binding asks, the write guard protecting the tenants an operator may not
// take out of service, and the provisioner that opens a tenant together with
// its first tenant administrator.

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
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
// place rather than tear the domain down.
func TestTheProvisionerLeavesTheWinnersPolicyWhenItLosesTheRace(t *testing.T) {
	roster := newFakeRoster()
	roster.rows["acme"] = tenant.Tenant{ID: "acme", Alias: "acme", Status: tenant.StatusActive}
	roster.lagging["acme"] = true
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
	product := tenancyProductOf(roster, newFakeMemberships(), manager, seeder, catalog)

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
	found := false
	for _, binding := range bindings {
		if binding.Account == "winner" && binding.Role == authz.TenantAdminRole {
			found = true
		}
	}
	if !found {
		t.Errorf("the winner's binding was torn down: %+v", bindings)
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
