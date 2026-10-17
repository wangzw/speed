package policy

import (
	"cmp"
	"errors"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

// grantedPairs returns the (resource, action) pairs of the grants a role holds,
// sorted so that a case compares sets and not row order.
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

// Seeding adds a grant the role does not hold, including on a builtin role,
// and a repeated seed adds nothing. The manager's own grant writes on the same
// role stay refused, which is the path the management surface takes, and the
// refusal covers deleting a grant the seed added: the seeding surface only
// grows the set.
func TestSeedingGrowsABuiltinRolesGrantsAndTheManagerStaysRefused(t *testing.T) {
	f := newFixture(t)
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
	// The binding lands before the seed, so the decision below observes the
	// seed's own reload and not a later write's.
	if err := f.PutBinding(ctx, authz.Binding{Tenant: tenant, Account: "alice", Role: role}); err != nil {
		t.Fatalf("PutBinding: %v", err)
	}

	// The permission a module declared after the mark is seeded in.
	added, err := f.SeedGrants(ctx, tenant, role, []authz.Grant{
		{Resource: "deal", Action: "read"},
	})
	if err != nil {
		t.Fatalf("SeedGrants: %v", err)
	}
	if added != 1 {
		t.Errorf("SeedGrants added %d, want one", added)
	}

	// A second run over the grown set writes nothing.
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

	// The seeded grant decides: the seed reloaded the snapshot.
	allowed, err := f.Allowed(ctx, authz.Query{
		Subject: "alice", Domain: authz.Domain(tenant), Object: "deal", Action: "read",
	})
	if err != nil {
		t.Fatalf("Allowed: %v", err)
	}
	if !allowed {
		t.Error("the seeded grant does not decide; the snapshot was not reloaded")
	}

	// The manager refuses the same writes on the builtin role, so the
	// management surface cannot use them to add, change or remove a grant.
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

// Seeding names a role that has to exist, and a tenant. Both refusals land
// before any row is written.
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
