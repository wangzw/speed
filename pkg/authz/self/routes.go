package self

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// pathPermissions is the route's absolute path.
var pathPermissions = authzSelfSpec.ResolvePath(pathPermissionsFragment)

// realmKey is where the name of the endpoint the route was mounted on is put
// for the handler to read back as the realm of its 401.
const realmKey = "authz.self.realm"

// withRealm binds a handler to the endpoint it was mounted on, so the endpoint
// reaches it as the realm of its 401.
func withRealm(endpoint string, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(realmKey, endpoint)
		handler(c)
	}
}

// realmOf reads back the endpoint the route was mounted on; it is empty when the
// handler is called directly.
func realmOf(c *gin.Context) string {
	if v, ok := c.Get(realmKey); ok {
		if name, ok := v.(string); ok {
			return name
		}
	}
	return ""
}

// mount resolves every configured endpoint on the entry point and registers the
// permission read on each, behind the identity check and bound to that
// endpoint's realm. Every name is looked up before any route is registered.
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
	handler := p.requireIdentity(permissionsOperation.Handle(p.handlePermissions))
	for _, tgt := range targets {
		tgt.endpoint.Route(http.MethodGet, pathPermissions, withRealm(tgt.name, handler))
	}
	return nil
}

// declareOpen records the route as needing no permission on the
// route-declaration surface, when one is present.
func (p *product) declareOpen() {
	if p.gate == nil {
		return
	}
	p.gate.Open(http.MethodGet, pathPermissions)
}
