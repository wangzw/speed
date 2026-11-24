package admin

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The paths the management routes are mounted on, resolved from the declarations
// against the API root the module's Spec carries.
var (
	pathPermissions = authzAdminSpec.ResolvePath(pathPermissionsFragment)

	pathRoles     = authzAdminSpec.ResolvePath(pathRolesFragment)
	pathRole      = authzAdminSpec.ResolvePath(pathRoleFragment)
	pathGrants    = authzAdminSpec.ResolvePath(pathGrantsFragment)
	pathGrant     = authzAdminSpec.ResolvePath(pathGrantFragment)
	pathBindings  = authzAdminSpec.ResolvePath(pathBindingsFragment)
	pathBindingOf = authzAdminSpec.ResolvePath(pathBindingOfFragment)
	pathBinding   = authzAdminSpec.ResolvePath(pathBindingFragment)
)

// managementGroups names the route groups in the order the start diagnostic
// writes them.
var managementGroups = []string{"catalog", "roles", "grants", "bindings"}

// realmKey is where the name of the endpoint a management route was mounted on
// is put for the handler to read back as the realm of its 401.
const realmKey = "authz.admin.realm"

// withRealm binds a management handler to the endpoint it was mounted on, so the
// endpoint reaches it as the realm of its 401.
func withRealm(endpoint string, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(realmKey, endpoint)
		handler(c)
	}
}

// route is one management route: its method and path, the permission the
// handler-internal decision asks for, and its handler.
type route struct {
	method  string
	path    string
	perm    authz.Permission
	handler gin.HandlerFunc
}

// routes is the closed set of management routes, in the order they are mounted.
func (p *product) routes() []route {
	routes := []route{
		{
			http.MethodGet, pathPermissions, authz.PermissionRoleRead,
			permissionListOperation.Handle(p.handlePermissionList),
		},

		{
			http.MethodGet, pathRoles, authz.PermissionRoleRead,
			roleListOperation.Handle(p.handleRoleList),
		},
		{
			http.MethodPut, pathRole, authz.PermissionRoleManage,
			prescreenRoleWrite(rolePutOperation.Handle(p.handleRolePut)),
		},
		{
			http.MethodDelete, pathRole, authz.PermissionRoleManage,
			roleDeleteOperation.Handle(p.handleRoleDelete),
		},

		{
			http.MethodGet, pathGrants, authz.PermissionRoleRead,
			grantListOperation.Handle(p.handleGrantList),
		},
		{
			http.MethodPut, pathGrant, authz.PermissionRoleManage,
			grantPutOperation.Handle(p.handleGrantPut),
		},
		{
			http.MethodDelete, pathGrant, authz.PermissionRoleManage,
			grantDeleteOperation.Handle(p.handleGrantDelete),
		},

		{
			http.MethodGet, pathBindings, authz.PermissionRoleRead,
			bindingListOperation.Handle(p.handleBindingList),
		},
		{
			http.MethodGet, pathBindingOf, authz.PermissionRoleRead,
			bindingOfOperation.Handle(p.handleBindingOf),
		},
		{
			http.MethodPut, pathBinding, authz.PermissionRoleManage,
			bindingPutOperation.Handle(p.handleBindingPut),
		},
		{
			http.MethodDelete, pathBinding, authz.PermissionRoleManage,
			bindingDeleteOperation.Handle(p.handleBindingDelete),
		},
	}
	for i := range routes {
		routes[i].handler = p.guardRoute(routes[i].method, routes[i].perm, routes[i].handler)
	}
	return routes
}

// mount resolves every configured endpoint on the entry point and registers the
// management routes on each. Every name is looked up before any route is
// registered.
func (p *product) mount(router speedhttp.Router) error {
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
			tgt.endpoint.Route(r.method, r.path, withRealm(tgt.name, r.handler))
		}
	}
	return nil
}

// declareOpen records every management route as needing no permission on the
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
