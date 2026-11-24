package admin

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

var managementGroups = []string{"settings"}

const realmKey = "settings.admin.realm"

func withRealm(endpoint string, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(realmKey, endpoint)
		handler(c)
	}
}

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

type route struct {
	method  string
	path    string
	perm    authz.Permission
	tenant  authz.Permission
	handler gin.HandlerFunc
}

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

func (p *product) declareOpen() {
	if p.gate == nil {
		return
	}
	for _, r := range p.routes() {
		p.gate.Open(r.method, r.path)
	}
}
