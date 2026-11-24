package policy

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
)

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

// TestGrantsScopesToOneTenantRole checks that grants scopes to one tenant role.
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
		byObject[g.Resource] = g
	}
	if g, ok := byObject["account"]; !ok || g.Action != "read" {
		t.Errorf("the account grant reads back as %+v, want resource account action read", byObject["account"])
	}
	if g, ok := byObject["session"]; !ok || g.Action != "revoke" {
		t.Errorf("the session grant reads back as %+v, want resource session action revoke", byObject["session"])
	}

	if other, err := f.Grants(ctx, "globex", "roleA"); err != nil || len(other) != 1 || other[0].Resource != "settings" {
		t.Errorf("Grants(globex, roleA) = %v, %v, want only that tenant's grant", other, err)
	}
}

// TestBindingsListsOneTenant checks that Bindings lists one tenant.
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

// TestBindingsOfScopesToOneTenant checks that bindings of scopes to one tenant.
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

// TestPutRoleWritesAndRewritesTheDescription checks that put role writes and rewrites the description.
func TestPutRoleWritesAndRewritesTheDescription(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	if err := f.PutRole(ctx, authz.RoleRecord{
		Tenant: "acme", Role: "roleA", DescriptionKey: "authz.role.custom", Description: "First line",
	}); err != nil {
		t.Fatalf("PutRole(first): %v", err)
	}
	roles, err := f.Roles(ctx, "acme")
	if err != nil || len(roles) != 1 {
		t.Fatalf("Roles(acme) = %v, %v, want the one role", roles, err)
	}
	if roles[0].Description != "First line" {
		t.Errorf("the description reads back as %q, want %q", roles[0].Description, "First line")
	}
	if roles[0].DescriptionKey != "authz.role.custom" {
		t.Errorf("the description key reads back as %q, want it kept apart from the description", roles[0].DescriptionKey)
	}

	if err = f.PutRole(ctx, authz.RoleRecord{
		Tenant: "acme", Role: "roleA", DescriptionKey: "authz.role.custom", Description: "Second line",
	}); err != nil {
		t.Fatalf("PutRole(second): %v", err)
	}
	roles, err = f.Roles(ctx, "acme")
	if err != nil || len(roles) != 1 {
		t.Fatalf("Roles(acme) after rewrite = %v, %v, want the one role", roles, err)
	}
	if roles[0].Description != "Second line" {
		t.Errorf("the rewritten description reads back as %q, want %q", roles[0].Description, "Second line")
	}
}

// TestGrantCountsCountsEachRolesGrantsInOneTenant checks that grant counts counts each roles grants in one tenant.
func TestGrantCountsCountsEachRolesGrantsInOneTenant(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", "roleA")
	mustPutRole(t, ctx, f, "acme", "roleB")
	mustPutRole(t, ctx, f, "acme", "roleC")
	mustPutRole(t, ctx, f, "globex", "roleA")
	mustPutGrant(t, ctx, f, "acme", "roleA", "account", "read")
	mustPutGrant(t, ctx, f, "acme", "roleA", "session", "revoke")
	mustPutGrant(t, ctx, f, "acme", "roleB", "audit", "read")
	mustPutGrant(t, ctx, f, "globex", "roleA", "account", "read")

	counts, err := f.GrantCounts(ctx, "acme")
	if err != nil {
		t.Fatalf("GrantCounts(acme): %v", err)
	}
	if len(counts) != 2 {
		t.Fatalf("GrantCounts(acme) = %v, want the two roles holding a grant", counts)
	}
	if got := counts["roleA"]; got != 2 {
		t.Errorf("GrantCounts(acme)[roleA] = %d, want 2", got)
	}
	if got := counts["roleB"]; got != 1 {
		t.Errorf("GrantCounts(acme)[roleB] = %d, want 1", got)
	}
	if _, counted := counts["roleC"]; counted {
		t.Errorf("GrantCounts(acme) counted roleC, which holds no grant")
	}
	if globe, err := f.GrantCounts(ctx, "globex"); err != nil || len(globe) != 1 || globe["roleA"] != 1 {
		t.Errorf("GrantCounts(globex) = %v, %v, want only that tenant's grant", globe, err)
	}
}

// TestBindingsOfRoleFiltersAndPagesByAccount checks that bindings of role filters and pages by account.
func TestBindingsOfRoleFiltersAndPagesByAccount(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", "roleA")
	mustPutRole(t, ctx, f, "acme", "roleB")
	mustPutRole(t, ctx, f, "globex", "roleA")
	for _, account := range []string{"acc1", "acc2", "acc3"} {
		mustPutBinding(t, ctx, f, "acme", account, "roleA")
	}
	mustPutBinding(t, ctx, f, "acme", "acc1", "roleB")
	mustPutBinding(t, ctx, f, "globex", "acc1", "roleA")

	accounts := func(bindings []authz.Binding) []string {
		out := make([]string, len(bindings))
		for i, b := range bindings {
			out[i] = b.Account
		}
		return out
	}
	pageOf := func(after string, limit int) []string {
		t.Helper()
		bindings, err := f.BindingsOfRole(ctx, "acme", "roleA", after, limit)
		if err != nil {
			t.Fatalf("BindingsOfRole(acme, roleA, %q, %d): %v", after, limit, err)
		}
		for _, b := range bindings {
			if b.Tenant != "acme" || b.Role != "roleA" {
				t.Errorf("a binding reads back as %+v, want tenant acme role roleA", b)
			}
		}
		return accounts(bindings)
	}

	if got, want := pageOf("", 0), []string{"acc1", "acc2", "acc3"}; !slices.Equal(got, want) {
		t.Errorf("the unpaged read = %v, want %v", got, want)
	}
	if got, want := pageOf("", 2), []string{"acc1", "acc2"}; !slices.Equal(got, want) {
		t.Errorf("the first page = %v, want %v", got, want)
	}
	if got, want := pageOf("acc2", 2), []string{"acc3"}; !slices.Equal(got, want) {
		t.Errorf("the page after acc2 = %v, want %v", got, want)
	}
	if got := pageOf("acc3", 2); len(got) != 0 {
		t.Errorf("the page after the last account = %v, want none", got)
	}

	otherRole, err := f.BindingsOfRole(ctx, "acme", "roleB", "", 0)
	if err != nil || len(otherRole) != 1 || otherRole[0].Account != "acc1" {
		t.Errorf("BindingsOfRole(acme, roleB) = %v, %v, want only roleB's binding", otherRole, err)
	}
	otherTenant, err := f.BindingsOfRole(ctx, "globex", "roleA", "", 0)
	if err != nil || len(otherTenant) != 1 || otherTenant[0].Tenant != "globex" {
		t.Errorf("BindingsOfRole(globex, roleA) = %v, %v, want only that tenant's binding", otherTenant, err)
	}
}

// TestClearAccountBindingsClearsOneAccountWithinOneTenant checks that clear account bindings clears one account within one tenant.
func TestClearAccountBindingsClearsOneAccountWithinOneTenant(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", string(authz.TenantAdminRole))
	mustPutRole(t, ctx, f, "acme", "editor")
	mustPutRole(t, ctx, f, "globex", string(authz.TenantAdminRole))
	mustPutBinding(t, ctx, f, "acme", "acc1", string(authz.TenantAdminRole))
	mustPutBinding(t, ctx, f, "acme", "acc2", "editor")
	mustPutBinding(t, ctx, f, "globex", "acc1", string(authz.TenantAdminRole))

	if err := f.DeleteBinding(ctx, "acme", "acc1", authz.TenantAdminRole); !errors.Is(err, authz.ErrLastTenantAdmin) {
		t.Fatalf("DeleteBinding of the last tenant-admin = %v, want ErrLastTenantAdmin", err)
	}
	if err := f.ClearAccountBindings(ctx, "acme", "acc1"); err != nil {
		t.Fatalf("ClearAccountBindings(acme, acc1): %v", err)
	}
	if bindings, err := f.BindingsOf(ctx, "acme", "acc1"); err != nil || len(bindings) != 0 {
		t.Errorf("BindingsOf(acme, acc1) = %v, %v, want none", bindings, err)
	}
	if kept, err := f.BindingsOf(ctx, "acme", "acc2"); err != nil || len(kept) != 1 {
		t.Errorf("BindingsOf(acme, acc2) = %v, %v, want the other account's binding untouched", kept, err)
	}
	if other, err := f.BindingsOf(ctx, "globex", "acc1"); err != nil || len(other) != 1 {
		t.Errorf("BindingsOf(globex, acc1) = %v, %v, want the other tenant's binding untouched", other, err)
	}
}

// TestDeleteRoleRevokesItsGrantsAndBindings checks that delete role revokes its grants and bindings.
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

// TestDeleteRoleLeavesAnotherTenantsRole checks that delete role leaves another tenants role.
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

// TestDeleteRoleOfAnUnknownRoleIsNoWork checks that delete role of an unknown role is no work.
func TestDeleteRoleOfAnUnknownRoleIsNoWork(t *testing.T) {
	f := newFixture(t)
	if err := f.DeleteRole(t.Context(), "acme", "ghost"); err != nil {
		t.Errorf("DeleteRole of a role that was never created gave %v, want nil", err)
	}
}

// TestDeleteGrantRemovesTheRow checks that delete grant removes the row.
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

// TestPutRoleRewritesANonBuiltinRole checks that put role rewrites a non builtin role.
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

// TestPutGrantAndPutBindingAreIdempotent checks that put grant and put binding are idempotent.
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

// TestPutRoleFillsCreatedAt checks that put role fills created at.
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

// TestPutBindingRefusesAnEmptyAccount checks that PutBinding refuses an empty account.
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

// TestEveryMethodRefusesAnEmptyTenant checks that every method refuses an empty tenant.
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
		{"BindingsOfRole", func() error { _, err := f.BindingsOfRole(ctx, "", "roleA", "", 10); return err }},
		{"GrantCounts", func() error { _, err := f.GrantCounts(ctx, ""); return err }},
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
		{"ClearAccountBindings", func() error { return f.ClearAccountBindings(ctx, "", "acc1") }},
	}
	for _, c := range cases {
		if err := c.call(); !errors.Is(err, authz.ErrTenantMismatch) {
			t.Errorf("%s with no tenant gave %v, want ErrTenantMismatch", c.name, err)
		}
	}
}

// TestLifecycleHooksThatHoldNoPolicyAreNoOps checks that lifecycle hooks that hold no policy are no ops.
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
