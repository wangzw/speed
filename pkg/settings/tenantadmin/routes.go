package tenantadmin

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

var managementGroups = []string{"settings"}

const realmKey = "settings.tenantadmin.realm"

func withRealm(endpoint string, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(realmKey, endpoint)
		handler(c)
	}
}

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

type route struct {
	method  string
	path    string
	perm    authz.Permission
	handler gin.HandlerFunc
}

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

// Without the tenant administration guard startup fails: the path tenant and the platform domain would be unguarded.
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

func (p *product) declareOpen() {
	if p.gate == nil {
		return
	}
	for _, r := range p.routes() {
		p.gate.Open(r.method, r.path)
	}
}
