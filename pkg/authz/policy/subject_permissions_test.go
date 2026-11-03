package policy

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	internalpolicy "github.com/vislake/speed/pkg/authz/internal/policy"
)

// TestHeldAnswersTheProductPermissions holds the subject-permission read to
// what the policy data says through the same product the request path reads: a
// subject holding two roles sees the union once each, sorted, another tenant's
// grant does not appear, and a held pair no permission translates to is left
// out.
func TestHeldAnswersTheProductPermissions(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	for _, role := range []authz.Role{"roleA", "roleB"} {
		if err := f.PutRole(ctx, authz.RoleRecord{Tenant: "acme", Role: role, DescriptionKey: "k"}); err != nil {
			t.Fatalf("PutRole(%s): %v", role, err)
		}
	}
	for _, grant := range []authz.GrantRecord{
		{Tenant: "acme", Role: "roleA", Resource: "account", Action: "read"},
		{Tenant: "acme", Role: "roleA", Resource: "role", Action: "read"},
		{Tenant: "acme", Role: "roleA", Resource: "session", Action: "revoke"},
		{Tenant: "acme", Role: "roleB", Resource: "account", Action: "read"},
		{Tenant: "acme", Role: "roleB", Resource: "widget", Action: "frobnicate"},
	} {
		if err := f.PutGrant(ctx, grant); err != nil {
			t.Fatalf("PutGrant(%+v): %v", grant, err)
		}
	}
	for _, binding := range []authz.Binding{
		{Tenant: "acme", Account: "acc1", Role: "roleA"},
		{Tenant: "acme", Account: "acc1", Role: "roleB"},
	} {
		if err := f.PutBinding(ctx, binding); err != nil {
			t.Fatalf("PutBinding(%+v): %v", binding, err)
		}
	}
	if err := f.PutRole(ctx, authz.RoleRecord{Tenant: "globex", Role: "roleG", DescriptionKey: "k"}); err != nil {
		t.Fatalf("PutRole(globex): %v", err)
	}
	if err := f.PutGrant(ctx, authz.GrantRecord{
		Tenant: "globex", Role: "roleG", Resource: "settings", Action: "write",
	}); err != nil {
		t.Fatalf("PutGrant(globex): %v", err)
	}
	if err := f.PutBinding(ctx, authz.Binding{Tenant: "globex", Account: "acc2", Role: "roleG"}); err != nil {
		t.Fatalf("PutBinding(globex): %v", err)
	}

	held, err := f.Held(ctx, "acme", "acc1")
	if err != nil {
		t.Fatalf("Held(acme, acc1): %v", err)
	}
	// (role, read) loads before (session, revoke) -- the read is ordered by
	// tenant, role, resource, action -- and their permission names sort the
	// other way round (authn.* before authz.*), so the load order is the
	// reverse of the answer's order and only the sort produces this result.
	want := []authz.Permission{
		authz.PermissionAccountRead,
		authz.PermissionSessionRevoke,
		authz.PermissionRoleRead,
	}
	if !slices.Equal(held, want) {
		t.Errorf("Held(acme, acc1) = %v, want %v: the union once each, sorted, and the untranslated pair left out", held, want)
	}

	other, err := f.Held(ctx, "acme", "acc2")
	if err != nil {
		t.Fatalf("Held(acme, acc2): %v", err)
	}
	if len(other) != 0 {
		t.Errorf("Held(acme, acc2) = %v, want nothing: acc2's only grant is in globex", other)
	}

	none, err := f.Held(ctx, "globex", "acc1")
	if err != nil {
		t.Fatalf("Held(globex, acc1): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("Held(globex, acc1) = %v, want nothing", none)
	}
}

// TestTheTenantAdminAnswerCarriesNoPlatformCode holds the governed split at the
// subject-permissions read: a tenant-admin role seeded with the governed set and
// bound in a tenant domain answers exactly that set, with no platform-only code
// in it.
func TestTheTenantAdminAnswerCarriesNoPlatformCode(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	const tenant = "acme"
	role := authz.TenantAdminRole
	if err := f.PutRole(ctx, authz.RoleRecord{Tenant: tenant, Role: role, DescriptionKey: "k"}); err != nil {
		t.Fatalf("PutRole: %v", err)
	}
	grants := make([]authz.Grant, 0, len(f.catalog.TenantGoverned()))
	for _, permission := range f.catalog.TenantGoverned() {
		object, action, ok := f.catalog.ObjectAction(permission)
		if !ok {
			t.Fatalf("the governed permission %q has no translation row", permission)
		}
		grants = append(grants, authz.Grant{Resource: object, Action: action})
	}
	if _, err := f.SeedGrants(ctx, tenant, role, grants); err != nil {
		t.Fatalf("SeedGrants: %v", err)
	}
	if err := f.MarkRoleBuiltin(ctx, tenant, role); err != nil {
		t.Fatalf("MarkRoleBuiltin: %v", err)
	}
	if err := f.PutBinding(ctx, authz.Binding{Tenant: tenant, Account: "alice", Role: role}); err != nil {
		t.Fatalf("PutBinding: %v", err)
	}

	held, err := f.Held(ctx, authz.Domain(tenant), "alice")
	if err != nil {
		t.Fatalf("Held: %v", err)
	}
	want := f.catalog.TenantGoverned()
	if !slices.Equal(held, want) {
		t.Errorf("Held = %v, want the governed set %v", held, want)
	}
	for _, permission := range held {
		if !f.catalog.Governed(permission) {
			t.Errorf("the tenant-admin answer carries the platform-only permission %q", permission)
		}
	}
}

// A read whose snapshot was never loaded is unavailable rather than an empty
// permission set.
func TestHeldReportsAnUnloadedSnapshotAsUnavailable(t *testing.T) {
	p := newProductOver(t)
	if _, err := p.Held(t.Context(), "acme", "acc1"); !errors.Is(err, authz.ErrPolicyUnavailable) {
		t.Errorf("Held over an unloaded product gave %v, want ErrPolicyUnavailable", err)
	}
}

// An engine failure is unavailable the same way: a snapshot that is loaded but
// whose engine cannot answer is a dependency that cannot answer, not an empty
// permission set, so the sentinel classifies that path too and the engine's
// cause stays in the error.
func TestHeldReportsAnEngineFailureAsUnavailable(t *testing.T) {
	p := newProductOver(t)
	// The read reaches the engine's role manager for "g", so a model without
	// the role definition loads (the tables are empty) but fails the read.
	broken := strings.Replace(authz.ModelText, "g = _, _, _\n", "", 1)
	snapshot, err := internalpolicy.Load(t.Context(), p.handle, broken)
	if err != nil {
		t.Fatalf("loading the snapshot: %v", err)
	}
	p.holder.Swap(snapshot)

	_, err = p.Held(t.Context(), "acme", "acc1")
	if !errors.Is(err, authz.ErrPolicyUnavailable) {
		t.Errorf("Held over a failing engine gave %v, want ErrPolicyUnavailable", err)
	}
	if !strings.Contains(err.Error(), "role manager") {
		t.Errorf("Held over a failing engine lost the engine's cause: %v", err)
	}
}
