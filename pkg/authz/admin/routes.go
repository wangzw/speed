package admin

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The paths the management routes are mounted on, resolved from the declarations
// against the API root the module's Spec carries. The declarations write them
// relative to that root, so the fragment and the registration share one source.
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
// writes them. Every group is always mounted, because the manager and the
// catalog it reads are required.
var managementGroups = []string{"catalog", "roles", "grants", "bindings"}

// realmKey is where the name of the endpoint a management route was mounted on
// is put for the handler to read back. A handler that answers 401 itself writes
// the endpoint's name as the realm of its challenge — the name of the protection
// space, the same realm the admission and authorization layers use — and the
// endpoint name is known only where the routes are mounted, which is why it is
// carried on the request rather than read in the handler.
const realmKey = "authz.admin.realm"

// withRealm wraps a management handler so the endpoint it was mounted on reaches
// it as the realm of its 401. The same handler is mounted on every configured
// endpoint, so the realm is bound per endpoint here rather than held by the
// handler.
func withRealm(endpoint string, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(realmKey, endpoint)
		handler(c)
	}
}

// route is one management route: the method and path it answers, the permission
// the handler-internal decision asks for, and the handler that serves it.
type route struct {
	method  string
	path    string
	perm    authz.Permission
	handler gin.HandlerFunc
}

// routes is the closed set of management routes, in the order they are mounted.
// Each handler is bound from the operation declared for it, so it receives the
// declared input and writes the declared answer, and each is put behind the
// guard that makes the identity and permission decision before the entry point
// binds the request.
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
// registered, so a name the entry point does not declare fails the startup
// naming that endpoint rather than leaving a partial mounting behind.
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
// route-declaration surface, when one is present.
//
// The routes are declared open so that the authorization middleware lets a
// request reach the guard wrapper: the wrapper makes the decision, on the
// permission the request's method implies. Declaring them to require a
// permission would make the middleware decide, and the module grants itself no
// permission. With no declaration surface the routes carry no declaration, and a
// guarded endpoint answers them as undeclared — which is why a command that
// mounts these routes should name the enforcement module too.
func (p *product) declareOpen() {
	if p.gate == nil {
		return
	}
	for _, r := range p.routes() {
		p.gate.Open(r.method, r.path)
	}
}
