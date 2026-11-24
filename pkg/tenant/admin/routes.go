package admin

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

var (
	pathTenants       = tenantAdminSpec.ResolvePath(pathTenantsFragment)
	pathTenant        = tenantAdminSpec.ResolvePath(pathTenantFragment)
	pathTenantProfile = tenantAdminSpec.ResolvePath(pathTenantProfileFragment)
	pathTenantStatus  = tenantAdminSpec.ResolvePath(pathTenantStatusFragment)

	pathTenantOwnerRequests = tenantAdminSpec.ResolvePath(pathTenantOwnerRequestsFragment)
	pathTenantPending       = tenantAdminSpec.ResolvePath(pathTenantPendingFragment)
	pathTenantRequest       = tenantAdminSpec.ResolvePath(pathTenantRequestFragment)

	pathOwnerRequests = tenantOwnerSpec.ResolvePath(pathOwnerRequestsFragment)
	pathOwnerPending  = tenantOwnerSpec.ResolvePath(pathOwnerPendingFragment)
	pathOwnerRequest  = tenantOwnerSpec.ResolvePath(pathOwnerRequestFragment)
	pathOrganization  = tenantOwnerSpec.ResolvePath(pathOrganizationFragment)

	pathMyRequests = selfSpec.ResolvePath(pathMyRequestsFragment)
	pathMyRequest  = selfSpec.ResolvePath(pathMyRequestFragment)
	pathMyConfirm  = selfSpec.ResolvePath(pathMyConfirmFragment)
	pathMyDecline  = selfSpec.ResolvePath(pathMyDeclineFragment)
)

var managementGroups = []string{"tenants"}

const realmKey = "tenant.admin.realm"

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
			http.MethodGet, pathTenants, PermissionTenantRead,
			listOperation.Handle(p.handleTenantList),
		},
		{
			http.MethodPost, pathTenants, PermissionTenantManage,
			prescreenJSONFields[createBody](p, nil, registrationForbiddenFields,
				createOperation.Handle(p.handleTenantCreate)),
		},
		{
			http.MethodGet, pathTenant, PermissionTenantRead,
			getOperation.Handle(p.handleTenantGet),
		},
		{
			http.MethodPost, pathTenantOwnerRequests, PermissionTenantManage,
			prescreenJSON[requestTransferInput](p, requestTransferOperation.Handle(p.handleTenantRequestTransfer)),
		},
		{
			http.MethodGet, pathTenantPending, PermissionTenantRead,
			pendingTransferOperation.Handle(p.handleTenantPendingTransfer),
		},
		{
			http.MethodDelete, pathTenantRequest, PermissionTenantManage,
			cancelTransferOperation.Handle(p.handleTenantCancelTransfer),
		},
		{
			http.MethodDelete, pathTenant, PermissionTenantManage,
			deleteOperation.Handle(p.handleTenantDelete),
		},
		{
			http.MethodPatch, pathTenantProfile, PermissionTenantManage,
			prescreenJSONFields[setProfileInput](p, profileWriteFields, nil,
				setProfileOperation.Handle(p.handleTenantSetProfile)),
		},
		{
			http.MethodPost, pathTenantStatus, PermissionTenantManage,
			prescreenJSON[setStatusInput](p, setStatusOperation.Handle(p.handleTenantSetStatus)),
		},
	}
	for i := range routes {
		routes[i].handler = p.guardRoute(routes[i].method, routes[i].perm, routes[i].handler)
	}
	return routes
}

func (p *product) tenantRoutes() []route {
	routes := []route{
		{
			http.MethodPost, pathOwnerRequests, "",
			p.guardOwnerRoute(http.MethodPost,
				prescreenJSON[ownerRequestTransferInput](p,
					tenantRequestTransferOperation.Handle(p.handleOwnerRequestTransfer))),
		},
		{
			http.MethodGet, pathOwnerPending, "",
			p.guardOwnerRoute(http.MethodGet,
				tenantPendingTransferOperation.Handle(p.handleOwnerPendingTransfer)),
		},
		{
			http.MethodDelete, pathOwnerRequest, "",
			p.guardOwnerRoute(http.MethodDelete,
				tenantCancelTransferOperation.Handle(p.handleOwnerCancelTransfer)),
		},
		{
			http.MethodDelete, pathOrganization, "",
			p.guardOwnerRoute(http.MethodDelete,
				tenantDeleteOperation.Handle(p.handleTenantOwnerDelete)),
		},
	}
	for i := range routes {
		routes[i].handler = p.routeGuard.GuardTenantAdmin(routes[i].handler)
	}
	return routes
}

func (p *product) selfRoutes() []route {
	return []route{
		{
			http.MethodGet, pathMyRequests, "",
			p.guardSelfRoute(false, myTransferRequestsOperation.Handle(p.handleMyTransferRequests)),
		},
		{
			http.MethodGet, pathMyRequest, "",
			p.guardSelfRoute(false, myTransferRequestOperation.Handle(p.handleMyTransferRequest)),
		},
		{
			http.MethodPost, pathMyConfirm, "",
			p.guardSelfRoute(true, myTransferConfirmOperation.Handle(p.handleMyTransferConfirm)),
		},
		{
			http.MethodPost, pathMyDecline, "",
			p.guardSelfRoute(false, myTransferDeclineOperation.Handle(p.handleMyTransferDecline)),
		},
	}
}

func (p *product) mount(router speedhttp.Router) error {
	type target struct {
		name     string
		endpoint speedhttp.Endpoint
	}
	resolve := func(names []string) ([]target, error) {
		targets := make([]target, 0, len(names))
		for _, name := range names {
			endpoint, err := router.Endpoint(name)
			if err != nil {
				return nil, fmt.Errorf("%s: %w: %q: %w", ModuleName, authz.ErrUnknownEndpoint, name, err)
			}
			targets = append(targets, target{name: name, endpoint: endpoint})
		}
		return targets, nil
	}
	platformTargets, err := resolve(p.cfg.endpoints)
	if err != nil {
		return err
	}
	tenantTargets, err := resolve(p.cfg.tenantEndpoints)
	if err != nil {
		return err
	}
	selfTargets, err := resolve(p.cfg.selfEndpoints)
	if err != nil {
		return err
	}
	for _, tgt := range platformTargets {
		// On the endpoint, so it precedes every module's handler under the prefix.
		tgt.endpoint.Use(p.tenantNameLayer())
		for _, r := range p.routes() {
			tgt.endpoint.Route(r.method, r.path, withRealm(tgt.name, r.handler))
		}
	}
	for _, tgt := range tenantTargets {
		for _, r := range p.tenantRoutes() {
			tgt.endpoint.Route(r.method, r.path, withRealm(tgt.name, r.handler))
		}
	}
	for _, tgt := range selfTargets {
		for _, r := range p.selfRoutes() {
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
	for _, r := range p.tenantRoutes() {
		p.gate.Open(r.method, r.path)
	}
	for _, r := range p.selfRoutes() {
		p.gate.Open(r.method, r.path)
	}
}
