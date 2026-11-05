package tenantadmin

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// managementGroups names the route groups in the order the start diagnostic
// writes them. The whole surface is one group, and it is always mounted, because
// the settings surface it reads and writes is required.
var managementGroups = []string{"settings"}

// realmKey is where the name of the endpoint a management route was mounted on
// is put for the handler to read back. A route that answers 401 itself writes
// the endpoint's name as the realm of its challenge — the name of the protection
// space, the same realm the admission and authorization layers use — and the
// endpoint name is known only where the routes are mounted, which is why it is
// carried on the request rather than read in the handler.
const realmKey = "settings.tenantadmin.realm"

// withRealm wraps a management route so the endpoint it was mounted on reaches
// it as the realm of its 401. The same declaration is mounted on every
// configured endpoint, so the realm is bound per endpoint here rather than held
// by the route.
func withRealm(endpoint string, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(realmKey, endpoint)
		handler(c)
	}
}

// guardRoute wraps an operation's handler with the decision this module makes
// itself: the identity the request carries, the registration state of a write's
// actor, the permission the route asks for in the path tenant's domain, and the
// refusal of the platform surface's layer and tenant query parameters. The routes
// are declared open, so the middleware lets every request reach this wrapper; it
// refuses an anonymous request with the realm challenge, an unverified
// self-registration's write with 403 contact-required, a permission shortfall
// with the guard's own answer, and a layer or tenant query parameter with the
// capability's bad-request problem, all before the entry point binds the request
// and before any settings call.
//
// The permission is fixed by the route, not by anything on the request: this
// surface acts on the tenant in the path alone, so it asks settings.tenant.read
// on a read and settings.tenant.write on a write, in that tenant's domain. There
// is no second channel that could point the decision at the global layer.
//
// The write decision is derived from the method rather than passed in: a route
// that is not a read is a write, so a write route added to the set cannot be
// left ungated by a caller that forgot the flag.
func (p *product) guardRoute(method string, perm authz.Permission, handler gin.HandlerFunc) gin.HandlerFunc {
	write := method != http.MethodGet
	return func(c *gin.Context) {
		id, ok := p.caller(c)
		if !ok {
			return
		}
		if write && !p.requireVerifiedAccount(c, id) {
			return
		}
		tenant := c.Param("tenant")
		if !p.require(c, perm, authz.Domain(tenant)) {
			return
		}
		if hasLayerQuery(c) {
			p.bad(c)
			return
		}
		handler(c)
	}
}

// route is one management route: the method and path it answers, the permission
// the path tenant's domain is asked for, and the handler that serves it, the
// decision already bound into the handler's wrapper. The path is absolute,
// resolved from the declaration against the module's API root once here so the
// mounting and the declaration share one source.
type route struct {
	method  string
	path    string
	perm    authz.Permission
	handler gin.HandlerFunc
}

// routes is the closed set of management routes, in the order they are mounted.
// Each route is bound from the operation declared for it, so the handler receives
// the declared input and writes the declared answer, and each is put behind the
// guard that makes the identity, registration and permission decision before the
// entry point binds the request.
func (p *product) routes() []route {
	routes := []route{
		{
			http.MethodGet, tenantAdminSpec.ResolvePath(pathSettings),
			authz.PermissionTenantSettingsRead,
			listOperation.Handle(p.handleList),
		},
		{
			http.MethodPut, tenantAdminSpec.ResolvePath(pathNamespaceKey),
			authz.PermissionTenantSettingsWrite,
			setOperation.Handle(p.handleSet),
		},
		{
			http.MethodDelete, tenantAdminSpec.ResolvePath(pathNamespaceKey),
			authz.PermissionTenantSettingsWrite,
			removeOperation.Handle(p.handleRemove),
		},
	}
	for i := range routes {
		routes[i].handler = p.guardRoute(routes[i].method, routes[i].perm, routes[i].handler)
	}
	return routes
}

// mount resolves every configured endpoint on the entry point and registers the
// management routes on each, wrapped with the tenant administration guard so the
// tenant the path names is the tenant the request resolved to and is not the
// platform domain. Every name is looked up before any route is registered, so a
// name the entry point does not declare fails the startup naming that endpoint
// rather than leaving a partial mounting behind.
//
// The guard is required. These are tenant-scoped routes: without it a request
// could address one tenant in the path while resolving to another, and the
// handler would act on the path's; the platform domain would also become
// addressable on a surface that does not administer it. An assembly that mounts
// the routes without one fails the startup instead.
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

// declareOpen records every management route as needing no permission on the
// route-declaration surface, when one is present.
//
// The routes are declared open so that the authorization middleware lets a
// request reach the route: the route's own authorization step makes the decision,
// on the permission its method implies, through the handler-internal guard.
// Declaring them to require a permission would make the middleware decide, and
// the module grants itself no permission. With no declaration surface the routes
// carry no declaration, and a guarded endpoint answers them as undeclared — which
// is why a command that mounts these routes should name the enforcement module
// too.
func (p *product) declareOpen() {
	if p.gate == nil {
		return
	}
	for _, r := range p.routes() {
		p.gate.Open(r.method, r.path)
	}
}
