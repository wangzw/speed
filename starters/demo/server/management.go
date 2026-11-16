package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
)

// managementModuleName is the name the host registers this module under.
const managementModuleName = "management.access"

// tenantManagementRoutes is the authentication capability's tenant-management
// route set, each as the entry point registers it: the HTTP method and the path
// in gin's :param form.
//
// The routes are mounted on the api endpoint, whose chain carries the
// authorization guard. Their handlers decide each action themselves through
// authn.Decider in the path tenant's policy domain, so the host declares them
// open to the guard: without the declaration the guard's closed default would
// refuse every one of them 403 before a handler ran.
//
// The impersonation-record routes are not here: they are platform-domain
// decisions with the path's tenant as the data the records are confined to, so
// they mount on the admin endpoint, which the authorization guard does not
// enforce.
//
// The set is written out because the declaration surface matches a route by its
// exact (method, path) pair and has no prefix form. claims_test.go holds it to
// the committed contract's tenant-management operations, so an operation the
// capability adds and this list omits fails the case instead of reaching the
// guard's closed default.
var tenantManagementRoutes = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/api/v1/tenants/:tenant/members"},
	{http.MethodPut, "/api/v1/tenants/:tenant/members/:account"},
	{http.MethodDelete, "/api/v1/tenants/:tenant/members/:account"},
	{http.MethodPut, "/api/v1/tenants/:tenant/members/:account/sso-exempt"},
	{http.MethodGet, "/api/v1/tenants/:tenant/invitations"},
	{http.MethodPost, "/api/v1/tenants/:tenant/invitations"},
	{http.MethodDelete, "/api/v1/tenants/:tenant/invitations/:id"},
	{http.MethodPost, "/api/v1/tenants/:tenant/invitations/:id/resend"},
	{http.MethodGet, "/api/v1/tenants/:tenant/sso/providers"},
	{http.MethodPost, "/api/v1/tenants/:tenant/sso/providers"},
	{http.MethodGet, "/api/v1/tenants/:tenant/sso/providers/:id"},
	{http.MethodPut, "/api/v1/tenants/:tenant/sso/providers/:id"},
	{http.MethodDelete, "/api/v1/tenants/:tenant/sso/providers/:id"},
	{http.MethodPost, "/api/v1/tenants/:tenant/sso/providers/:id/domains"},
	{http.MethodDelete, "/api/v1/tenants/:tenant/sso/providers/:id/domains/:domain"},
	{http.MethodPost, "/api/v1/tenants/:tenant/sso/providers/:id/domains/:domain/verify"},
	{http.MethodPost, "/api/v1/tenants/:tenant/sso/providers/:id/metadata/refresh"},
	{http.MethodPost, "/api/v1/tenants/:tenant/sso/providers/:id/reverify-domains"},
}

// init registers the module. The import is the wiring, as for every module; the
// serve command's Include list is what puts it in a run.
func init() { core.ProcessRegistry.Register(managementModule()) }

// managementModule is the host's own declaration of the tenant-management
// surface's access. It is taken up optionally: a run without the authorization
// layer has no table to write to and nothing to enforce.
func managementModule() core.Module {
	return core.Module{
		Name: managementModuleName,
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			gate, err := core.Resolve[authz.Gate](reg)
			if err != nil {
				if errors.Is(err, core.ErrMissingProvider) {
					return nil
				}
				return err
			}
			for _, r := range tenantManagementRoutes {
				gate.Open(r.method, r.path)
			}
			return nil
		},
	}
}
