package profile

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

// pathProfile is the route's absolute path, resolved from the declaration
// against the API root the module's Spec carries.
var pathProfile = tenantProfileSpec.ResolvePath(pathProfileFragment)

// managementGroups names the route groups in the order the start diagnostic
// writes them. The whole surface is one group.
var managementGroups = []string{"profile"}

// realmKey is where the name of the endpoint the route was mounted on is put
// for the handler to read back. A handler that answers 401 itself writes the
// endpoint's name as the realm of its challenge.
const realmKey = "tenant.profile.realm"

// withRealm wraps a handler so the endpoint it was mounted on reaches it as the
// realm of its 401.
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

// routes is the closed set of profile routes, in the order they are mounted.
// Each handler is bound from the operation declared for it, and each is put
// behind the guard that makes the identity, registration and permission
// decision in the resolved tenant before the entry point binds the request. The
// write is also prescreened, so a body the binding refuses keeps this module's
// bad-request problem type.
func (p *product) routes() []route {
	routes := []route{
		{
			http.MethodGet, pathProfile, authz.PermissionTenantProfileRead,
			getOperation.Handle(p.handleGet),
		},
		{
			http.MethodPatch, pathProfile, authz.PermissionTenantProfileWrite,
			p.prescreenJSON(setOperation.Handle(p.handleSet)),
		},
	}
	for i := range routes {
		routes[i].handler = p.guardRoute(routes[i].method, routes[i].perm, routes[i].handler)
	}
	return routes
}

// mount resolves every configured endpoint on the entry point and registers the
// routes on each. Every name is looked up before any route is registered, so a
// name the entry point does not declare fails the startup naming that endpoint
// rather than leaving a partial mounting behind.
//
// Every route is wrapped with the tenant administration guard the tenant
// capability delivers, and a mount with no such guard is refused: a route
// mounted without it would let a caller authorized in one tenant reach another
// tenant's profile by naming that tenant in the path, and would let a caller
// address the platform domain, which this surface does not administer.
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
			return fmt.Errorf("%s: %w: %q: %w", ModuleName, tenant.ErrUnknownEndpoint, name, err)
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
// request reach the guard wrapper, which makes the decision. Declaring them to
// require a permission would make the middleware decide, and the module grants
// itself no permission.
func (p *product) declareOpen() {
	if p.gate == nil {
		return
	}
	for _, r := range p.routes() {
		p.gate.Open(r.method, r.path)
	}
}
