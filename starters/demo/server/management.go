package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
)

const managementModuleName = "management.access"

// tenantManagementRoutes is the authentication capability's tenant-management
// route set, each as the entry point registers it: the HTTP method and the path
// in gin's :param form.
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

func init() { core.ProcessRegistry.Register(managementModule()) }

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
