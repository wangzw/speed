package admin

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
const realmKey = "settings.admin.realm"

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
// actor, the layer its query string names and the permission that layer implies.
// The routes are declared open, so the middleware lets every request reach this
// wrapper; it refuses an anonymous request with the realm challenge, an
// unverified self-registration's write with 403 contact-required, an unreadable
// layer selection with the capability's bad-request problem, and a permission
// shortfall with the guard's own answer, all before the entry point binds the
// request and before any settings call.
//
// The permission follows the layer. The global layer is a platform operation and
// is decided on the global codes — settings.read for a read, settings.write for a
// write — in the platform domain. A tenant layer is decided on that tenant's own
// codes — settings.tenant.read or settings.tenant.write — in that tenant's
// domain. The two sets are decided apart: a holder of only the global codes
// cannot reach a tenant layer, and a tenant administrator holding only the tenant
// codes cannot reach the global one. The global code is the route's own, and the
// tenant code stands beside it so each route states both rather than deriving one
// from the other.
//
// The write decision is derived from the method rather than passed in: a route
// that is not a read is a write, so a write route added to the set cannot be
// left ungated by a caller that forgot the flag. The method is the one fact the
// route already carries, and deriving from it is what makes the gate follow the
// surface as it grows.
//
// The decision stays in front of the declared handler because the typed handler
// receives no request: the realm is bound per endpoint at the mount, and the
// guard's refusals are written through it. The settled selection travels to the
// handler on the request context, because the query string names the tenant by
// alias while the handler acts on the identifier it resolved to.
func (p *product) guardRoute(method string, globalPerm, tenantPerm authz.Permission, handler gin.HandlerFunc) gin.HandlerFunc {
	write := method != http.MethodGet
	return func(c *gin.Context) {
		id, ok := p.caller(c)
		if !ok {
			return
		}
		if write && !p.requireVerifiedAccount(c, id) {
			return
		}
		sel, ok := p.selectLayer(c)
		if !ok {
			return
		}
		perm := globalPerm
		if sel.name == layerTenant {
			perm = tenantPerm
		}
		if !p.require(c, perm, sel.domain) {
			return
		}
		c.Request = c.Request.WithContext(withSelection(c.Request.Context(), sel))
		handler(c)
	}
}

// route is one management route: the method and path it answers, the permissions
// the two layers' domains are asked for, and the handler that serves it, the
// decision already bound into the handler's wrapper. The path is absolute,
// resolved from the declaration against the module's API root once here so the
// mounting and the declaration share one source.
type route struct {
	method  string
	path    string
	perm    authz.Permission
	tenant  authz.Permission
	handler gin.HandlerFunc
}

// routes is the closed set of management routes, in the order they are mounted.
// Each route is bound from the operation declared for it, so the handler
// receives the declared input and writes the declared answer, and each is put
// behind the guard that makes the identity, registration and permission
// decision before the entry point binds the request.
func (p *product) routes() []route {
	routes := []route{
		{
			http.MethodGet, adminSpec.ResolvePath(pathSettings),
			authz.PermissionSettingsRead, authz.PermissionTenantSettingsRead,
			listOperation.Handle(p.handleList),
		},
		{
			http.MethodGet, adminSpec.ResolvePath(pathNamespace),
			authz.PermissionSettingsRead, authz.PermissionTenantSettingsRead,
			namespaceOperation.Handle(p.handleListNamespace),
		},
		{
			http.MethodPut, adminSpec.ResolvePath(pathNamespaceKey),
			authz.PermissionSettingsWrite, authz.PermissionTenantSettingsWrite,
			setOperation.Handle(p.handleSet),
		},
		{
			http.MethodDelete, adminSpec.ResolvePath(pathNamespaceKey),
			authz.PermissionSettingsWrite, authz.PermissionTenantSettingsWrite,
			removeOperation.Handle(p.handleRemove),
		},
	}
	for i := range routes {
		routes[i].handler = p.guardRoute(routes[i].method, routes[i].perm, routes[i].tenant, routes[i].handler)
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
// request reach the route: the route's own authorization step makes the
// decision, on the permission the request's method implies, through the
// handler-internal guard. Declaring them to require a permission would make the
// middleware decide, and the module grants itself no permission. With no
// declaration surface the routes carry no declaration, and a guarded endpoint
// answers them as undeclared — which is why a command that mounts these routes
// should name the enforcement module too.
func (p *product) declareOpen() {
	if p.gate == nil {
		return
	}
	for _, r := range p.routes() {
		p.gate.Open(r.method, r.path)
	}
}
