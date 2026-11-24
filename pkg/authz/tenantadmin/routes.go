package tenantadmin

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The paths the tenant management routes are mounted on, resolved from the
// declarations against the API root the module's Spec carries.
var (
	pathPermissions = tenantAdminSpec.ResolvePath(pathPermissionsFragment)

	pathRoles        = tenantAdminSpec.ResolvePath(pathRolesFragment)
	pathRole         = tenantAdminSpec.ResolvePath(pathRoleFragment)
	pathRoleBindings = tenantAdminSpec.ResolvePath(pathRoleBindingsFragment)
	pathGrants       = tenantAdminSpec.ResolvePath(pathGrantsFragment)
	pathGrant        = tenantAdminSpec.ResolvePath(pathGrantFragment)
	pathBindings     = tenantAdminSpec.ResolvePath(pathBindingsFragment)
	pathBindingOf    = tenantAdminSpec.ResolvePath(pathBindingOfFragment)
	pathBinding      = tenantAdminSpec.ResolvePath(pathBindingFragment)
)

// managementGroups names the route groups in the order the start diagnostic
// writes them.
var managementGroups = []string{"catalog", "roles", "grants", "bindings"}

// realmKey is where the name of the endpoint a route was mounted on is put for
// the handler to read back as the realm of its 401.
const realmKey = "authz.tenantadmin.realm"

// withRealm binds a route handler to the endpoint it was mounted on, so the
// endpoint reaches it as the realm of its 401.
func withRealm(endpoint string, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(realmKey, endpoint)
		handler(c)
	}
}

// route is one route: its method and path, the permission the handler-internal
// decision asks for, and its handler.
type route struct {
	method  string
	path    string
	perm    authz.Permission
	handler gin.HandlerFunc
}

// routes is the closed set of tenant management routes, in the order they are
// mounted.
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
			http.MethodGet, pathRoleBindings, authz.PermissionTenantRoleRead,
			roleBindingListOperation.Handle(p.handleRoleBindingList),
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
// registered.
//
// Every route is wrapped with the tenant administration guard; a mount with no
// such guard is refused, since a route mounted without it would let a caller
// authorized in one tenant reach another's data or the platform domain.
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
// route-declaration surface, when one is present, so the middleware lets a
// request reach the guard wrapper, which makes the decision.
func (p *product) declareOpen() {
	if p.gate == nil {
		return
	}
	for _, r := range p.routes() {
		p.gate.Open(r.method, r.path)
	}
}
