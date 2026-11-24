// Package pathguard provides the route guards that reconcile the tenant a
// request path names with the tenant the request resolved to: New for the shared
// rule, and NewTenantAdmin, which also refuses the platform domain. A refusal is
// a bare 404, and a route with no "tenant" path parameter passes through.
//
// It lives here rather than in pkg/http because it reads tenant.Catalog, and
// pkg/tenant imports pkg/http.
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

// New returns the shared guard. It answers a bare 404 unless the path's tenant
// segment names, by identifier or current name, the tenant the request resolved
// to, and rewrites the segment to that tenant's identifier.
func New(roster tenant.Catalog) speedhttp.RouteGuard {
	return &guard{roster: roster}
}

// NewTenantAdmin returns the guard the tenant management surfaces apply: the
// shared rule plus a bare 404 for the platform domain.
func NewTenantAdmin(roster tenant.Catalog) speedhttp.TenantAdminRouteGuard {
	return &tenantAdminGuard{guard: &guard{roster: roster}}
}

// Guard wraps next with the shared check.
func (g *guard) Guard(next gin.HandlerFunc) gin.HandlerFunc { return g.wrap(next, false) }

// GuardTenantAdmin wraps next with the shared check and the platform domain
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

// refuse answers a bare 404 and logs why, so a caller cannot tell a segment that
// names nothing from one that names another tenant.
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
