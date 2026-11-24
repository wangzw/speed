package self

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

var pathPermissions = authzSelfSpec.ResolvePath(pathPermissionsFragment)

const realmKey = "authz.self.realm"

func withRealm(endpoint string, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(realmKey, endpoint)
		handler(c)
	}
}

func realmOf(c *gin.Context) string {
	if v, ok := c.Get(realmKey); ok {
		if name, ok := v.(string); ok {
			return name
		}
	}
	return ""
}

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

func (p *product) declareOpen() {
	if p.gate == nil {
		return
	}
	p.gate.Open(http.MethodGet, pathPermissions)
}
