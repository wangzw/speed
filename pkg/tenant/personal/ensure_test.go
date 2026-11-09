package personal

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// The ensure writes the same five pieces, and a second call changes nothing.
func TestEnsureIsIdempotent(t *testing.T) {
	p, roster, memberships, manager, _ := unitProduct()
	ctx := context.Background()

	first, err := p.ensure(ctx, unitAccount)
	if err != nil {
		t.Fatalf("the first ensure: %v", err)
	}
	if !first.Created {
		t.Errorf("the first ensure reports created=false, want true")
	}
	wantID := TenantID(unitAccount)
	if first.Tenant.ID != wantID {
		t.Errorf("the tenant is %q, want %q", first.Tenant.ID, wantID)
	}

	second, err := p.ensure(ctx, unitAccount)
	if err != nil {
		t.Fatalf("the second ensure: %v", err)
	}
	if second.Created {
		t.Errorf("the second ensure reports created=true, want false")
	}
	if roster.creates != 1 || len(memberships.rows[unitAccount]) != 1 ||
		len(manager.roles[string(wantID)]) != 1 || len(manager.bindings[string(wantID)]) != 1 {
		t.Errorf("the second ensure wrote duplicates: tenants=%d memberships=%v roles=%v bindings=%v",
			roster.creates, memberships.rows[unitAccount],
			manager.roles[string(wantID)], manager.bindings[string(wantID)])
	}
}

// The ensure names the tenant's name after the account's username.
func TestEnsureNamesTheNameAfterTheUsername(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	p.accounts = &fakeAccounts{identifiers: []authn.Identifier{
		{Kind: authn.IdentifierEmail, Value: "alice@example.com"},
		{Kind: authn.IdentifierUsername, Value: "alice"},
	}}
	if _, err := p.ensure(context.Background(), unitAccount); err != nil {
		t.Fatalf("the ensure: %v", err)
	}
	row := roster.rows[TenantID(unitAccount)]
	if row.Name != "alice" {
		t.Errorf("the name is %q, want the account's username", row.Name)
	}
}

// A username the ensure cannot read is a refusal, not a derived name: the
// personal tenant's name is always the account's username.
func TestEnsureRefusesWhenTheUsernameCannotBeRead(t *testing.T) {
	cases := []struct {
		name     string
		accounts accountReader
	}{
		{name: "no account read surface", accounts: nil},
		{name: "no username identifier", accounts: &fakeAccounts{identifiers: []authn.Identifier{
			{Kind: authn.IdentifierEmail, Value: "alice@example.com"},
		}}},
		{name: "empty username identifier", accounts: &fakeAccounts{identifiers: []authn.Identifier{
			{Kind: authn.IdentifierUsername, Value: ""},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, roster, _, _, _ := unitProduct()
			p.accounts = tc.accounts
			_, err := p.ensure(context.Background(), unitAccount)
			if !errors.Is(err, ErrUsernameMissing) {
				t.Fatalf("the ensure gave %v, want ErrUsernameMissing", err)
			}
			if len(roster.rows) != 0 {
				t.Errorf("the refused ensure wrote %v", roster.rows)
			}
		})
	}
}

// A name another tenant holds is the identifier-unavailable refusal.
func TestEnsureReportsAHeldName(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	p.accounts = &fakeAccounts{identifiers: []authn.Identifier{{Kind: authn.IdentifierUsername, Value: "alice"}}}
	roster.createErr = tenant.ErrNameConflict
	_, err := p.ensure(context.Background(), unitAccount)
	if !errors.Is(err, ErrIdentifierUnavailable) {
		t.Fatalf("the ensure gave %v, want ErrIdentifierUnavailable", err)
	}
}

// A disabled tenant is refused without a write.
func TestEnsureRefusesADisabledTenant(t *testing.T) {
	p, roster, memberships, manager, _ := unitProduct()
	id := TenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Name: "unit-user", Status: tenant.StatusDisabled}
	memberships.rows[unitAccount] = []authn.Membership{{Account: unitAccount, Tenant: string(id), Active: true}}
	before := len(manager.bindings[string(id)])
	_, err := p.ensure(context.Background(), unitAccount)
	if !errors.Is(err, ErrTenantUnavailable) {
		t.Fatalf("the ensure gave %v, want ErrTenantUnavailable", err)
	}
	if len(manager.bindings[string(id)]) != before {
		t.Errorf("the refused ensure wrote a binding")
	}
}

// A create this call lost to another call leaves a failed audit row beside the
// winner's tenant.
func TestEnsureClosesALostRaceWithAFailedAuditRow(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	id := TenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Name: "unit-user", Status: tenant.StatusActive}
	roster.creates = 0
	roster.createErr = tenant.ErrTenantExists
	result, err := p.ensure(context.Background(), unitAccount)
	if err != nil {
		t.Fatalf("the ensure: %v", err)
	}
	if result.Created {
		t.Errorf("the lost race reports created=true")
	}
	rows := entriesOfAction(p.audit.(*fakeAudit).rows(), personalCreateAction)
	if len(rows) != 2 || rows[0].Outcome != personalOutcomeSuccess || rows[1].Outcome != personalOutcomeFailed {
		t.Errorf("the create audit rows are %+v, want a success and a linked failure", rows)
	}
}

// The kind names a personal tenant's identifier, whoever asks: the platform
// roster lists every account's spaces, so another account's personal tenant
// must read personal too.
func TestKindOfNamesAPersonalTenantsIdentifier(t *testing.T) {
	p := &product{}
	for _, id := range []tenant.ID{TenantID(unitAccount), TenantID("another-account")} {
		if kind, err := p.KindOf(context.Background(), id); err != nil || kind != tenant.KindPersonal {
			t.Errorf("KindOf(%q) = (%v, %v), want personal", id, kind, err)
		}
	}
	for _, id := range []tenant.ID{"business", ""} {
		if kind, err := p.KindOf(context.Background(), id); err != nil || kind != tenant.KindBusiness {
			t.Errorf("KindOf(%q) = (%v, %v), want business", id, kind, err)
		}
	}
}

// The allowlist is the business set intersected with the governed set, less
// the management actions a single-person space must not have.
func TestTheAllowlistKeepsOnlyGovernedBusinessPermissions(t *testing.T) {
	kept := authz.Permission("crm.deal.read")
	excluded := authz.PermissionMembershipManage
	notGoverned := authz.Permission("crm.deal.delete")
	catalog := fakeCatalog{governed: []authz.Permission{kept, excluded}}
	got := personalAllowlist(catalog, []authz.Permission{kept, excluded, notGoverned})
	if len(got) != 1 || got[0] != kept {
		t.Errorf("the allowlist is %v, want only %q", got, kept)
	}
}

// Concurrent ensures with the same account collide on the deterministic
// identifier: one creates the tenant and the rest read it back.
func TestEnsureCreatesOneTenantUnderConcurrentCalls(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	const callers = 8
	results := make([]personalResult, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = p.ensure(context.Background(), unitAccount)
		}(i)
	}
	wg.Wait()

	created := 0
	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("caller %d failed: %v", i, errs[i])
		}
		if results[i].Tenant.ID != results[0].Tenant.ID {
			t.Errorf("caller %d answered %q, caller 0 answered %q", i, results[i].Tenant.ID, results[0].Tenant.ID)
		}
		if results[i].Created {
			created++
		}
	}
	if created != 1 {
		t.Errorf("%d callers report created=true, want exactly one", created)
	}
	if len(roster.rows) != 1 || roster.creates != 1 {
		t.Errorf("the roster holds %d row(s) after %d create(s), want one and one", len(roster.rows), roster.creates)
	}
}

// A roster snapshot that lags the table is caught up by EnterRegistered: the
// existing row is read back and no second tenant is created.
func TestEnsureCatchesTheRosterSnapshotUp(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	id := TenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Name: "unit-user", Status: tenant.StatusActive}
	roster.lagging[id] = true

	result, err := p.ensure(context.Background(), unitAccount)
	if err != nil {
		t.Fatalf("the ensure over a lagging snapshot: %v", err)
	}
	if result.Created {
		t.Errorf("the ensure reports created=true, want false: the table already held the row")
	}
	if result.Tenant.ID != id {
		t.Errorf("the ensure answered %q, want %q", result.Tenant.ID, id)
	}
	if roster.entered != 1 {
		t.Errorf("the roster was caught up %d time(s), want once", roster.entered)
	}
}

// The ensure converges the tenant-admin role's grants to the allowlist: every
// grant outside it is revoked. The skeleton's allowlist is empty, so all are.
func TestEnsureConvergesTheTenantAdminsGrantsToTheAllowlist(t *testing.T) {
	p, _, _, manager, seeder := unitProduct()
	ctx := context.Background()
	if _, err := p.ensure(ctx, unitAccount); err != nil {
		t.Fatalf("the first ensure: %v", err)
	}
	id := string(TenantID(unitAccount))
	outer := []authz.Grant{
		{Resource: "account", Action: "read"},
		{Resource: "object", Action: "read"},
	}
	seeder.grants.mu.Lock()
	for _, extra := range outer {
		seeder.grants.sets[id][personalTenantAdminRole][extra] = struct{}{}
	}
	seeder.grants.mu.Unlock()

	if _, err := p.ensure(ctx, unitAccount); err != nil {
		t.Fatalf("the second ensure: %v", err)
	}
	if manager.deleted != len(outer) {
		t.Errorf("the ensure revoked %d grant(s), want the %d outside the allowlist", manager.deleted, len(outer))
	}
	if held := seeder.grants.held(id, personalTenantAdminRole); len(held) != 0 {
		t.Errorf("the tenant-admin role holds %v, want the empty allowlist", held)
	}
}

// A tenant built under the old model is converged to the tenant-admin role:
// the ensure removes the legacy owner role with its grants and bindings.
func TestEnsureMigratesTheLegacyOwnerRole(t *testing.T) {
	p, roster, memberships, manager, seeder := unitProduct()
	ctx := context.Background()
	id := TenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Name: "unit-user", Status: tenant.StatusActive}
	memberships.rows[unitAccount] = []authn.Membership{
		{Account: unitAccount, Tenant: string(id), Active: true, Source: personalMemberSource},
	}
	legacy := authz.Role("owner")
	if err := manager.PutRole(ctx, authz.RoleRecord{Tenant: string(id), Role: legacy}); err != nil {
		t.Fatalf("writing the legacy role: %v", err)
	}
	if err := manager.PutBinding(ctx, authz.Binding{
		Tenant: string(id), Account: unitAccount.String(), Role: legacy,
	}); err != nil {
		t.Fatalf("writing the legacy binding: %v", err)
	}
	if _, err := seeder.SeedGrants(ctx, string(id), legacy, []authz.Grant{{Resource: "legacy", Action: "read"}}); err != nil {
		t.Fatalf("writing the legacy grant: %v", err)
	}

	if _, err := p.ensure(ctx, unitAccount); err != nil {
		t.Fatalf("the ensure over the legacy tenant: %v", err)
	}
	roles, err := manager.Roles(ctx, string(id))
	if err != nil {
		t.Fatalf("reading the tenant's roles: %v", err)
	}
	admin := false
	for _, role := range roles {
		if role.Role == legacy {
			t.Errorf("the legacy owner role survived the ensure: %+v", roles)
		}
		if role.Role == personalTenantAdminRole {
			admin = true
		}
	}
	if !admin {
		t.Errorf("the ensure did not write the tenant-admin role: %+v", roles)
	}
	if held := seeder.grants.held(string(id), legacy); len(held) != 0 {
		t.Errorf("the legacy role's grants survived: %v", held)
	}
	bindings, err := manager.Bindings(ctx, string(id))
	if err != nil {
		t.Fatalf("reading the tenant's bindings: %v", err)
	}
	for _, binding := range bindings {
		if binding.Role == legacy {
			t.Errorf("the legacy owner binding survived: %+v", bindings)
		}
	}
}
