package policy

import (
	"cmp"
	"errors"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

func grantedPairs(t *testing.T, f *fixture, tenant string, role authz.Role) []authz.Grant {
	t.Helper()
	held, err := f.Grants(t.Context(), tenant, role)
	if err != nil {
		t.Fatalf("Grants(%s, %s): %v", tenant, role, err)
	}
	pairs := make([]authz.Grant, len(held))
	for i, g := range held {
		pairs[i] = authz.Grant{Resource: g.Resource, Action: g.Action}
	}
	slices.SortFunc(pairs, func(a, b authz.Grant) int {
		if a.Resource != b.Resource {
			return cmp.Compare(a.Resource, b.Resource)
		}
		return cmp.Compare(a.Action, b.Action)
	})
	return pairs
}

// TestSeedingGrowsABuiltinRolesGrantsAndTheManagerStaysRefused checks that seeding grows a builtin roles grants and the manager stays refused.
func TestSeedingGrowsABuiltinRolesGrantsAndTheManagerStaysRefused(t *testing.T) {
	f := newFixture(t)
	f.membership = &membershipStub{member: true}
	ctx := t.Context()
	const tenant = "system"
	role := authz.Role("platform-admin")

	if err := f.PutRole(ctx, authz.RoleRecord{Tenant: tenant, Role: role, DescriptionKey: "k"}); err != nil {
		t.Fatalf("PutRole: %v", err)
	}
	mustPutGrant(t, ctx, f, tenant, string(role), "account", "read")
	if err := f.MarkRoleBuiltin(ctx, tenant, role); err != nil {
		t.Fatalf("MarkRoleBuiltin: %v", err)
	}
	if err := f.PutBinding(ctx, authz.Binding{Tenant: tenant, Account: "alice", Role: role}); err != nil {
		t.Fatalf("PutBinding: %v", err)
	}

	added, err := f.SeedGrants(ctx, tenant, role, []authz.Grant{
		{Resource: "deal", Action: "read"},
	})
	if err != nil {
		t.Fatalf("SeedGrants: %v", err)
	}
	if added != 1 {
		t.Errorf("SeedGrants added %d, want one", added)
	}

	added, err = f.SeedGrants(ctx, tenant, role, []authz.Grant{
		{Resource: "account", Action: "read"},
		{Resource: "deal", Action: "read"},
	})
	if err != nil {
		t.Fatalf("SeedGrants(second): %v", err)
	}
	if added != 0 {
		t.Errorf("the second SeedGrants added %d, want zero", added)
	}

	want := []authz.Grant{
		{Resource: "account", Action: "read"},
		{Resource: "deal", Action: "read"},
	}
	if got := grantedPairs(t, f, tenant, role); !slices.Equal(got, want) {
		t.Errorf("the role holds %+v, want %+v", got, want)
	}

	allowed, err := f.Allowed(ctx, authz.Query{
		Subject: "alice", Domain: authz.Domain(tenant), Object: "deal", Action: "read",
	})
	if err != nil {
		t.Fatalf("Allowed: %v", err)
	}
	if !allowed {
		t.Error("the seeded grant does not decide; the snapshot was not reloaded")
	}

	refused := []struct {
		name string
		call func() error
	}{
		{"PutGrant", func() error {
			return f.PutGrant(ctx, authz.GrantRecord{
				Tenant: tenant, Role: role, Resource: "deal", Action: "write",
			})
		}},
		{"DeleteGrant", func() error {
			return f.DeleteGrant(ctx, authz.GrantRecord{
				Tenant: tenant, Role: role, Resource: "deal", Action: "read",
			})
		}},
		{"PutRole", func() error {
			return f.PutRole(ctx, authz.RoleRecord{Tenant: tenant, Role: role, DescriptionKey: "rewritten"})
		}},
		{"DeleteRole", func() error {
			return f.DeleteRole(ctx, tenant, role)
		}},
	}
	for _, c := range refused {
		if err := c.call(); !errors.Is(err, authz.ErrBuiltinRole) {
			t.Errorf("%s on a builtin role gave %v, want ErrBuiltinRole", c.name, err)
		}
	}
	if got := grantedPairs(t, f, tenant, role); !slices.Equal(got, want) {
		t.Errorf("after the refused writes the role holds %+v, want %+v", got, want)
	}
}

// TestSeedingRefusesAnUnknownRoleAndAnEmptyTenant checks that seeding refuses an unknown role and an empty tenant.
func TestSeedingRefusesAnUnknownRoleAndAnEmptyTenant(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	added, err := f.SeedGrants(ctx, "acme", "ghost", []authz.Grant{{Resource: "deal", Action: "read"}})
	if !errors.Is(err, authz.ErrUnknownRole) {
		t.Errorf("SeedGrants for an unknown role gave %v, want ErrUnknownRole", err)
	}
	if added != 0 {
		t.Errorf("SeedGrants for an unknown role reported %d added, want zero", added)
	}

	if _, err = f.SeedGrants(ctx, "", "ghost", []authz.Grant{{Resource: "deal", Action: "read"}}); !errors.Is(err, authz.ErrTenantMismatch) {
		t.Errorf("SeedGrants with no tenant gave %v, want ErrTenantMismatch", err)
	}
}
