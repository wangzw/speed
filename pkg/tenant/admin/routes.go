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
	pathTenants       = tenantAdminSpec.ResolvePath(pathTenantsFragment)
	pathTenant        = tenantAdminSpec.ResolvePath(pathTenantFragment)
	pathTenantProfile = tenantAdminSpec.ResolvePath(pathTenantProfileFragment)
	pathTenantStatus  = tenantAdminSpec.ResolvePath(pathTenantStatusFragment)
	pathTenantOwner   = tenantAdminSpec.ResolvePath(pathTenantOwnerFragment)

	// The tenant-surface routes. They are resolved against the tenants-scope
	// root, so the tenant is the first segment after /api/v1/tenants.
	pathOwnerTransfer = tenantOwnerSpec.ResolvePath(pathOwnerTransferFragment)
	pathOrganization  = tenantOwnerSpec.ResolvePath(pathOrganizationFragment)
)

// managementGroups names the route groups in the order the start diagnostic
// writes them. The whole surface is one group, and it is always mounted, because
// the roster it reads and writes is required.
var managementGroups = []string{"tenants"}

// realmKey is where the name of the endpoint a management route was mounted on
// is put for the handler to read back. A handler that answers 401 itself writes
// the endpoint's name as the realm of its challenge — the name of the protection
// space, the same realm the admission and authorization layers use — and the
// endpoint name is known only where the routes are mounted, which is why it is
// carried on the request rather than read in the handler.
const realmKey = "tenant.admin.realm"

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

// route is one route: the method and path it answers, and the handler that
// serves it. perm is the permission the handler-internal decision asks for on
// the platform surface; the tenant surface decides by owner identity and asks
// none.
type route struct {
	method  string
	path    string
	perm    authz.Permission
	handler gin.HandlerFunc
}

// routes is the closed set of platform-management routes, in the order they are
// mounted. Each handler is bound from the operation declared for it, so it
// receives the declared input and writes the declared answer, and each is put
// behind the guard that makes the identity and permission decision before the
// entry point binds the request. The write routes are also prescreened, so a
// body the binding refuses keeps this module's bad-request problem type.
//
// Every route here is platform staff's: the organization's owner reaches the
// transfer and the deletion on the tenant surface, never through this one.
func (p *product) routes() []route {
	routes := []route{
		{
			http.MethodGet, pathTenants, PermissionTenantRead,
			listOperation.Handle(p.handleTenantList),
		},
		{
			http.MethodPost, pathTenants, PermissionTenantManage,
			prescreenJSONFields[createBody](p, nil, registrationForbiddenFields,
				createOperation.Handle(p.handleTenantCreate)),
		},
		{
			http.MethodGet, pathTenant, PermissionTenantRead,
			getOperation.Handle(p.handleTenantGet),
		},
		{
			http.MethodPut, pathTenantOwner, PermissionTenantManage,
			prescreenJSON[transferOwnerInput](p, transferOwnerOperation.Handle(p.handleTenantTransferOwner)),
		},
		{
			http.MethodDelete, pathTenant, PermissionTenantManage,
			deleteOperation.Handle(p.handleTenantDelete),
		},
		{
			http.MethodPatch, pathTenantProfile, PermissionTenantManage,
			prescreenJSONFields[setProfileInput](p, profileWriteFields, nil,
				setProfileOperation.Handle(p.handleTenantSetProfile)),
		},
		{
			http.MethodPost, pathTenantStatus, PermissionTenantManage,
			prescreenJSON[setStatusInput](p, setStatusOperation.Handle(p.handleTenantSetStatus)),
		},
	}
	for i := range routes {
		routes[i].handler = p.guardRoute(routes[i].method, routes[i].perm, routes[i].handler)
	}
	return routes
}

// tenantRoutes is the closed set of tenant-surface routes: the organization's
// own owner transfers it or ends it. Each handler is wrapped by the tenant
// administration guard, which holds the path tenant to the tenant the request
// resolved to and refuses the platform domain, and then by the owner decision,
// which reads the caller and refuses anybody who is not the row's owner.
func (p *product) tenantRoutes() []route {
	routes := []route{
		{
			http.MethodPut, pathOwnerTransfer, "",
			p.guardOwnerRoute(http.MethodPut,
				tenantTransferOwnerOperation.Handle(p.handleTenantOwnerTransfer)),
		},
		{
			http.MethodDelete, pathOrganization, "",
			p.guardOwnerRoute(http.MethodDelete,
				tenantDeleteOperation.Handle(p.handleTenantOwnerDelete)),
		},
	}
	for i := range routes {
		routes[i].handler = p.routeGuard.GuardTenantAdmin(routes[i].handler)
	}
	return routes
}

// mount resolves every configured endpoint on the entry point and registers the
// routes on each. Every name is looked up before any route is registered, so a
// name the entry point does not declare fails the startup naming that endpoint
// rather than leaving a partial mounting behind.
//
// The platform routes mount on the endpoints tenant.admin.endpoints names. The
// tenant-surface routes mount on tenant.admin.tenant-endpoints, the endpoints
// that carry tenant resolution and the tenant administration guard.
func (p *product) mount(router speedhttp.Router) error {
	type target struct {
		name     string
		endpoint speedhttp.Endpoint
	}
	resolve := func(names []string) ([]target, error) {
		targets := make([]target, 0, len(names))
		for _, name := range names {
			endpoint, err := router.Endpoint(name)
			if err != nil {
				return nil, fmt.Errorf("%s: %w: %q: %w", ModuleName, authz.ErrUnknownEndpoint, name, err)
			}
			targets = append(targets, target{name: name, endpoint: endpoint})
		}
		return targets, nil
	}
	platformTargets, err := resolve(p.cfg.endpoints)
	if err != nil {
		return err
	}
	tenantTargets, err := resolve(p.cfg.tenantEndpoints)
	if err != nil {
		return err
	}
	for _, tgt := range platformTargets {
		// The tenant every module's platform management route names is an
		// name on the request surface and an identifier behind it. The layer
		// is on the endpoint so that it precedes every module's handler under
		// the prefix, not only this module's routes.
		tgt.endpoint.Use(p.tenantNameLayer())
		for _, r := range p.routes() {
			tgt.endpoint.Route(r.method, r.path, withRealm(tgt.name, r.handler))
		}
	}
	for _, tgt := range tenantTargets {
		for _, r := range p.tenantRoutes() {
			tgt.endpoint.Route(r.method, r.path, withRealm(tgt.name, r.handler))
		}
	}
	return nil
}

// declareOpen records every route as needing no permission on the
// route-declaration surface, when one is present.
//
// The routes are declared open so that the authorization middleware lets a
// request reach the guard wrapper: the wrapper makes the decision, on the
// permission the request's method implies. Declaring them to require a
// permission would make the middleware decide, and the module grants itself no
// permission. With no declaration surface the routes carry no declaration, and a
// guarded endpoint answers them as undeclared — which is why a command that mounts
// these routes should name the enforcement module too.
func (p *product) declareOpen() {
	if p.gate == nil {
		return
	}
	for _, r := range p.routes() {
		p.gate.Open(r.method, r.path)
	}
	for _, r := range p.tenantRoutes() {
		p.gate.Open(r.method, r.path)
	}
}
