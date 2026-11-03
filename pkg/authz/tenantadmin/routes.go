package tenantadmin

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The paths the tenant management routes are mounted on, resolved from the
// declarations against the API root the module's Spec carries. The declarations
// write them relative to that root, so the fragment and the registration share
// one source.
var (
	pathPermissions = tenantAdminSpec.ResolvePath(pathPermissionsFragment)

	pathRoles     = tenantAdminSpec.ResolvePath(pathRolesFragment)
	pathRole      = tenantAdminSpec.ResolvePath(pathRoleFragment)
	pathGrants    = tenantAdminSpec.ResolvePath(pathGrantsFragment)
	pathGrant     = tenantAdminSpec.ResolvePath(pathGrantFragment)
	pathBindings  = tenantAdminSpec.ResolvePath(pathBindingsFragment)
	pathBindingOf = tenantAdminSpec.ResolvePath(pathBindingOfFragment)
	pathBinding   = tenantAdminSpec.ResolvePath(pathBindingFragment)
)

// managementGroups names the route groups in the order the start diagnostic
// writes them. Every group is always mounted, because the manager, the catalog,
// the permission reader and the membership checker it needs are required.
var managementGroups = []string{"catalog", "roles", "grants", "bindings"}

// realmKey is where the name of the endpoint a route was mounted on is put for
// the handler to read back. A handler that answers 401 itself writes the
// endpoint's name as the realm of its challenge — the name of the protection
// space, the same realm the admission and authorization layers use.
const realmKey = "authz.tenantadmin.realm"

// withRealm wraps a route handler so the endpoint it was mounted on reaches it
// as the realm of its 401.
func withRealm(endpoint string, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(realmKey, endpoint)
		handler(c)
	}
}

// route is one route: the method and path it answers, the permission the
// handler-internal decision asks for, and the handler that serves it.
type route struct {
	method  string
	path    string
	perm    authz.Permission
	handler gin.HandlerFunc
}

// routes is the closed set of tenant management routes, in the order they are
// mounted. Each handler is bound from the operation declared for it, and each
// is put behind the guard that makes the identity, registration and permission
// decision in the tenant the path names before the entry point binds the
// request.
func (p *product) routes() []route {
	routes := []route{
		{
			http.MethodGet, pathPermissions, authz.PermissionTenantRoleRead,
			permissionListOperation.Handle(p.handlePermissionList),
		},

		{
			http.MethodGet, pathRoles, authz.PermissionTenantRoleRead,
			roleListOperation.Handle(p.handleRoleList),
		},
		{
			http.MethodPut, pathRole, authz.PermissionTenantRoleManage,
			prescreenRoleWrite(rolePutOperation.Handle(p.handleRolePut)),
		},
		{
			http.MethodDelete, pathRole, authz.PermissionTenantRoleManage,
			roleDeleteOperation.Handle(p.handleRoleDelete),
		},

		{
			http.MethodGet, pathGrants, authz.PermissionTenantRoleRead,
			grantListOperation.Handle(p.handleGrantList),
		},
		{
			http.MethodPut, pathGrant, authz.PermissionTenantRoleManage,
			grantPutOperation.Handle(p.handleGrantPut),
		},
		{
			http.MethodDelete, pathGrant, authz.PermissionTenantRoleManage,
			grantDeleteOperation.Handle(p.handleGrantDelete),
		},

		{
			http.MethodGet, pathBindings, authz.PermissionTenantRoleRead,
			bindingListOperation.Handle(p.handleBindingList),
		},
		{
			http.MethodGet, pathBindingOf, authz.PermissionTenantRoleRead,
			bindingOfOperation.Handle(p.handleBindingOf),
		},
		{
			http.MethodPut, pathBinding, authz.PermissionTenantRoleManage,
			bindingPutOperation.Handle(p.handleBindingPut),
		},
		{
			http.MethodDelete, pathBinding, authz.PermissionTenantRoleManage,
			bindingDeleteOperation.Handle(p.handleBindingDelete),
		},
	}
	for i := range routes {
		routes[i].handler = p.guardRoute(routes[i].method, routes[i].perm, routes[i].handler)
	}
	return routes
}

// mount resolves every configured endpoint on the entry point and registers the
// tenant management routes on each. Every name is looked up before any route is
// registered, so a name the entry point does not declare fails the startup
// naming that endpoint rather than leaving a partial mounting behind.
//
// Every route is wrapped with the tenant administration guard the tenant
// capability delivers, and a mount with no such guard is refused: a route
// mounted without it would let a caller authorized in one tenant reach another
// tenant's policy data by naming that tenant in the path, and would let a
// caller address the platform domain, which this surface does not administer.
func (p *product) mount(router speedhttp.Router) error {
	if p.routeGuard == nil {
		return fmt.Errorf("%s: the tenant-scoped routes need the tenant administration guard, and no "+
			"module delivers it. Write the tenant resolving module's name into this command", ModuleName)
	}
	type target struct {
		name     string
		endpoint speedhttp.Endpoint
	}
	targets := make([]target, 0, len(p.cfg.endpoints))
	for _, name := range p.cfg.endpoints {
		endpoint, err := router.Endpoint(name)
		if err != nil {
			return fmt.Errorf("%s: %w: %q: %w", ModuleName, authz.ErrUnknownEndpoint, name, err)
		}
		targets = append(targets, target{name: name, endpoint: endpoint})
	}
	for _, tgt := range targets {
		for _, r := range p.routes() {
			tgt.endpoint.Route(r.method, r.path, withRealm(tgt.name, p.routeGuard.GuardTenantAdmin(r.handler)))
		}
	}
	return nil
}

// declareOpen records every route as needing no permission on the
// route-declaration surface, when one is present.
//
// The routes are declared open so that the authorization middleware lets a
// request reach the guard wrapper: the wrapper makes the decision, in the
// tenant the path names, on the permission the route table carries. Declaring
// them to require a permission would make the middleware decide, and the module
// grants itself no permission.
func (p *product) declareOpen() {
	if p.gate == nil {
		return
	}
	for _, r := range p.routes() {
		p.gate.Open(r.method, r.path)
	}
}
