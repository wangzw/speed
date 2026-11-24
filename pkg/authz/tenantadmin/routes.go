package tenantadmin

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

var (
	pathPermissions = tenantAdminSpec.ResolvePath(pathPermissionsFragment)

	pathRoles        = tenantAdminSpec.ResolvePath(pathRolesFragment)
	pathRole         = tenantAdminSpec.ResolvePath(pathRoleFragment)
	pathRoleBindings = tenantAdminSpec.ResolvePath(pathRoleBindingsFragment)
	pathGrants       = tenantAdminSpec.ResolvePath(pathGrantsFragment)
	pathGrant        = tenantAdminSpec.ResolvePath(pathGrantFragment)
	pathBindings     = tenantAdminSpec.ResolvePath(pathBindingsFragment)
	pathBindingOf    = tenantAdminSpec.ResolvePath(pathBindingOfFragment)
	pathBinding      = tenantAdminSpec.ResolvePath(pathBindingFragment)
)

var managementGroups = []string{"catalog", "roles", "grants", "bindings"}

const realmKey = "authz.tenantadmin.realm"

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
			http.MethodGet, pathPermissions, authz.PermissionTenantRoleRead,
			permissionListOperation.Handle(p.handlePermissionList),
		},

		{
			http.MethodGet, pathRoles, authz.PermissionTenantRoleRead,
			roleListOperation.Handle(p.handleRoleList),
		},
		{
			http.MethodGet, pathRoleBindings, authz.PermissionTenantRoleRead,
			roleBindingListOperation.Handle(p.handleRoleBindingList),
		},
		{
			http.MethodPut, pathRole, authz.PermissionTenantRoleManage,
			prescreenRoleWrite(rolePutOperation.Handle(p.handleRolePut)),
		},
		{
			http.MethodDelete, pathRole, authz.PermissionTenantRoleManage,
			roleDeleteOperation.Handle(p.handleRoleDelete),
		},

		{
			http.MethodGet, pathGrants, authz.PermissionTenantRoleRead,
			grantListOperation.Handle(p.handleGrantList),
		},
		{
			http.MethodPut, pathGrant, authz.PermissionTenantRoleManage,
			grantPutOperation.Handle(p.handleGrantPut),
		},
		{
			http.MethodDelete, pathGrant, authz.PermissionTenantRoleManage,
			grantDeleteOperation.Handle(p.handleGrantDelete),
		},

		{
			http.MethodGet, pathBindings, authz.PermissionTenantRoleRead,
			bindingListOperation.Handle(p.handleBindingList),
		},
		{
			http.MethodGet, pathBindingOf, authz.PermissionTenantRoleRead,
			bindingOfOperation.Handle(p.handleBindingOf),
		},
		{
			http.MethodPut, pathBinding, authz.PermissionTenantRoleManage,
			bindingPutOperation.Handle(p.handleBindingPut),
		},
		{
			http.MethodDelete, pathBinding, authz.PermissionTenantRoleManage,
			bindingDeleteOperation.Handle(p.handleBindingDelete),
		},
	}
	for i := range routes {
		routes[i].handler = p.guardRoute(routes[i].method, routes[i].perm, routes[i].handler)
	}
	return routes
}

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
