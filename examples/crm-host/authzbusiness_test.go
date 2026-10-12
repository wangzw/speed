package main

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authz"
	authztest "github.com/vislake/speed/pkg/authz/authztest"
	authzguard "github.com/vislake/speed/pkg/authz/guard"
	authzpolicy "github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// These cases hold the host's authorization vocabulary and role matrix. Two of
// them drive the real policy and enforcement modules through a real registry, so
// what they check is what an assembly's startup runs: the catalog the policy
// module builds from the specs the registry collects, and the translation the
// enforcement module resolves from the rows a module declares. The matrix cases
// hold the provisioners to the grant sets they write, through a manager that
// keeps the rows the way the policy table's primary key would.

// businessGuardConfig names the endpoint the driven assemblies guard, so the
// enforcement module's required endpoint list is satisfied by the stand-in
// router below.
const businessGuardConfig = `{"authz":{"guard":{"endpoints":["public"]}}}`

// authzSupply registers a stand-in module and supplies its instance, so a driven
// module resolves the capability from it exactly as a host's assembly hands one
// over.
func authzSupply(reg *core.Registry, name string, token core.Token, instance any) {
	reg.Register(core.Module{Name: name, Provides: []core.Provision{{Token: token}}})
	reg.Supply(instance, name)
}

// authzRouterStub stands for an entry point: one endpoint whose registrations are
// dropped, so the enforcement module wires without a listener.
type authzRouterStub struct{}

func (authzRouterStub) Endpoint(string) (speedhttp.Endpoint, error) { return authzEndpointStub{}, nil }
func (authzRouterStub) Endpoints() []string                         { return []string{"public"} }

// authzEndpointStub accepts registrations and does nothing with them.
type authzEndpointStub struct{}

func (authzEndpointStub) Route(string, string, ...gin.HandlerFunc) {}
func (authzEndpointStub) Use(speedhttp.Middleware)                 {}
func (authzEndpointStub) Accepting() bool                          { return true }

// authzDBStub delivers the database capability without opening anything. The
// policy module's construction stores the handle and reads nothing of it until
// its Start, so a drive that stops at Init needs no engine.
type authzDBStub struct{}

var _ db.Database = authzDBStub{}

func (authzDBStub) DB() *gorm.DB                                   { return nil }
func (authzDBStub) Dialect() db.Dialect                            { return "" }
func (authzDBStub) BlindIndex(string) string                       { return "" }
func (authzDBStub) Open(context.Context, string) (*gorm.DB, error) { return nil, nil }

// authzRouteModule is a stand-in for a feature module that declares one route
// and the permission it needs, from its own Init — the shape every host module
// takes for its routes.
func authzRouteModule(name, method, path string, perm authz.Permission) core.Module {
	return core.Module{
		Name:     name,
		Requires: []core.Requirement{{Token: (*speedhttp.Router)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			gate, err := core.Resolve[authz.Gate](reg)
			if err != nil {
				return err
			}
			gate.Require(method, path, perm)
			return nil
		},
	}
}

// TestTheHostDeclaresItsBusinessPermissionsAndTranslation drives the real policy
// and enforcement modules with the host's business module and holds two things:
// every permission the host declares reaches the policy module's catalog, and
// every one of them resolves through the enforcement module's translation, so a
// route that names one passes the startup's declaration check. A permission with
// no catalog entry and one with no translation row each fail it.
func TestTheHostDeclaresItsBusinessPermissionsAndTranslation(t *testing.T) {
	t.Parallel()
	policyModule := authzpolicy.Module()
	guardModule := authzguard.Module()
	reader := authztest.ConfigReaderAll(t, businessGuardConfig, policyModule, guardModule)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(policyModule)
	reg.Register(guardModule)
	reg.Register(authzBusinessModule())
	authzSupply(reg, "stand-in.db", (*db.Database)(nil), authzDBStub{})
	authzSupply(reg, "stand-in.router", (*speedhttp.Router)(nil), authzRouterStub{})

	names := []string{
		authzpolicy.ModuleName, authzguard.ModuleName, authzBusinessModuleName,
		"stand-in.db", "stand-in.router",
	}
	var checked bool
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: names,
		Action: func(_ context.Context, reg *core.Registry) error {
			catalog, err := core.Resolve[authz.Catalog](reg)
			if err != nil {
				return err
			}
			declared := map[authz.Permission]bool{}
			for _, p := range catalog.Declared() {
				declared[p] = true
			}
			for _, p := range businessPermissions {
				if !declared[p] {
					t.Errorf("the catalog does not declare the host's permission %q", p)
				}
				if _, _, ok := authzguard.ObjectAction(p); !ok {
					t.Errorf("the resolved translation has no row for the host's permission %q", p)
				}
				if businessPermissionKeys[p] == "" {
					t.Errorf("the host declares no description key for the permission %q", p)
				}
			}
			checked = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the policy and enforcement modules with the host's business module failed: %v", err)
	}
	if !checked {
		t.Fatal("the drive's action never ran, so nothing was held to the catalog or the translation")
	}
}

// TestARouteOnHostPermissionStartsAndOneWithoutARowFails is the case for the
// translation check the enforcement module runs at Start. A route declared on a
// host permission starts, because the host's declared row and the module's own
// route reach the resolved translation. A route on a permission the catalog
// holds but no translation covers fails the start with ErrUnknownPermission —
// the failure a missing declaration row produces.
func TestARouteOnHostPermissionStartsAndOneWithoutARowFails(t *testing.T) {
	t.Parallel()

	covered := newGuardRegistry(t, authztest.NewCatalog(permDealRead),
		authzRouteModule("stand-in.route", "GET", "/deals", permDealRead))
	if err := covered.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: guardParticipants(),
	}); err != nil {
		t.Fatalf("a route on a permission the host declares and translates failed the start: %v", err)
	}

	undeclared := authz.Permission("crm.undeclared.read")
	uncovered := newGuardRegistry(t, authztest.NewCatalog(undeclared),
		authzRouteModule("stand-in.route", "GET", "/deals", undeclared))
	err := uncovered.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: guardParticipants(),
	})
	if !errors.Is(err, authz.ErrUnknownPermission) {
		t.Fatalf("a route on a permission the translation cannot express started with %v, want ErrUnknownPermission", err)
	}
}

// guardParticipants lists the stand-in modules an enforcement-only drive takes
// in, so the two drives above build the same set. The route module is named
// here as well — a module registered but not taken in declares nothing.
func guardParticipants() []string {
	return []string{
		authzguard.ModuleName,
		"stand-in.enforcer", "stand-in.catalog", "stand-in.router", "stand-in.route",
		authzBusinessModuleName,
	}
}

// newGuardRegistry builds a registry holding the enforcement module and the
// capabilities it takes, with a catalog of the given permissions, and the host's
// business module so its declared rows reach the resolved translation. A route
// on one of the host's permissions starts; a route on a permission the host does
// not declare fails the start's translation check.
func newGuardRegistry(t *testing.T, catalog authz.Catalog, decls ...core.Module) *core.Registry {
	t.Helper()
	guardModule := authzguard.Module()
	reader := authztest.ConfigReaderAll(t, businessGuardConfig, guardModule)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(guardModule)
	reg.Register(authzBusinessModule())
	authzSupply(reg, "stand-in.enforcer", (*authz.Enforcer)(nil), authztest.NewEnforcer())
	authzSupply(reg, "stand-in.catalog", (*authz.Catalog)(nil), catalog)
	authzSupply(reg, "stand-in.router", (*speedhttp.Router)(nil), authzRouterStub{})
	for _, m := range decls {
		reg.Register(m)
	}
	return reg
}

// The matrix cases use a manager that keeps roles, grants and bindings in maps
// keyed the way the policy tables' primary keys are, so a re-run that issues the
// same rows adds none and one that issues a different row adds it. It stands in
// for the policy module's manager, whose Start reads a database a host case
// would otherwise have to open and migrate; the shape it must honour — a grant
// keyed by (tenant, role, resource, action), a binding by (tenant, account,
// role) — is the interface's and the migration's, not this case's invention.

type authzRoleKey struct {
	tenant string
	role   authz.Role
}
type authzGrantKey struct {
	tenant         string
	role           authz.Role
	object, action string
}
type authzBindingKey struct {
	tenant, account string
	role            authz.Role
}

type authzFakeManager struct {
	roles    map[authzRoleKey]authz.RoleRecord
	grants   map[authzGrantKey]authz.GrantRecord
	bindings map[authzBindingKey]authz.Binding

	// grantPuts counts every PutGrant, duplicates included, so a case can tell a
	// re-run that re-issues the matrix from one that short-circuits.
	grantPuts int
}

var _ authz.Manager = (*authzFakeManager)(nil)

func newAuthzFakeManager() *authzFakeManager {
	return &authzFakeManager{
		roles:    map[authzRoleKey]authz.RoleRecord{},
		grants:   map[authzGrantKey]authz.GrantRecord{},
		bindings: map[authzBindingKey]authz.Binding{},
	}
}

func (m *authzFakeManager) Roles(_ context.Context, tenant string) ([]authz.RoleRecord, error) {
	var out []authz.RoleRecord
	for k, v := range m.roles {
		if k.tenant == tenant {
			out = append(out, v)
		}
	}
	return out, nil
}

func (m *authzFakeManager) Grants(_ context.Context, tenant string, role authz.Role) ([]authz.GrantRecord, error) {
	var out []authz.GrantRecord
	for k, v := range m.grants {
		if k.tenant == tenant && k.role == role {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Resource != out[j].Resource {
			return out[i].Resource < out[j].Resource
		}
		return out[i].Action < out[j].Action
	})
	return out, nil
}

func (m *authzFakeManager) Bindings(_ context.Context, tenant string) ([]authz.Binding, error) {
	var out []authz.Binding
	for k, v := range m.bindings {
		if k.tenant == tenant {
			out = append(out, v)
		}
	}
	return out, nil
}

func (m *authzFakeManager) BindingsOf(_ context.Context, tenant, account string) ([]authz.Binding, error) {
	var out []authz.Binding
	for k, v := range m.bindings {
		if k.tenant == tenant && k.account == account {
			out = append(out, v)
		}
	}
	return out, nil
}

func (m *authzFakeManager) PutRole(_ context.Context, r authz.RoleRecord) error {
	m.roles[authzRoleKey{r.Tenant, r.Role}] = r
	return nil
}

func (m *authzFakeManager) DeleteRole(_ context.Context, tenant string, role authz.Role) error {
	delete(m.roles, authzRoleKey{tenant, role})
	return nil
}

func (m *authzFakeManager) PutGrant(_ context.Context, g authz.GrantRecord) error {
	m.grantPuts++
	m.grants[authzGrantKey{g.Tenant, g.Role, g.Resource, g.Action}] = g
	return nil
}

func (m *authzFakeManager) DeleteGrant(_ context.Context, g authz.GrantRecord) error {
	delete(m.grants, authzGrantKey{g.Tenant, g.Role, g.Resource, g.Action})
	return nil
}

func (m *authzFakeManager) PutBinding(_ context.Context, b authz.Binding) error {
	m.bindings[authzBindingKey{b.Tenant, b.Account, b.Role}] = b
	return nil
}

func (m *authzFakeManager) DeleteBinding(_ context.Context, tenant, account string, role authz.Role) error {
	delete(m.bindings, authzBindingKey{tenant, account, role})
	return nil
}

// grantPairSetOf renders the (resource, action) pairs a role's permissions
// translate to, as a sorted set, so a comparison names every row that differs.
func grantPairSetOf(t *testing.T, perms []authz.Permission) []string {
	t.Helper()
	set := make([]string, 0, len(perms))
	for _, p := range perms {
		object, action, ok := resourceActionOf(p)
		if !ok {
			t.Fatalf("the permission %q has no translation row", p)
		}
		set = append(set, object+"\x00"+action)
	}
	sort.Strings(set)
	return set
}

// grantPairSet reads a tenant role's grant rows back through the manager and
// renders them the way grantPairSetOf does, so the two compare exactly.
func grantPairSet(t *testing.T, mgr authz.Manager, tenant string, role authz.Role) []string {
	t.Helper()
	rows, err := mgr.Grants(t.Context(), tenant, role)
	if err != nil {
		t.Fatalf("reading the %s role's grants in %q: %v", role, tenant, err)
	}
	set := make([]string, 0, len(rows))
	for _, r := range rows {
		set = append(set, r.Resource+"\x00"+r.Action)
	}
	sort.Strings(set)
	return set
}

func equalSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestTheTenantRoleMatrixIsWrittenExactly is the case for the four tenant roles'
// grant sets. Each role is granted exactly the permissions its tier carries: a
// role with one permission too many and one with one too few both fail, because
// the whole set is compared.
func TestTheTenantRoleMatrixIsWrittenExactly(t *testing.T) {
	t.Parallel()
	const tenant = "t_acme"
	mgr := newAuthzFakeManager()
	if err := provisionTenantRoles(t.Context(), mgr, tenant); err != nil {
		t.Fatalf("provisioning the tenant roles: %v", err)
	}
	for _, role := range tenantRoles {
		want := grantPairSetOf(t, tenantRoleGrants[role])
		got := grantPairSet(t, mgr, tenant, role)
		if !equalSets(got, want) {
			t.Errorf("the %s role carries %v, want exactly %v", role, got, want)
		}
		// The tenant roles are the host's own data, not builtin: the management
		// surface may re-grant or delete them, unlike the platform operator's
		// role.
		if record := mgr.roles[authzRoleKey{tenant, role}]; record.Builtin {
			t.Errorf("the %s role was written builtin, want an editable tenant role", role)
		}
	}
	// A role that was never provisioned holds nothing: the matrix is what the
	// case wrote, not what the manager would answer for any name.
	if got := grantPairSet(t, mgr, tenant, authz.Role("ghost")); len(got) != 0 {
		t.Errorf("an unprovisioned role carries %v, want none", got)
	}
}

// TestThePlatformOperatorMatrixIsWrittenExactly is the case for the platform
// operator's role: its grant set is exactly the management actions it carries —
// the identity system's own, plus the carried surface an operator administers in
// the platform domain (the settings and the audit trail) — and it carries
// neither authn.tenant.access (so an operator stays a stranger to every tenant)
// nor authn.impersonation.start (so a static subject cannot start one). The
// operator's binding in the platform domain is written too.
func TestThePlatformOperatorMatrixIsWrittenExactly(t *testing.T) {
	t.Parallel()
	mgr := newAuthzFakeManager()
	if err := provisionPlatformRoles(t.Context(), mgr); err != nil {
		t.Fatalf("provisioning the platform operator: %v", err)
	}
	domain := string(authz.PlatformDomain)
	want := grantPairSetOf(t, platformOperatorGrants)
	if got := grantPairSet(t, mgr, domain, authzRoleOperator); !equalSets(got, want) {
		t.Errorf("the operator role carries %v, want exactly %v", got, want)
	}
	// The operator role is builtin: the management surface refuses to delete it
	// or change its grants, since it carries the platform's own administration
	// and losing it would lock that administration out.
	if record := mgr.roles[authzRoleKey{domain, authzRoleOperator}]; !record.Builtin {
		t.Errorf("the operator role was written not builtin, want builtin: the management surface must not delete or re-grant it")
	}
	// The actions the operator must not hold, held to their pairs: a role
	// granted (tenant, access) would let an operator into every tenant, one
	// granted (impersonation, start) would let a static subject start an
	// impersonation with no session behind it, and authn.membership.manage and
	// authn.impersonation.read/revoke decide in the tenant domain, where the
	// operator holds no binding, so a platform-domain grant of one would be inert.
	for _, forbidden := range []authz.Permission{
		authz.PermissionTenantAccess, authz.PermissionImpersonateStart,
		authz.PermissionMembershipManage,
		authz.PermissionImpersonationRead, authz.PermissionImpersonationRevoke,
	} {
		object, action, ok := resourceActionOf(forbidden)
		if !ok {
			t.Fatalf("the carried permission %q has no translation row", forbidden)
		}
		for _, row := range grantPairSet(t, mgr, domain, authzRoleOperator) {
			if row == object+"\x00"+action {
				t.Errorf("the operator role carries %q (%s, %s), which it must not", forbidden, object, action)
			}
		}
	}
	bindings, err := mgr.Bindings(t.Context(), domain)
	if err != nil {
		t.Fatalf("reading the platform bindings: %v", err)
	}
	if len(bindings) != 1 || bindings[0].Account != string(operatorAccount) || bindings[0].Role != authzRoleOperator {
		t.Errorf("the platform bindings are %+v, want one binding of %s to the operator role", bindings, operatorAccount)
	}
}

// TestProvisioningIsIdempotent is the case for re-running the provisioners. A
// second run re-issues the same rows and adds none: the grant table holds
// exactly the matrix after any number of runs, and the second run reaches the
// manager rather than short-circuiting on a read.
func TestProvisioningIsIdempotent(t *testing.T) {
	t.Parallel()
	const tenant = "t_acme"
	mgr := newAuthzFakeManager()
	tenantRows := 0
	for _, role := range tenantRoles {
		tenantRows += len(tenantRoleGrants[role])
	}

	if err := provisionTenantRoles(t.Context(), mgr, tenant); err != nil {
		t.Fatalf("the first tenant provisioning run: %v", err)
	}
	first := len(mgr.grants)
	if first != tenantRows {
		t.Fatalf("the first tenant run wrote %d grant rows, want %d", first, tenantRows)
	}
	if err := provisionTenantRoles(t.Context(), mgr, tenant); err != nil {
		t.Fatalf("the second tenant provisioning run: %v", err)
	}
	if got := len(mgr.grants); got != first {
		t.Errorf("a second tenant run left %d grant rows, want the %d the first wrote", got, first)
	}
	if got := mgr.grantPuts; got != 2*tenantRows {
		t.Errorf("two tenant runs issued %d PutGrant calls, want %d: the second run must re-issue the matrix, not short-circuit",
			got, 2*tenantRows)
	}

	operatorRows := len(platformOperatorGrants)
	before := mgr.grantPuts
	if err := provisionPlatformRoles(t.Context(), mgr); err != nil {
		t.Fatalf("the first platform provisioning run: %v", err)
	}
	afterFirst := len(mgr.grants)
	if got := mgr.grantPuts - before; got != operatorRows {
		t.Fatalf("the first platform run issued %d PutGrant calls, want %d", got, operatorRows)
	}
	if err := provisionPlatformRoles(t.Context(), mgr); err != nil {
		t.Fatalf("the second platform provisioning run: %v", err)
	}
	if got := len(mgr.grants); got != afterFirst {
		t.Errorf("a second platform run left %d grant rows, want the %d the first wrote", got, afterFirst)
	}
	if got := len(mgr.bindings); got != 1 {
		t.Errorf("two platform runs left %d bindings, want one", got)
	}
}
