package self

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

// pathTenants is the route's absolute path, resolved from the declaration
// against the API root the module's Spec carries.
var pathTenants = tenantSelfSpec.ResolvePath(pathTenantsFragment)

// realmKey is where the name of the endpoint the route was mounted on is put
// for the handler to read back. A handler that answers 401 itself writes the
// endpoint's name as the realm of its challenge — the name of the protection
// space, the same realm the admission and authorization layers use — and the
// endpoint name is known only where the route is mounted, which is why it is
// carried on the request rather than read in the handler.
const realmKey = "tenant.self.realm"

// withRealm wraps a handler so the endpoint it was mounted on reaches it as the
// realm of its 401. The same handler is mounted on every configured endpoint,
// so the realm is bound per endpoint here rather than held by the handler.
func withRealm(endpoint string, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(realmKey, endpoint)
		handler(c)
	}
}

// realmOf reads back the endpoint the route was mounted on. It is empty off the
// mounted route — the handler called directly — and the challenge is then
// written with an empty realm.
func realmOf(c *gin.Context) string {
	if v, ok := c.Get(realmKey); ok {
		if name, ok := v.(string); ok {
			return name
		}
	}
	return ""
}

// mount resolves every configured endpoint on the entry point and registers the
// tenant read on each, behind the identity check and bound to that endpoint's
// realm. Every name is looked up before any route is registered, so a name the
// entry point does not declare fails the startup naming that endpoint rather
// than leaving a partial mounting behind.
//
// When the assembly delivers the gateway's admission surface, each mounted
// route is registered Impersonable. An impersonating identity is confined to
// its grant's tenant; the read's answer is narrowed to that tenant in the
// handler, so the registration hands the impersonator the one name the grant
// already names and none of the impersonated account's other memberships. It is
// what lets a host name the grant's tenant after a full page load, when the
// roster cache it read before is gone.
func (p *product) mount(router speedhttp.Router) error {
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
	handler := p.requireIdentity(tenantsOperation.Handle(p.handleTenants))
	for _, tgt := range targets {
		tgt.endpoint.Route(http.MethodGet, pathTenants, withRealm(tgt.name, handler))
		if p.admission != nil {
			p.admission.Impersonable(tgt.name, http.MethodGet, pathTenants)
		}
	}
	return nil
}

// declareOpen records the route as needing no permission on the
// route-declaration surface, when one is present.
//
// The route is declared open so that the authorization middleware lets a
// request reach the handler: every authenticated caller may read its own
// memberships, so there is no permission to require. With no declaration
// surface the route carries no declaration, and a guarded endpoint answers it as
// undeclared — which is why a command that mounts it should name the enforcement
// module too. The identity requirement is the gateway's: a route on a guarded
// endpoint carries no exemption, so admission refuses a request with no
// credential before any handler runs.
func (p *product) declareOpen() {
	if p.gate == nil {
		return
	}
	p.gate.Open(http.MethodGet, pathTenants)
}
