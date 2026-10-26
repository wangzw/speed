package policy

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
)

// The cases here drive the management surface over the real policy tables: the
// read shapes and their per-tenant scoping, and the writes' refusals and
// effects. Each asserts a property a wrong implementation would not hold.

func mustPutRole(t *testing.T, ctx context.Context, f *fixture, tenant, role string) {
	t.Helper()
	if err := f.PutRole(ctx, authz.RoleRecord{Tenant: tenant, Role: authz.Role(role), DescriptionKey: "k"}); err != nil {
		t.Fatalf("PutRole(%s, %s): %v", tenant, role, err)
	}
}

func mustPutGrant(t *testing.T, ctx context.Context, f *fixture, tenant, role, object, action string) {
	t.Helper()
	if err := f.PutGrant(ctx, authz.GrantRecord{Tenant: tenant, Role: authz.Role(role), Resource: object, Action: action}); err != nil {
		t.Fatalf("PutGrant(%s, %s): %v", tenant, role, err)
	}
}

func mustPutBinding(t *testing.T, ctx context.Context, f *fixture, tenant, account, role string) {
	t.Helper()
	if err := f.PutBinding(ctx, authz.Binding{Tenant: tenant, Account: account, Role: authz.Role(role)}); err != nil {
		t.Fatalf("PutBinding(%s, %s, %s): %v", tenant, account, role, err)
	}
}

// Grants returns a role's grants within its tenant and no other's, with the
// fields mapped the way the table holds them.
func TestGrantsScopesToOneTenantRole(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", "roleA")
	mustPutRole(t, ctx, f, "acme", "roleB")
	mustPutRole(t, ctx, f, "globex", "roleA")
	mustPutGrant(t, ctx, f, "acme", "roleA", "account", "read")
	mustPutGrant(t, ctx, f, "acme", "roleA", "session", "revoke")
	mustPutGrant(t, ctx, f, "acme", "roleB", "audit", "read")
	mustPutGrant(t, ctx, f, "globex", "roleA", "settings", "write")

	grants, err := f.Grants(ctx, "acme", "roleA")
	if err != nil {
		t.Fatalf("Grants(acme, roleA): %v", err)
	}
	if len(grants) != 2 {
		t.Fatalf("Grants(acme, roleA) = %v, want the two grants of that one role", grants)
	}
	byObject := make(map[string]authz.GrantRecord, len(grants))
	for _, g := range grants {
		if g.Tenant != "acme" || g.Role != "roleA" {
			t.Errorf("a grant reads back as %+v, want tenant acme role roleA", g)
		}
		// The resource and the action are the row's own columns: swapping them
		// would put the action in the resource field.
		byObject[g.Resource] = g
	}
	if g, ok := byObject["account"]; !ok || g.Action != "read" {
		t.Errorf("the account grant reads back as %+v, want resource account action read", byObject["account"])
	}
	if g, ok := byObject["session"]; !ok || g.Action != "revoke" {
		t.Errorf("the session grant reads back as %+v, want resource session action revoke", byObject["session"])
	}

	// Another tenant's same-named role is not reached, and neither is a
	// different role of the same tenant.
	if other, err := f.Grants(ctx, "globex", "roleA"); err != nil || len(other) != 1 || other[0].Resource != "settings" {
		t.Errorf("Grants(globex, roleA) = %v, %v, want only that tenant's grant", other, err)
	}
}

// Bindings lists one tenant's bindings and no other's.
func TestBindingsListsOneTenant(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", "roleA")
	mustPutRole(t, ctx, f, "acme", "roleB")
	mustPutRole(t, ctx, f, "globex", "roleG")
	mustPutBinding(t, ctx, f, "acme", "acc1", "roleA")
	mustPutBinding(t, ctx, f, "acme", "acc2", "roleB")
	mustPutBinding(t, ctx, f, "globex", "acc1", "roleG")

	bindings, err := f.Bindings(ctx, "acme")
	if err != nil {
		t.Fatalf("Bindings(acme): %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("Bindings(acme) = %v, want the two bindings of that tenant", bindings)
	}
	for _, b := range bindings {
		if b.Tenant != "acme" {
			t.Errorf("a binding reads back with tenant %q, want acme", b.Tenant)
		}
	}
	if other, err := f.Bindings(ctx, "globex"); err != nil || len(other) != 1 || other[0].Role != "roleG" {
		t.Errorf("Bindings(globex) = %v, %v, want only that tenant's binding", other, err)
	}
}

// BindingsOf holds a subject's bindings to the named tenant: the same subject
// bound in two tenants reads back one row per tenant, not both.
func TestBindingsOfScopesToOneTenant(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", "roleA")
	mustPutRole(t, ctx, f, "globex", "roleG")
	mustPutBinding(t, ctx, f, "acme", "acc1", "roleA")
	mustPutBinding(t, ctx, f, "globex", "acc1", "roleG")

	bindings, err := f.BindingsOf(ctx, "acme", "acc1")
	if err != nil {
		t.Fatalf("BindingsOf(acme, acc1): %v", err)
	}
	if len(bindings) != 1 || bindings[0].Tenant != "acme" || bindings[0].Role != "roleA" {
		t.Fatalf("BindingsOf(acme, acc1) = %v, want only acme's row", bindings)
	}
}

// DeleteBindings is the domain teardown path: it removes the tenant's last
// tenant-admin binding, which DeleteBinding refuses, and leaves another
// tenant's bindings alone.
func TestDeleteBindingsClearsADomainIncludingTheLastTenantAdmin(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", string(authz.TenantAdminRole))
	mustPutRole(t, ctx, f, "globex", string(authz.TenantAdminRole))
	mustPutBinding(t, ctx, f, "acme", "acc1", string(authz.TenantAdminRole))
	mustPutBinding(t, ctx, f, "globex", "acc1", string(authz.TenantAdminRole))

	if err := f.DeleteBinding(ctx, "acme", "acc1", authz.TenantAdminRole); !errors.Is(err, authz.ErrLastTenantAdmin) {
		t.Fatalf("DeleteBinding of the last tenant-admin = %v, want ErrLastTenantAdmin", err)
	}
	if err := f.DeleteBindings(ctx, "acme"); err != nil {
		t.Fatalf("DeleteBindings(acme): %v", err)
	}
	if bindings, err := f.Bindings(ctx, "acme"); err != nil || len(bindings) != 0 {
		t.Errorf("Bindings(acme) = %v, %v, want none", bindings, err)
	}
	if other, err := f.Bindings(ctx, "globex"); err != nil || len(other) != 1 {
		t.Errorf("Bindings(globex) = %v, %v, want the other tenant's binding untouched", other, err)
	}
}

// Deleting a role revokes it: its grants and bindings go with its row, so the
// account it was bound to stops being allowed. The engine decides from the
// grant and binding tables and never reads the role table, so a leftover row
// would keep deciding for a role that no longer exists.
func TestDeleteRoleRevokesItsGrantsAndBindings(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", "roleA")
	mustPutGrant(t, ctx, f, "acme", "roleA", "account", "read")
	mustPutBinding(t, ctx, f, "acme", "acc1", "roleA")
	if !f.allow(t, "acc1", "acme") {
		t.Fatalf("the account was refused a granted permission before the delete")
	}

	if err := f.DeleteRole(ctx, "acme", "roleA"); err != nil {
		t.Fatalf("DeleteRole(acme, roleA): %v", err)
	}

	if roles, err := f.Roles(ctx, "acme"); err != nil || len(roles) != 0 {
		t.Errorf("Roles(acme) = %v, %v, want none", roles, err)
	}
	if grants, err := f.Grants(ctx, "acme", "roleA"); err != nil || len(grants) != 0 {
		t.Errorf("Grants(acme, roleA) = %v, %v, want none: a deleted role's grants must go with it", grants, err)
	}
	if bindings, err := f.BindingsOf(ctx, "acme", "acc1"); err != nil || len(bindings) != 0 {
		t.Errorf("BindingsOf(acme, acc1) = %v, %v, want none: a deleted role's bindings must go with it", bindings, err)
	}
	if f.allow(t, "acc1", "acme") {
		t.Errorf("a deleted role still decided: the account was allowed after its role was removed")
	}
}

// Deleting a role removes only that tenant's role: another tenant's same-named
// role and its grants and bindings stay.
func TestDeleteRoleLeavesAnotherTenantsRole(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", "roleA")
	mustPutGrant(t, ctx, f, "acme", "roleA", "account", "read")
	mustPutBinding(t, ctx, f, "acme", "acc1", "roleA")
	mustPutRole(t, ctx, f, "globex", "roleA")
	mustPutGrant(t, ctx, f, "globex", "roleA", "account", "read")
	mustPutBinding(t, ctx, f, "globex", "acc1", "roleA")

	if err := f.DeleteRole(ctx, "acme", "roleA"); err != nil {
		t.Fatalf("DeleteRole(acme, roleA): %v", err)
	}

	if roles, err := f.Roles(ctx, "globex"); err != nil || len(roles) != 1 {
		t.Errorf("Roles(globex) = %v, %v, want the other tenant's role untouched", roles, err)
	}
	if !f.allow(t, "acc1", "globex") {
		t.Errorf("deleting acme's role revoked globex's")
	}
}

// Deleting a role that is not there is no work and no error: the removal is
// idempotent.
func TestDeleteRoleOfAnUnknownRoleIsNoWork(t *testing.T) {
	f := newFixture(t)
	if err := f.DeleteRole(t.Context(), "acme", "ghost"); err != nil {
		t.Errorf("DeleteRole of a role that was never created gave %v, want nil", err)
	}
}

// Deleting a grant removes the row it names and stops deciding, for a role that
// is not builtin.
func TestDeleteGrantRemovesTheRow(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", "roleA")
	mustPutGrant(t, ctx, f, "acme", "roleA", "account", "read")
	mustPutBinding(t, ctx, f, "acme", "acc1", "roleA")
	if !f.allow(t, "acc1", "acme") {
		t.Fatalf("the account was refused a granted permission before the delete")
	}

	if err := f.DeleteGrant(ctx, authz.GrantRecord{
		Tenant: "acme", Role: "roleA", Resource: "account", Action: "read",
	}); err != nil {
		t.Fatalf("DeleteGrant(acme, roleA, account, read): %v", err)
	}

	if grants, err := f.Grants(ctx, "acme", "roleA"); err != nil || len(grants) != 0 {
		t.Errorf("Grants(acme, roleA) = %v, %v, want none", grants, err)
	}
	if f.allow(t, "acc1", "acme") {
		t.Errorf("a removed grant still decided")
	}
}

// Rewriting a role that is not builtin replaces its description: PutRole is
// create-or-rewrite, not create-only. The rewrite leaves the builtin mark where
// it is — the mark has its own write, and a role write never carries it.
func TestPutRoleRewritesANonBuiltinRole(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	if err := f.PutRole(ctx, authz.RoleRecord{Tenant: "acme", Role: "roleA", DescriptionKey: "first"}); err != nil {
		t.Fatalf("PutRole(first): %v", err)
	}
	if err := f.PutRole(ctx, authz.RoleRecord{Tenant: "acme", Role: "roleA", DescriptionKey: "second"}); err != nil {
		t.Fatalf("PutRole(second): %v", err)
	}
	roles, err := f.Roles(ctx, "acme")
	if err != nil || len(roles) != 1 {
		t.Fatalf("Roles(acme) = %v, %v, want the one rewritten role", roles, err)
	}
	if roles[0].DescriptionKey != "second" {
		t.Errorf("the description reads back as %q, want the rewritten value", roles[0].DescriptionKey)
	}
	if roles[0].Builtin {
		t.Errorf("the rewrite set the builtin flag")
	}
}

// A grant and a binding are create-or-keep, not create-only: writing the same
// rule twice names the same row, and the second write is a no-op rather than a
// duplicate-key failure. The management surface addresses grants and bindings
// with PUT, and the host's role provisioner re-issues a tenant's grants on every
// grant — both rely on a re-run adding no row.
func TestPutGrantAndPutBindingAreIdempotent(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", "roleA")

	grant := authz.GrantRecord{Tenant: "acme", Role: "roleA", Resource: "account", Action: "read"}
	if err := f.PutGrant(ctx, grant); err != nil {
		t.Fatalf("PutGrant(first): %v", err)
	}
	if err := f.PutGrant(ctx, grant); err != nil {
		t.Fatalf("PutGrant(second, same key): %v", err)
	}
	if grants, err := f.Grants(ctx, "acme", "roleA"); err != nil || len(grants) != 1 {
		t.Errorf("Grants(acme, roleA) = %v, %v, want the one grant: the second write added no row", grants, err)
	}

	binding := authz.Binding{Tenant: "acme", Account: "acc1", Role: "roleA"}
	if err := f.PutBinding(ctx, binding); err != nil {
		t.Fatalf("PutBinding(first): %v", err)
	}
	if err := f.PutBinding(ctx, binding); err != nil {
		t.Fatalf("PutBinding(second, same key): %v", err)
	}
	if bindings, err := f.BindingsOf(ctx, "acme", "acc1"); err != nil || len(bindings) != 1 {
		t.Errorf("BindingsOf(acme, acc1) = %v, %v, want the one binding: the second write added no row", bindings, err)
	}
}

// A role written with no creation time gets one, so a later read never sees a
// zero timestamp.
func TestPutRoleFillsCreatedAt(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", "roleA")
	roles, err := f.Roles(ctx, "acme")
	if err != nil || len(roles) != 1 {
		t.Fatalf("Roles(acme) = %v, %v, want the one role", roles, err)
	}
	if roles[0].CreatedAt.IsZero() {
		t.Errorf("a role written with no creation time reads back with a zero one")
	}
}

// A binding that names no account is refused: an empty subject is the zero
// identity's key, and a binding for it would decide for whoever arrives with
// no identity.
func TestPutBindingRefusesAnEmptyAccount(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", "roleA")

	if err := f.PutBinding(ctx, authz.Binding{Tenant: "acme", Role: "roleA"}); err == nil {
		t.Errorf("PutBinding with no account was accepted")
	}
	if bindings, err := f.Bindings(ctx, "acme"); err != nil || len(bindings) != 0 {
		t.Errorf("Bindings(acme) = %v, %v, want none: no empty-account row was written", bindings, err)
	}
}

// Every read and write refuses an empty tenant: a row belonging to no tenant
// reaches no tenant's decision while the cross-tenant mark lets it through.
func TestEveryMethodRefusesAnEmptyTenant(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	cases := []struct {
		name string
		call func() error
	}{
		{"Roles", func() error { _, err := f.Roles(ctx, ""); return err }},
		{"Grants", func() error { _, err := f.Grants(ctx, "", "roleA"); return err }},
		{"Bindings", func() error { _, err := f.Bindings(ctx, ""); return err }},
		{"BindingsOf", func() error { _, err := f.BindingsOf(ctx, "", "acc1"); return err }},
		{"PutRole", func() error { return f.PutRole(ctx, authz.RoleRecord{Role: "roleA"}) }},
		{"DeleteRole", func() error { return f.DeleteRole(ctx, "", "roleA") }},
		{"PutGrant", func() error {
			return f.PutGrant(ctx, authz.GrantRecord{Role: "roleA", Resource: "account", Action: "read"})
		}},
		{"DeleteGrant", func() error {
			return f.DeleteGrant(ctx, authz.GrantRecord{Role: "roleA", Resource: "account", Action: "read"})
		}},
		{"PutBinding", func() error { return f.PutBinding(ctx, authz.Binding{Account: "acc1", Role: "roleA"}) }},
		{"DeleteBinding", func() error { return f.DeleteBinding(ctx, "", "acc1", "roleA") }},
		{"DeleteBindings", func() error { return f.DeleteBindings(ctx, "") }},
	}
	for _, c := range cases {
		if err := c.call(); !errors.Is(err, authz.ErrTenantMismatch) {
			t.Errorf("%s with no tenant gave %v, want ErrTenantMismatch", c.name, err)
		}
	}
}

// The lifecycle hooks that hold no policy are no-ops: they return nil for any
// input and change nothing.
func TestLifecycleHooksThatHoldNoPolicyAreNoOps(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	if err := f.AccountEnabled(ctx, authn.AccountID("acc1")); err != nil {
		t.Errorf("AccountEnabled gave %v, want nil", err)
	}
	if err := f.AccountDisabled(ctx, authn.AccountID("acc1")); err != nil {
		t.Errorf("AccountDisabled gave %v, want nil", err)
	}
	if err := f.ServiceSecretRevoked(ctx, authn.AccountID("acc1"), "cred1"); err != nil {
		t.Errorf("ServiceSecretRevoked gave %v, want nil", err)
	}
}
