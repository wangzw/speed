package policy

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	tenantcatalog "github.com/vislake/speed/pkg/tenant/catalog"
)

// tenantFilter returns the real statement filter the tenant catalog installs on
// the handles an assembly hands a module, taken from the module's own
// declarations.
func tenantFilter(t *testing.T) gorm.Plugin {
	t.Helper()
	for _, resource := range tenantcatalog.Module().Resources {
		declared, ok := resource.(db.Plugin)
		if ok && declared.Plugin != nil {
			return declared.Plugin
		}
	}
	t.Fatal("the tenant catalog declares no statement filter")
	return nil
}

// fixture is a product over a real handle with the policy tables created and
// the tenant filter installed.
type fixture struct{ *product }

// newFixture delivers a SQLite handle through the database module, installs the
// tenant filter, creates the policy tables and starts the product.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	handle := dbtest.SQLite(t, filepath.Join(t.TempDir(), "authz.db"), nil).DB()
	if err := handle.Use(tenantFilter(t)); err != nil {
		t.Fatalf("installing the tenant filter: %v", err)
	}
	applyMigration(t, handle, "sqlite/0001_authz_policy.sql")
	applyMigration(t, handle, "sqlite/0003_role_description.sql")
	reg := core.New()
	reg.Register(Module())
	p := &product{
		handle:      handle,
		dialect:     db.SQLite,
		text:        authz.ModelText,
		loadTimeout: 5 * time.Second,
		catalog:     mustBuildCatalog(t, reg),
		logger:      log.Default(),
		tenantLock:  authz.NewInProcessTenantLock(),
	}
	if err := p.begin(t.Context()); err != nil {
		t.Fatalf("starting the product: %v", err)
	}
	return &fixture{product: p}
}

func (f *fixture) allow(t *testing.T, account, domain string) bool {
	t.Helper()
	allowed, err := f.Allowed(t.Context(), authz.Query{
		Subject: account, Domain: authz.Domain(domain), Object: "account", Action: "read",
	})
	if err != nil {
		t.Fatalf("Allowed: %v", err)
	}
	return allowed
}

// A role, its grant and a binding written through the manager decide a query on
// the granting tenant and no other.
func TestManagerWritesDecideByTenant(t *testing.T) {
	f := newFixture(t)

	if err := f.PutRole(t.Context(), authz.RoleRecord{Tenant: "acme", Role: "roleA", DescriptionKey: "k"}); err != nil {
		t.Fatalf("PutRole: %v", err)
	}
	if err := f.PutGrant(t.Context(), authz.GrantRecord{Tenant: "acme", Role: "roleA", Resource: "account", Action: "read"}); err != nil {
		t.Fatalf("PutGrant: %v", err)
	}
	if err := f.PutBinding(t.Context(), authz.Binding{Tenant: "acme", Account: "acc1", Role: "roleA"}); err != nil {
		t.Fatalf("PutBinding: %v", err)
	}

	if !f.allow(t, "acc1", "acme") {
		t.Errorf("acme's account was refused a granted permission")
	}
	if f.allow(t, "acc1", "globex") {
		t.Errorf("a binding in acme decided a query in globex")
	}
	if f.allow(t, "acc2", "acme") {
		t.Errorf("an account with no binding was allowed")
	}

	roles, err := f.Roles(t.Context(), "acme")
	if err != nil || len(roles) != 1 || roles[0].Role != "roleA" {
		t.Fatalf("Roles(acme) = %v, %v, want the one role", roles, err)
	}
	if other, listErr := f.Roles(t.Context(), "globex"); listErr != nil || len(other) != 0 {
		t.Fatalf("Roles(globex) = %v, %v, want none", other, listErr)
	}
	bindings, err := f.BindingsOf(t.Context(), "acme", "acc1")
	if err != nil || len(bindings) != 1 || bindings[0].Role != "roleA" {
		t.Fatalf("BindingsOf(acme, acc1) = %v, %v, want the one binding", bindings, err)
	}
}

// A grant or a binding naming a role that was not created is refused.
func TestManagerRefusesAnUnknownRole(t *testing.T) {
	f := newFixture(t)
	err := f.PutGrant(t.Context(), authz.GrantRecord{Tenant: "acme", Role: "ghost", Resource: "account", Action: "read"})
	if !errors.Is(err, authz.ErrUnknownRole) {
		t.Errorf("PutGrant for an unknown role gave %v, want ErrUnknownRole", err)
	}
	err = f.PutBinding(t.Context(), authz.Binding{Tenant: "acme", Account: "acc1", Role: "ghost"})
	if !errors.Is(err, authz.ErrUnknownRole) {
		t.Errorf("PutBinding for an unknown role gave %v, want ErrUnknownRole", err)
	}
}

// A builtin role's grants are fixed: it cannot be deleted, its grants cannot be
// changed, and it cannot be rewritten.
func TestManagerRefusesToChangeABuiltinRole(t *testing.T) {
	f := newFixture(t)
	if err := f.PutRole(t.Context(), authz.RoleRecord{Tenant: "system", Role: "operator", DescriptionKey: "k"}); err != nil {
		t.Fatalf("PutRole: %v", err)
	}
	if err := f.MarkRoleBuiltin(t.Context(), "system", "operator"); err != nil {
		t.Fatalf("MarkRoleBuiltin: %v", err)
	}
	if err := f.DeleteRole(t.Context(), "system", "operator"); !errors.Is(err, authz.ErrBuiltinRole) {
		t.Errorf("DeleteRole on a builtin role gave %v, want ErrBuiltinRole", err)
	}
	// A builtin role can be bound to an account: that is how platform
	// operations are handed out.
	if err := f.PutBinding(t.Context(), authz.Binding{Tenant: "system", Account: "acc9", Role: "operator"}); err != nil {
		t.Errorf("PutBinding a builtin role: %v", err)
	}
	grant := authz.GrantRecord{Tenant: "system", Role: "operator", Resource: "audit", Action: "read"}
	if err := f.PutGrant(t.Context(), grant); !errors.Is(err, authz.ErrBuiltinRole) {
		t.Errorf("PutGrant on a builtin role gave %v, want ErrBuiltinRole", err)
	}
	if err := f.DeleteGrant(t.Context(), grant); !errors.Is(err, authz.ErrBuiltinRole) {
		t.Errorf("DeleteGrant on a builtin role gave %v, want ErrBuiltinRole", err)
	}
	// Rewriting an existing builtin role is refused.
	err := f.PutRole(t.Context(), authz.RoleRecord{Tenant: "system", Role: "operator", DescriptionKey: "changed"})
	if !errors.Is(err, authz.ErrBuiltinRole) {
		t.Errorf("PutRole over a builtin role gave %v, want ErrBuiltinRole", err)
	}
	// The refusals leave the role standing: a refused delete that removed the
	// row anyway would still lock the operator out.
	roles, err := f.Roles(t.Context(), "system")
	if err != nil {
		t.Fatalf("Roles: %v", err)
	}
	if len(roles) != 1 || roles[0].Role != "operator" || !roles[0].Builtin {
		t.Errorf("Roles(system) = %+v, want the one builtin role", roles)
	}
}

// The refusal belongs to the builtin flag and not to roles in general: a role
// that is not builtin keeps its write paths, so the guard is not wider than the
// defect it answers.
func TestManagerStillChangesANonBuiltinRole(t *testing.T) {
	f := newFixture(t)
	const tenant = "acme"
	if err := f.PutRole(t.Context(), authz.RoleRecord{Tenant: tenant, Role: "editor", DescriptionKey: "k"}); err != nil {
		t.Fatalf("PutRole: %v", err)
	}
	grant := authz.GrantRecord{Tenant: tenant, Role: "editor", Resource: "deal", Action: "read"}
	if err := f.PutGrant(t.Context(), grant); err != nil {
		t.Errorf("PutGrant on a non-builtin role: %v", err)
	}
	if err := f.DeleteGrant(t.Context(), grant); err != nil {
		t.Errorf("DeleteGrant on a non-builtin role: %v", err)
	}
	if err := f.PutRole(t.Context(), authz.RoleRecord{Tenant: tenant, Role: "editor", DescriptionKey: "changed"}); err != nil {
		t.Errorf("PutRole over a non-builtin role: %v", err)
	}
	if err := f.DeleteRole(t.Context(), tenant, "editor"); err != nil {
		t.Errorf("DeleteRole on a non-builtin role: %v", err)
	}
}

// A role write never carries the builtin mark, whether it creates or rewrites
// the role. A role created with the mark could never receive the grants that
// give it meaning, and a rewrite that could add it would turn any role into one
// the management surface may neither rewrite nor delete, with no path back.
func TestManagerRefusesARoleWriteCarryingTheBuiltinMark(t *testing.T) {
	f := newFixture(t)
	const tenant = "acme"

	// A rewrite of an existing plain role cannot promote it.
	if err := f.PutRole(t.Context(), authz.RoleRecord{Tenant: tenant, Role: "editor", DescriptionKey: "k"}); err != nil {
		t.Fatalf("PutRole: %v", err)
	}
	err := f.PutRole(t.Context(), authz.RoleRecord{Tenant: tenant, Role: "editor", Builtin: true, DescriptionKey: "k"})
	if !errors.Is(err, authz.ErrBuiltinRole) {
		t.Errorf("PutRole promoting a plain role gave %v, want ErrBuiltinRole", err)
	}
	roles, err := f.Roles(t.Context(), tenant)
	if err != nil {
		t.Fatalf("Roles: %v", err)
	}
	if len(roles) != 1 || roles[0].Builtin {
		t.Errorf("the refused rewrite changed the role's mark: %+v", roles)
	}

	// A creation cannot carry it either: builtin with no grants is a role
	// whose permissions can never be written.
	if err := f.PutRole(t.Context(), authz.RoleRecord{Tenant: tenant, Role: "frozen", Builtin: true, DescriptionKey: "k"}); !errors.Is(err, authz.ErrBuiltinRole) {
		t.Errorf("PutRole creating a builtin role gave %v, want ErrBuiltinRole", err)
	}
	if roles, listErr := f.Roles(t.Context(), tenant); listErr != nil || len(roles) != 1 {
		t.Errorf("the refused creation wrote a role: %+v, %v", roles, listErr)
	}
}

// The builtin mark has its own write. It sets the mark on an existing role,
// refuses one that is not there, and an already-marked role is no work.
func TestManagerMarksARoleBuiltin(t *testing.T) {
	f := newFixture(t)
	const tenant = "acme"

	if err := f.MarkRoleBuiltin(t.Context(), tenant, "ghost"); !errors.Is(err, authz.ErrUnknownRole) {
		t.Errorf("MarkRoleBuiltin on an unknown role gave %v, want ErrUnknownRole", err)
	}
	if err := f.PutRole(t.Context(), authz.RoleRecord{Tenant: tenant, Role: "operator", DescriptionKey: "k"}); err != nil {
		t.Fatalf("PutRole: %v", err)
	}
	if err := f.MarkRoleBuiltin(t.Context(), tenant, "operator"); err != nil {
		t.Fatalf("MarkRoleBuiltin: %v", err)
	}
	if err := f.MarkRoleBuiltin(t.Context(), tenant, "operator"); err != nil {
		t.Errorf("marking an already-builtin role again gave %v, want no error", err)
	}
	roles, err := f.Roles(t.Context(), tenant)
	if err != nil {
		t.Fatalf("Roles: %v", err)
	}
	if len(roles) != 1 {
		t.Fatalf("Roles = %+v, want the one role", roles)
	}
	if !roles[0].Builtin {
		t.Errorf("Roles = %+v, want the role marked builtin", roles)
	}
	// The mark leaves the rest of the record as it stood.
	if roles[0].DescriptionKey != "k" {
		t.Errorf("the mark changed the description key to %q, want it kept", roles[0].DescriptionKey)
	}
}

// An empty tenant argument is refused: a row belonging to no tenant reaches no
// tenant's decision.
func TestManagerRefusesAnEmptyTenant(t *testing.T) {
	f := newFixture(t)
	if err := f.PutRole(t.Context(), authz.RoleRecord{Role: "roleA", DescriptionKey: "k"}); !errors.Is(err, authz.ErrTenantMismatch) {
		t.Errorf("PutRole with no tenant gave %v, want ErrTenantMismatch", err)
	}
	if _, err := f.Roles(t.Context(), ""); !errors.Is(err, authz.ErrTenantMismatch) {
		t.Errorf("Roles with no tenant gave %v, want ErrTenantMismatch", err)
	}
}

// Deleting a binding removes it, and the account-deletion hook clears every
// binding the account holds.
func TestDeleteBindingAndAccountCleanup(t *testing.T) {
	f := newFixture(t)
	if err := f.PutRole(t.Context(), authz.RoleRecord{Tenant: "acme", Role: "roleA", DescriptionKey: "k"}); err != nil {
		t.Fatalf("PutRole: %v", err)
	}
	if err := f.PutBinding(t.Context(), authz.Binding{Tenant: "acme", Account: "acc1", Role: "roleA"}); err != nil {
		t.Fatalf("PutBinding: %v", err)
	}
	if err := f.DeleteBinding(t.Context(), "acme", "acc1", "roleA"); err != nil {
		t.Fatalf("DeleteBinding: %v", err)
	}
	if f.allow(t, "acc1", "acme") {
		t.Errorf("a removed binding still decided")
	}

	// Rebind in two tenants and delete the account: both rows go.
	if err := f.PutRole(t.Context(), authz.RoleRecord{Tenant: "globex", Role: "roleG", DescriptionKey: "k"}); err != nil {
		t.Fatalf("PutRole(globex): %v", err)
	}
	if err := f.PutBinding(t.Context(), authz.Binding{Tenant: "acme", Account: "acc1", Role: "roleA"}); err != nil {
		t.Fatalf("PutBinding(acme): %v", err)
	}
	if err := f.PutBinding(t.Context(), authz.Binding{Tenant: "globex", Account: "acc1", Role: "roleG"}); err != nil {
		t.Fatalf("PutBinding(globex): %v", err)
	}
	if err := f.AccountDeleted(t.Context(), authn.AccountID("acc1")); err != nil {
		t.Fatalf("AccountDeleted: %v", err)
	}
	for _, tenantID := range []string{"acme", "globex"} {
		bindings, err := f.BindingsOf(t.Context(), tenantID, "acc1")
		if err != nil {
			t.Fatalf("BindingsOf(%s): %v", tenantID, err)
		}
		if len(bindings) != 0 {
			t.Errorf("tenant %s still holds %d binding(s) of the deleted account", tenantID, len(bindings))
		}
	}

	// The cleanup is idempotent: a second delivery of the deletion is no work
	// and no error.
	if err := f.AccountDeleted(t.Context(), authn.AccountID("acc1")); err != nil {
		t.Fatalf("AccountDeleted a second time: %v", err)
	}
	for _, tenantID := range []string{"acme", "globex"} {
		if bindings, err := f.BindingsOf(t.Context(), tenantID, "acc1"); err != nil || len(bindings) != 0 {
			t.Errorf("after a second cleanup tenant %s holds %v (%v), want none", tenantID, bindings, err)
		}
	}
}

// buildCatalog unions every registered module's PermissionSpec: a permission two
// modules declare is kept once, one only a single module declares still arrives,
// Declared answers a copy while Has reads the set, and DescriptionKey reports the
// key of a declared permission.
//
// This is the piece the vocabulary guard in pkg/authz cannot reach: that guard
// holds the declared permissions to the root package's constants but reads the
// specs through the registry rather than building the catalog, so the union
// itself is held here. Together the two cover Catalog.Declared(), which is what
// the design's guard requires.
func TestCatalogUnionsTheDeclaredSpecs(t *testing.T) {
	spec := func(perms ...authz.Permission) authz.PermissionSpec {
		keys := make(map[authz.Permission]string, len(perms))
		for _, p := range perms {
			keys[p] = "k." + string(p)
		}
		return authz.PermissionSpec{Permissions: perms, DescriptionKeys: keys}
	}
	reg := core.New()
	reg.Register(core.Module{Name: "spec.one", Resources: []any{
		spec(authz.PermissionRoleRead, authz.PermissionAuditRead),
	}})
	reg.Register(core.Module{Name: "spec.two", Resources: []any{
		spec(authz.PermissionAuditRead, authz.PermissionSettingsWrite),
	}})

	c := mustBuildCatalog(t, reg)

	declared := c.Declared()
	if len(declared) != 3 {
		t.Fatalf("the catalog declares %v, want the three distinct permissions", declared)
	}
	// Declared answers a copy: trimming what it hands back does not disturb the
	// next reader.
	declared[0] = authz.Permission("mutated")
	if c.Declared()[0] == authz.Permission("mutated") {
		t.Errorf("Declared handed back the catalog's own slice")
	}
	for _, p := range []authz.Permission{authz.PermissionRoleRead, authz.PermissionAuditRead, authz.PermissionSettingsWrite} {
		if !c.Has(p) {
			t.Errorf("the catalog does not declare %q", p)
		}
	}
	if c.Has("authz.undeclared") {
		t.Errorf("the catalog reports an undeclared permission as declared")
	}
	if key, ok := c.DescriptionKey(authz.PermissionAuditRead); !ok || key != "k."+string(authz.PermissionAuditRead) {
		t.Errorf("DescriptionKey of a declared permission = (%q, %t), want its spec's key", key, ok)
	}
	if _, ok := c.DescriptionKey("authz.undeclared"); ok {
		t.Errorf("the catalog holds a description key for an undeclared permission")
	}
}

// rosterStub is a tenant catalog carrying a fixed set of identifiers.
type rosterStub struct{ ids map[tenant.ID]bool }

func (r rosterStub) Get(_ context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	return tenant.Tenant{ID: id}, r.ids[id], nil
}

func (rosterStub) ByAlias(context.Context, string) (tenant.Tenant, bool, error) {
	return tenant.Tenant{}, false, nil
}
func (rosterStub) Put(context.Context, tenant.Tenant) error    { return nil }
func (rosterStub) Create(context.Context, tenant.Tenant) error { return nil }
func (rosterStub) SetStatus(context.Context, tenant.ID, tenant.Status) error {
	return nil
}

func (rosterStub) SetStatusByAccount(context.Context, tenant.ID, tenant.Status) error {
	return nil
}
func (rosterStub) Delete(context.Context, tenant.ID) error           { return nil }
func (rosterStub) SetAlias(context.Context, tenant.ID, string) error { return nil }
func (rosterStub) SetProfile(context.Context, tenant.ID, tenant.Profile) error {
	return nil
}
func (rosterStub) List(context.Context) ([]tenant.Tenant, error)             { return nil, nil }
func (rosterStub) Enter(context.Context, tenant.ID) (context.Context, error) { return nil, nil }
func (rosterStub) EnterRegistered(context.Context, tenant.ID) (context.Context, error) {
	return nil, nil
}

// A roster with no tenant carrying the platform domain stops the startup; one
// that carries it passes. The platform domain's policy rows hang on that
// tenant's identifier, so an assembly with a roster and no such tenant has
// nowhere for a platform decision to land.
func TestCheckPlatformDomain(t *testing.T) {
	ctx := t.Context()
	err := checkPlatformDomain(ctx, rosterStub{ids: map[tenant.ID]bool{"acme": true}})
	if !errors.Is(err, authz.ErrPlatformDomainMissing) {
		t.Errorf("a roster without the platform domain gave %v, want ErrPlatformDomainMissing", err)
	}
	if err := checkPlatformDomain(ctx, rosterStub{ids: map[tenant.ID]bool{
		"acme": true, tenant.ID(authz.PlatformDomain): true,
	}}); err != nil {
		t.Errorf("a roster carrying the platform domain gave %v, want nil", err)
	}
}
