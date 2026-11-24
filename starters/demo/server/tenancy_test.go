package main

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	authzpolicy "github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	tenantpersonal "github.com/vislake/speed/pkg/tenant/personal"
	tenantprofile "github.com/vislake/speed/pkg/tenant/profile"
)

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
		accounts:    newFakeAccounts(),
		manager:     manager,
		seeder:      seeder,
		catalog:     catalog,
		removalLock: &tenantRemovalLock{dialect: db.SQLite, byTenant: make(map[string]chan struct{})},
		logger:      log.Default(),
	}
}

type fakeAccounts struct {
	authn.Accounts
	byID        map[authn.AccountID]authn.Account
	identifiers map[authn.AccountID][]authn.Identifier
	reads       []authn.AccountID
	batches     int
	err         error
}

func newFakeAccounts() *fakeAccounts {
	return &fakeAccounts{
		byID:        map[authn.AccountID]authn.Account{},
		identifiers: map[authn.AccountID][]authn.Identifier{},
	}
}

func (a *fakeAccounts) IdentifiersMany(_ context.Context, ids []authn.AccountID) (map[authn.AccountID][]authn.Identifier, error) {
	a.batches++
	if a.err != nil {
		return nil, a.err
	}
	out := make(map[authn.AccountID][]authn.Identifier, len(ids))
	for _, id := range ids {
		if _, ok := a.byID[id]; !ok {
			continue
		}
		out[id] = append([]authn.Identifier{}, a.identifiers[id]...)
	}
	return out, nil
}

func (a *fakeAccounts) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	a.reads = append(a.reads, id)
	if a.err != nil {
		return authn.Account{}, a.err
	}
	held, ok := a.byID[id]
	if !ok {
		return authn.Account{}, authn.ErrAccountNotFound
	}
	return held, nil
}

type errorMemberships struct {
	authn.Memberships
	err error
}

func (m errorMemberships) Membership(context.Context, string, authn.AccountID) (authn.Membership, bool, error) {
	return authn.Membership{}, false, m.err
}

// TestTheMembershipCheckerReadsTheMembershipSeam holds that an active membership answers true, an
// absent one false, and an unparseable subject false rather than a failure.
func TestTheMembershipCheckerReadsTheMembershipSeam(t *testing.T) {
	t.Parallel()
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

// TestTheIdentityReaderResolvesDisplayNamesAndActivity holds that activity
// needs an active account and a membership, and a static principal is active.
func TestTheIdentityReaderResolvesDisplayNamesAndActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	memberships := newFakeMemberships()
	for account, active := range map[authn.AccountID]bool{
		"acct-1": true, "acct-2": true, "acct-3": true, "acct-4": false,
	} {
		if err := memberships.Put(ctx, authn.Membership{
			Account: account, Tenant: "acme", Active: active, Source: "admin",
		}); err != nil {
			t.Fatalf("writing the stand-in membership of %q: %v", account, err)
		}
	}
	accounts := newFakeAccounts()
	accounts.byID["acct-1"] = authn.Account{ID: "acct-1", Status: authn.StatusActive, DisplayName: "Alice"}
	accounts.byID["acct-2"] = authn.Account{ID: "acct-2", Status: authn.StatusDisabled, DisplayName: "Bob"}
	accounts.byID["acct-4"] = authn.Account{ID: "acct-4", Status: authn.StatusActive, DisplayName: "Carol"}
	accounts.identifiers["acct-1"] = []authn.Identifier{
		{Kind: authn.IdentifierEmail, Value: "alice@example.com"},
		{Kind: authn.IdentifierUsername, Value: "alice"},
	}
	accounts.identifiers["acct-2"] = []authn.Identifier{
		{Kind: authn.IdentifierEmail, Value: "bob@example.com"},
	}
	accounts.identifiers["acct-4"] = []authn.Identifier{
		{Kind: authn.IdentifierUsername, Value: "carol"},
	}
	product := tenancyProductOf(newFakeRoster(), memberships, nil, nil, nil)
	product.accounts = accounts

	got, err := product.Identities(ctx, "acme", []string{
		"acct-1", "acct-2", "acct-3", "acct-4", "static.operator", "not an account id",
	})
	if err != nil {
		t.Fatalf("Identities: %v", err)
	}
	want := map[string]authz.AccountIdentity{
		"acct-1":          {DisplayName: "Alice", Username: "alice", Active: true},
		"acct-2":          {DisplayName: "Bob", Active: false},
		"acct-4":          {DisplayName: "Carol", Username: "carol", Active: false},
		"static.operator": {Active: true},
	}
	if len(got) != len(want) {
		t.Fatalf("Identities resolved %v, want %v", got, want)
	}
	for account, identity := range want {
		if got[account] != identity {
			t.Errorf("Identities[%q] = %+v, want %+v", account, got[account], identity)
		}
	}
	for _, account := range []string{"acct-3", "not an account id"} {
		if _, ok := got[account]; ok {
			t.Errorf("Identities answered %q, which names no account this host knows", account)
		}
	}
	if accounts.batches != 1 {
		t.Errorf("the identity read made %d identifier batch reads, want one for the whole page", accounts.batches)
	}
}

// TestTheIdentityReaderLeavesAnUnreadableAnswerToTheCaller holds that a store that cannot be read is
// an error, never an empty or inactive set.
func TestTheIdentityReaderLeavesAnUnreadableAnswerToTheCaller(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	accounts := newFakeAccounts()
	accounts.byID["acct-1"] = authn.Account{ID: "acct-1", Status: authn.StatusActive}
	accounts.err = errors.New("the account table is unavailable")
	product := tenancyProductOf(newFakeRoster(), newFakeMemberships(), nil, nil, nil)
	product.accounts = accounts
	if _, err := product.Identities(ctx, "acme", []string{"acct-1"}); err == nil {
		t.Error("Identities answered an unreadable account store with no error")
	}

	accounts.err = nil
	product.memberships = errorMemberships{err: errors.New("the membership table is unavailable")}
	if _, err := product.Identities(ctx, "acme", []string{"acct-1"}); err == nil {
		t.Error("Identities answered an unreadable membership store with no error")
	}
}

// TestTheIdentityReaderOfNoAccountsIsAnEmptyMap holds that a role with no binding gets an empty map,
// not nil.
func TestTheIdentityReaderOfNoAccountsIsAnEmptyMap(t *testing.T) {
	t.Parallel()
	got, err := tenancyProductOf(newFakeRoster(), newFakeMemberships(), nil, nil, nil).
		Identities(context.Background(), "acme", nil)
	if err != nil {
		t.Fatalf("Identities: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("Identities of no accounts = %v, want an empty map", got)
	}
}

// TestTheWriteGuardProtectsTheSystemTenantAndPersonalTenants holds the two protected classes apart
// from an ordinary tenant for both protected writes.
func TestTheWriteGuardProtectsTheSystemTenantAndPersonalTenants(t *testing.T) {
	t.Parallel()
	product := tenancyProductOf(newFakeRoster(), newFakeMemberships(), nil, nil, nil)
	ctx := context.Background()
	for _, id := range []tenant.ID{
		tenant.ID(authz.PlatformDomain),
		tenant.ID(tenantpersonal.IDPrefix + "acct-1"),
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

// TestTheProvisionerWritesTheTenantAdminAndTheCreatorBinding holds that Provision leaves the role,
// governed grants, roster row, and the creator's membership and binding.
func TestTheProvisionerWritesTheTenantAdminAndTheCreatorBinding(t *testing.T) {
	t.Parallel()
	roster := newFakeRoster()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	seeder := newFakeSeeder(grants)
	catalog := fixedCatalog{
		pairs: map[authz.Permission][2]string{
			"crm.deal.read":  {"deal", "read"},
			"crm.deal.write": {"deal", "write"},
		},
		governed: map[authz.Permission]bool{
			"crm.deal.read":  true,
			"crm.deal.write": true,
		},
	}
	product := tenancyProductOf(roster, newFakeMemberships(), manager, seeder, catalog)

	record := tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}
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
		t.Errorf("the tenant-admin holds %d grant(s), want the two governed pairs", len(held))
	}
	bindings, _ := manager.BindingsOf(context.Background(), "acme", "acct-1")
	if len(bindings) != 1 || bindings[0].Role != authz.TenantAdminRole {
		t.Errorf("the creator's bindings = %+v, want one tenant-admin", bindings)
	}
}

// TestTheProvisionerWritesTheCreatorMembership holds that opening a tenant writes the creator's
// membership together with the tenant-admin binding.
func TestTheProvisionerWritesTheCreatorMembership(t *testing.T) {
	t.Parallel()
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
		tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}, "acct-1"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	member, err := memberships.IsMember(context.Background(), authn.AccountID("acct-1"), "acme")
	if err != nil {
		t.Fatalf("reading the creator's membership: %v", err)
	}
	if !member {
		t.Error("the creator is not an active member of the tenant it created")
	}
	held, found, err := memberships.Membership(context.Background(), "acme", "acct-1")
	if err != nil || !found {
		t.Fatalf("reading the creator's membership row: found=%t err=%v", found, err)
	}
	if held.Source != tenantCreatorMembershipSource {
		t.Errorf("the creator's membership source is %q, want %q", held.Source, tenantCreatorMembershipSource)
	}
}

// TestTheProvisionerWritesTheRosterRowBeforeTheCreatorRows holds the order: the role, its grants, the
// roster row, the builtin mark, then the creator's rows.
func TestTheProvisionerWritesTheRosterRowBeforeTheCreatorRows(t *testing.T) {
	t.Parallel()
	steps := &stepLog{}
	roster := newFakeRoster()
	roster.steps = steps
	memberships := newFakeMemberships()
	memberships.steps = steps
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	manager.steps = steps
	seeder := newFakeSeeder(grants)
	seeder.steps = steps
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	product := tenancyProductOf(roster, memberships, manager, seeder, catalog)

	if err := product.Provision(context.Background(),
		tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}, "acct-1"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	want := []string{
		"role:tenant-admin",
		"seed:tenant-admin",
		"create",
		"mark:tenant-admin",
		"put:acct-1",
		"bind:acct-1:tenant-admin",
	}
	if got := steps.snapshot(); !slices.Equal(got, want) {
		t.Errorf("the provision wrote\n%v\nwant\n%v", got, want)
	}
}

// TestTheProvisionerRetriesTheBuiltinMarkOnce holds that a first marking failure is retried and a
// successful retry completes the tenant.
func TestTheProvisionerRetriesTheBuiltinMarkOnce(t *testing.T) {
	t.Parallel()
	roster := newFakeRoster()
	memberships := newFakeMemberships()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	manager.markBuiltinFailure = 1
	manager.markBuiltinErr = errors.New("the policy store is unavailable")
	seeder := newFakeSeeder(grants)
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	product := tenancyProductOf(roster, memberships, manager, seeder, catalog)

	if err := product.Provision(context.Background(),
		tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}, "acct-1"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if manager.markBuiltinCalls != 2 {
		t.Errorf("the role was marked %d time(s), want the first attempt and one retry", manager.markBuiltinCalls)
	}
	roles, _ := manager.Roles(context.Background(), "acme")
	if len(roles) != 1 || !roles[0].Builtin {
		t.Errorf("the tenant's roles = %+v, want one builtin tenant-admin after the retry", roles)
	}
	member, _ := memberships.IsMember(context.Background(), authn.AccountID("acct-1"), "acme")
	if !member {
		t.Error("the successful retry did not finish the creator's membership")
	}
	bindings, _ := manager.BindingsOf(context.Background(), "acme", "acct-1")
	if len(bindings) != 1 {
		t.Errorf("the creator's bindings = %+v, want one", bindings)
	}
}

// TestTheProvisionerReportsTheBuiltinFailureWithoutCreatorRows holds that after one retry the error is
// returned with no membership or binding written.
func TestTheProvisionerReportsTheBuiltinFailureWithoutCreatorRows(t *testing.T) {
	t.Parallel()
	roster := newFakeRoster()
	memberships := newFakeMemberships()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	manager.markBuiltinFailure = 2
	manager.markBuiltinErr = errors.New("the policy store is unavailable")
	seeder := newFakeSeeder(grants)
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	product := tenancyProductOf(roster, memberships, manager, seeder, catalog)

	err := product.Provision(context.Background(),
		tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}, "acct-1")
	if err == nil {
		t.Fatal("Provision reported success after the builtin mark failed twice")
	}
	if manager.markBuiltinCalls != 2 {
		t.Errorf("the role was marked %d time(s), want the first attempt and one retry", manager.markBuiltinCalls)
	}
	if _, found, _ := roster.Get(context.Background(), "acme"); !found {
		t.Error("the tenant was not left registered after the builtin mark failed")
	}
	roles, _ := manager.Roles(context.Background(), "acme")
	if len(roles) != 1 || roles[0].Role != authz.TenantAdminRole || roles[0].Builtin {
		t.Errorf("the tenant's roles = %+v, want one non-builtin tenant-admin left for repair", roles)
	}
	if member, _ := memberships.IsMember(context.Background(), authn.AccountID("acct-1"), "acme"); member {
		t.Error("the failed provision wrote the creator's membership")
	}
	if bindings, _ := manager.Bindings(context.Background(), "acme"); len(bindings) != 0 {
		t.Errorf("the failed provision wrote bindings: %+v", bindings)
	}
}

// TestTheProvisionerLeavesTheWinnersPolicyWhenItLosesTheRace holds that the losing call of a
// roster-row race writes no creator rows and leaves the winner's policy in place.
func TestTheProvisionerLeavesTheWinnersPolicyWhenItLosesTheRace(t *testing.T) {
	t.Parallel()
	roster := newFakeRoster()
	roster.rows["acme"] = tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}
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
		tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}, "acct-loser")
	if !errors.Is(err, tenant.ErrTenantExists) {
		t.Fatalf("Provision that lost the race = %v, want ErrTenantExists", err)
	}
	if manager.markBuiltinCalls != 0 {
		t.Errorf("the losing provision marked the role builtin %d time(s), want none: the role belongs to the winner",
			manager.markBuiltinCalls)
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

// TestTheProvisionerMarksTheTenantAdminRoleBuiltin holds that the provisioned role is frozen against
// management writes.
func TestTheProvisionerMarksTheTenantAdminRoleBuiltin(t *testing.T) {
	t.Parallel()
	roster := newFakeRoster()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	seeder := newFakeSeeder(grants)
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	product := tenancyProductOf(roster, newFakeMemberships(), manager, seeder, catalog)

	if err := product.Provision(context.Background(),
		tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}, "acct-1"); err != nil {
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

// TestTheProvisionerLeavesAPersonalPrefixesRoleUnfrozen holds that a tenant opened with a
// personal-prefixed identifier keeps an unfrozen role.
func TestTheProvisionerLeavesAPersonalPrefixesRoleUnfrozen(t *testing.T) {
	t.Parallel()
	roster := newFakeRoster()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	seeder := newFakeSeeder(grants)
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	product := tenancyProductOf(roster, newFakeMemberships(), manager, seeder, catalog)

	id := tenant.ID(tenantpersonal.IDPrefix + "acme")
	if err := product.Provision(context.Background(),
		tenant.Tenant{ID: id, Name: "acme", Status: tenant.StatusActive}, "acct-1"); err != nil {
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

// TestTheProvisionerAnswersATakenIdentifierWhenTheWinnerFrozeTheRole holds that a builtin-role refusal
// on a registered identifier answers ErrTenantExists.
func TestTheProvisionerAnswersATakenIdentifierWhenTheWinnerFrozeTheRole(t *testing.T) {
	t.Parallel()
	roster := newFakeRoster()
	roster.rows["acme"] = tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}
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
		tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}, "acct-loser")
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

// TestTheProvisionerCleansItsPolicyWhenTheRosterRefuses holds that a refused roster row leaves no
// role, grant, binding or membership behind.
func TestTheProvisionerCleansItsPolicyWhenTheRosterRefuses(t *testing.T) {
	t.Parallel()
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
		tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}, "acct-1")
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

// TestTheMembershipCheckerTrustsAConfiguredStaticPrincipal holds that a static credential subject is a
// member without a membership row.
func TestTheMembershipCheckerTrustsAConfiguredStaticPrincipal(t *testing.T) {
	t.Parallel()
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
	member, err := product.IsMember(context.Background(), "system", "not an account id")
	if err != nil {
		t.Fatalf("IsMember of an unparseable subject: %v", err)
	}
	if member {
		t.Error("a subject outside the account-identifier domain read as a member")
	}
}

// TestThePolicyWriteGuardRefusesEveryPersonalDomainWrite holds that every policy write in a
// personal-prefixed domain is refused and other domains are left alone.
func TestThePolicyWriteGuardRefusesEveryPersonalDomainWrite(t *testing.T) {
	t.Parallel()
	guard := policyWriteGuard{}
	ctx := context.Background()
	ops := []authz.WriteOp{authz.WriteRole, authz.WriteGrant, authz.WriteBinding}
	for _, op := range ops {
		for _, domain := range []string{tenantpersonal.IDPrefix + "acct-1", tenantpersonal.IDPrefix} {
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

// TestTheSettingsWriteGuardRefusesAPersonalDomainWrite holds that a tenant settings write in a
// personal-prefixed domain is refused.
func TestTheSettingsWriteGuardRefusesAPersonalDomainWrite(t *testing.T) {
	t.Parallel()
	guard := settingsWriteGuard{}
	ctx := context.Background()
	for _, domain := range []string{tenantpersonal.IDPrefix + "acct-1", tenantpersonal.IDPrefix} {
		denied, err := guard.Denied(ctx, domain)
		if err != nil {
			t.Fatalf("Denied(%q): %v", domain, err)
		}
		if !denied {
			t.Errorf("Denied(%q) = false, want the personal domain refused", domain)
		}
	}
	for _, domain := range []string{"acme", string(authz.PlatformDomain)} {
		denied, err := guard.Denied(ctx, domain)
		if err != nil {
			t.Fatalf("Denied(%q): %v", domain, err)
		}
		if denied {
			t.Errorf("Denied(%q) = true, want an ordinary domain allowed", domain)
		}
	}
}

// TestTheMembershipGuardRefusesAPersonalDomainRemoval holds that any membership removal in a personal
// tenant is refused.
func TestTheMembershipGuardRefusesAPersonalDomainRemoval(t *testing.T) {
	t.Parallel()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	personalID := tenantpersonal.IDPrefix + "acct-1"
	manager.bindings[personalID] = []authz.Binding{
		{Tenant: personalID, Account: "acct-2", Role: authz.TenantAdminRole},
	}
	product := tenancyProductOf(newFakeRoster(), newFakeMemberships(), manager, newFakeSeeder(grants), nil)

	err := product.CheckMembershipRemoval(context.Background(), authn.AccountID("acct-2"), personalID)
	if !errors.Is(err, authn.ErrLastTenantAdmin) {
		t.Fatalf("CheckMembershipRemoval in a personal domain = %v, want a refusal", err)
	}
}

// TestTheMembershipGuardRefusesTheLastTenantAdmin holds that removing a tenant's only
// tenant-administrator is refused.
func TestTheMembershipGuardRefusesTheLastTenantAdmin(t *testing.T) {
	t.Parallel()
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

// TestTheHostModulesProvideTheGuards holds that the tenancy module delivers the membership guard and
// separate modules deliver the two write guards.
func TestTheHostModulesProvideTheGuards(t *testing.T) {
	t.Parallel()
	provides := func(module core.Module, token any) bool {
		want := reflect.TypeOf(token)
		for _, provision := range module.Provides {
			if reflect.TypeOf(provision.Token) == want {
				return true
			}
		}
		return false
	}
	requires := func(module core.Module, token any) bool {
		want := reflect.TypeOf(token)
		for _, requirement := range module.Requires {
			if reflect.TypeOf(requirement.Token) == want {
				return true
			}
		}
		return false
	}
	if !provides(tenancyModule(), (*authn.MembershipGuard)(nil)) {
		t.Error("the tenancy module does not provide authn.MembershipGuard")
	}
	if !provides(tenancyModule(), (*authz.AccountIdentityReader)(nil)) {
		t.Error("the tenancy module does not provide authz.AccountIdentityReader")
	}
	if !requires(tenancyModule(), (*authn.Accounts)(nil)) {
		t.Error("the tenancy module does not require authn.Accounts for its identity read")
	}
	if !provides(policyWriteGuardModule(), (*authz.WriteGuard)(nil)) {
		t.Error("the policy write guard module does not provide authz.WriteGuard")
	}
	if !provides(settingsWriteGuardModule(), (*authz.WriteSettings)(nil)) {
		t.Error("the settings write guard module does not provide authz.WriteSettings")
	}
	if !strings.Contains(policyWriteGuardModuleName, tenancyModuleName) {
		t.Errorf("the policy write guard module is named %q, want a name under %q",
			policyWriteGuardModuleName, tenancyModuleName)
	}
	if !strings.Contains(settingsWriteGuardModuleName, tenancyModuleName) {
		t.Errorf("the settings write guard module is named %q, want a name under %q",
			settingsWriteGuardModuleName, tenancyModuleName)
	}
}

// TestTheBaselineAssemblesTheTenantAdminCounter holds that the policy module is in the baseline serve
// roster and provides authz.TenantAdminCounter.
func TestTheBaselineAssemblesTheTenantAdminCounter(t *testing.T) {
	t.Parallel()
	modules := assembledDescriptors(t)
	policy, ok := modules[authzpolicy.ModuleName]
	if !ok {
		t.Fatalf("the baseline serve roster does not assemble %q", authzpolicy.ModuleName)
	}
	want := reflect.TypeOf((*authz.TenantAdminCounter)(nil))
	for _, token := range policy.provides {
		if reflect.TypeOf(token) == want {
			return
		}
	}
	t.Errorf("%q is in the baseline roster but does not provide authz.TenantAdminCounter: "+
		"the account deletion would lose the last-administrator guard", authzpolicy.ModuleName)
}

// TestTheBaselineServesTheOrganizationProfile holds that the profile module is in the baseline serve
// roster and its feature is declared.
func TestTheBaselineServesTheOrganizationProfile(t *testing.T) {
	t.Parallel()
	if !slices.Contains(baselineServe, tenantprofile.ModuleName) {
		t.Fatalf("the baseline serve roster does not name %q: the demo would serve no "+
			"organization profile route", tenantprofile.ModuleName)
	}
	declared, err := declaredVocabulary()
	if err != nil {
		t.Fatalf("reading this binary's declarations: %v", err)
	}
	for _, entry := range declared {
		if entry.Name == "tenant.profile" && entry.Module == tenantprofile.ModuleName {
			return
		}
	}
	t.Errorf("this binary does not declare the feature tenant.profile under %q: the frontend "+
		"cannot discover the organization profile surface", tenantprofile.ModuleName)
}

// TestTheProvisionerRefusesATakenIdentifierWithoutTouchingItsPolicy holds that a tenant the roster
// already holds is refused before any policy write.
func TestTheProvisionerRefusesATakenIdentifierWithoutTouchingItsPolicy(t *testing.T) {
	t.Parallel()
	roster := newFakeRoster()
	roster.rows["acme"] = tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	seeder := newFakeSeeder(grants)
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	product := tenancyProductOf(roster, newFakeMemberships(), manager, seeder, catalog)

	err := product.Provision(context.Background(),
		tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}, "acct-1")
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
