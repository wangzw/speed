package resolve

import (
	nethttp "net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func TestATenantAddressedRouteHoldsThePathTenantToTheResolvedOne(t *testing.T) {
	const sharedRoute = "/tenants/:tenant/probe"
	const adminRoute = "/tenants/:tenant/manage"
	const ordinaryRoute = "/probe"

	host := startHost(t, hostOptions{
		roster: standardRoster(),
		register: func(reg *core.Registry, ep speedhttp.Endpoint) error {
			shared, err := core.Resolve[speedhttp.RouteGuard](reg)
			if err != nil {
				return err
			}
			admin, err := core.Resolve[speedhttp.TenantAdminRouteGuard](reg)
			if err != nil {
				return err
			}
			ok := func(c *gin.Context) { c.Status(nethttp.StatusOK) }
			ep.Route(nethttp.MethodGet, sharedRoute, shared.Guard(ok))
			ep.Route(nethttp.MethodGet, adminRoute, admin.GuardTenantAdmin(ok))
			ep.Route(nethttp.MethodGet, ordinaryRoute, ok)
			return nil
		},
	})

	cases := []struct {
		name     string
		path     string
		resolved string
		status   int
	}{
		{"the path carries the tenant identifier", "/tenants/t_acme/probe", "acme", nethttp.StatusNotFound},
		{"the path carries the current name", "/tenants/acme/probe", "acme", nethttp.StatusOK},
		{"the path names another tenant", "/tenants/paused/probe", "acme", nethttp.StatusNotFound},
		{"the segment names no tenant", "/tenants/ghost/probe", "acme", nethttp.StatusNotFound},
		{"the shared guard admits the system tenant by identifier", "/tenants/system/probe", "platform", nethttp.StatusNotFound},
		{"the shared guard admits the system tenant by name", "/tenants/platform/probe", "platform", nethttp.StatusOK},
		{"the tenant admin guard admits a managed tenant", "/tenants/acme/manage", "acme", nethttp.StatusOK},
		{"the tenant admin guard refuses the system tenant by identifier", "/tenants/system/manage", "platform", nethttp.StatusNotFound},
		{"the tenant admin guard refuses the system tenant by name", "/tenants/platform/manage", "platform", nethttp.StatusNotFound},
		{"the tenant admin guard keeps the identity rule", "/tenants/paused/manage", "acme", nethttp.StatusNotFound},
		{"a route outside the tenant scope passes through", ordinaryRoute, "acme", nethttp.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := host.get(t, tc.path, tc.resolved)
			if got.status != tc.status {
				t.Fatalf("GET %s as %q answered %d, want %d (body %q)",
					tc.path, tc.resolved, got.status, tc.status, got.body)
			}
			if tc.status == nethttp.StatusNotFound {
				requireProblem(t, got, nethttp.StatusNotFound)
			}
		})
	}
}
