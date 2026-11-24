package profile

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

var pathProfile = tenantProfileSpec.ResolvePath(pathProfileFragment)

const realmKey = "tenant.profile.realm"

func withRealm(endpoint string, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(realmKey, endpoint)
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

// Every endpoint name is looked up before any route is registered, so an
// unknown name fails startup without leaving a partial mounting.
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

// The routes are declared open so the authorization middleware lets the request
// reach the guard wrapper, which makes the decision.
func (p *product) declareOpen() {
	if p.gate == nil {
		return
	}
	for _, r := range p.routes() {
		p.gate.Open(r.method, r.path)
	}
}
