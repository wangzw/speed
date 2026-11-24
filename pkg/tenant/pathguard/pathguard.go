// Package pathguard provides the route guard that holds a tenant path segment to
// the tenant the request resolved to.
package pathguard

import (
	nethttp "net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

type guard struct {
	roster tenant.Catalog
}

type tenantAdminGuard struct {
	*guard
}

var (
	_ speedhttp.RouteGuard            = (*guard)(nil)
	_ speedhttp.RouteGuard            = (*tenantAdminGuard)(nil)
	_ speedhttp.TenantAdminRouteGuard = (*tenantAdminGuard)(nil)
)

// New returns the guard that applies the identity rule: the path segment must
// name the resolved tenant, and the path parameter is rewritten to its identifier.
func New(roster tenant.Catalog) speedhttp.RouteGuard {
	return &guard{roster: roster}
}

// NewTenantAdmin returns the identity-rule guard that also refuses the platform
// domain.
func NewTenantAdmin(roster tenant.Catalog) speedhttp.TenantAdminRouteGuard {
	return &tenantAdminGuard{guard: &guard{roster: roster}}
}

// Guard wraps next with the identity rule; a mismatch is answered with a bare 404.
func (g *guard) Guard(next gin.HandlerFunc) gin.HandlerFunc { return g.wrap(next, false) }

// GuardTenantAdmin wraps next with the identity rule and the platform-domain
// refusal.
func (g *tenantAdminGuard) GuardTenantAdmin(next gin.HandlerFunc) gin.HandlerFunc {
	return g.wrap(next, true)
}

func (g *guard) wrap(next gin.HandlerFunc, refusePlatformDomain bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		segment := c.Param("tenant")
		if segment == "" {
			next(c)
			return
		}
		ctx := c.Request.Context()
		resolved, ok := tenant.FromContext(ctx)
		if !ok {
			g.refuse(c, "the request carries no resolved tenant", segment)
			return
		}
		named, found, err := g.roster.ByName(ctx, segment)
		if err != nil {
			log.FromContext(ctx).Error("tenant: the path tenant could not be resolved",
				"path", c.Request.URL.Path, "segment", segment, "error", err)
			speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusInternalServerError})
			return
		}
		if !found {
			g.refuse(c, "the segment names no tenant", segment)
			return
		}
		if named.ID != resolved {
			g.refuse(c, "the segment names another tenant", segment)
			return
		}
		if refusePlatformDomain && (named.ID == platformDomain || resolved == platformDomain) {
			g.refuse(c, "the tenant is the platform domain", segment)
			return
		}
		replaceParam(c, "tenant", string(named.ID))
		next(c)
	}
}

func (g *guard) refuse(c *gin.Context, why, segment string) {
	log.FromContext(c.Request.Context()).Info("tenant: the path tenant was refused",
		"path", c.Request.URL.Path, "segment", segment, "reason", why)
	speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusNotFound})
}

func replaceParam(c *gin.Context, name, value string) {
	for i := range c.Params {
		if c.Params[i].Key == name {
			c.Params[i].Value = value
			return
		}
	}
}

var platformDomain = tenant.ID(authz.PlatformDomain)
