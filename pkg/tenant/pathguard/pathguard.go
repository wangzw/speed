// Package pathguard carries the layer that reconciles the tenant a request
// path names with the tenant the request itself resolved to.
//
// A tenant-scoped route is addressed under a tenant segment, /api/v1/tenants/
// {tenant}/...; the request surface names a tenant by its alias, while the
// resolving layer replaces that name with the tenant's identifier before
// anything downstream reads it. The two are the same tenant only when the
// segment and the resolved identifier agree, and the segment may carry either
// the identifier or the tenant's current alias, because a roster alias may be
// renamed while the identifier stays.
//
// The layer therefore resolves the segment through the roster and compares
// identifiers, never strings. A segment that resolves to no tenant, to more
// than one, or to a tenant other than the one the request resolved to is
// answered with the same bare 404 the resolving layer writes for a tenant it
// does not serve, so the answer tells a caller nothing about whether the named
// tenant exists.
//
// The identity rule covers every ScopeTenants group. The tenant management
// surfaces add one more check: the platform domain is not a tenant they
// administer, so a path or resolved tenant that is the platform domain is
// refused the same bare 404. That check is not the shared rule — a tenant-
// scoped surface that manages the system tenant itself, the members, invitations
// and SSO groups, is addressed by the platform domain legitimately — so it is a
// second constructor, NewTenantAdmin, delivered under its own
// speedhttp.TenantAdminRouteGuard capability and applied by the two tenant
// management modules alone.
//
// It reads the segment from the gin path parameter "tenant", which every
// ScopeTenants route declares. A route with no such parameter is passed through
// untouched, which is a defensive case: the guard is mounted on the routes of a
// tenant-scoped group, and every one of them carries the segment.
//
// The guard is a route-level seam, not an endpoint layer: the mounting module
// applies it to every route of a ScopeTenants group, and the tenant capability
// delivers it under speedhttp.RouteGuard. A platform route that names a tenant
// in its path — /api/v1/system/tenants/:tenant/roles is one — treats that tenant
// as the data the route acts on, not as the scope it is decided in, and is not
// guarded even when it hangs off the same endpoint.
//
// It lives in this capability's directory rather than in pkg/http because it
// reads the roster through tenant.Catalog: pkg/tenant imports pkg/http (the
// resolving module registers its layer through it), and the reverse import
// would be a cycle. It is not applied by pkg/authn/mount's package either,
// because that module's contract keeps the tenant directory out of the
// authentication closure; the mounter receives the guard as the pkg/http
// capability and applies it without importing this package.
package pathguard

import (
	"context"
	nethttp "net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// guard reconciles a request's tenant path segment with its resolved tenant.
// It implements speedhttp.RouteGuard alone: the shared rule is the identity
// check, so a value built by New does not claim the tenant administration
// capability.
type guard struct {
	roster tenant.Catalog
}

// tenantAdminGuard is the guard the tenant management surfaces apply: the
// identity rule plus the refusal of the platform domain. It embeds the shared
// guard, so the identity check has one implementation, and adds only the
// capability of its own.
type tenantAdminGuard struct {
	*guard
}

var (
	_ speedhttp.RouteGuard            = (*guard)(nil)
	_ speedhttp.RouteGuard            = (*tenantAdminGuard)(nil)
	_ speedhttp.TenantAdminRouteGuard = (*tenantAdminGuard)(nil)
)

// New builds the shared guard over the roster. It holds every tenant-scoped
// route to the identity rule and nothing else.
func New(roster tenant.Catalog) speedhttp.RouteGuard {
	return &guard{roster: roster}
}

// NewTenantAdmin builds the guard the tenant management surfaces apply: the
// identity rule plus the refusal of the platform domain.
func NewTenantAdmin(roster tenant.Catalog) speedhttp.TenantAdminRouteGuard {
	return &tenantAdminGuard{guard: &guard{roster: roster}}
}

// Guard wraps next with the shared check. The wrapped handler hands the request
// on when the path names the tenant the request resolved to, or carries no
// tenant segment at all, and answers the bare 404 when it does not.
func (g *guard) Guard(next gin.HandlerFunc) gin.HandlerFunc { return g.wrap(next, false) }

// GuardTenantAdmin wraps next with the identity rule and the platform domain
// refusal.
func (g *tenantAdminGuard) GuardTenantAdmin(next gin.HandlerFunc) gin.HandlerFunc {
	return g.wrap(next, true)
}

// wrap is the check both capabilities share. The platform domain refusal is
// gated on refusePlatformDomain, so the shared guard applies the identity rule
// alone.
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
		named, unique, err := g.resolveSegment(ctx, segment)
		if err != nil {
			log.FromContext(ctx).Error("tenant: the path tenant could not be resolved",
				"path", c.Request.URL.Path, "segment", segment, "error", err)
			speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusInternalServerError})
			return
		}
		if !unique {
			g.refuse(c, "the segment names no unique tenant", segment)
			return
		}
		if named != resolved {
			g.refuse(c, "the segment names another tenant", segment)
			return
		}
		if refusePlatformDomain && (named == platformDomain || resolved == platformDomain) {
			g.refuse(c, "the tenant is the platform domain", segment)
			return
		}
		next(c)
	}
}

// refuse answers the one status this layer refuses with and records why.
//
// The response carries the status and nothing else. It says the request
// addresses no tenant it may reach; the reason goes to the request's logger and
// not to the caller, so the two refusals a caller could otherwise tell apart —
// a segment that names nothing and one that names another tenant — share an
// answer.
func (g *guard) refuse(c *gin.Context, why, segment string) {
	log.FromContext(c.Request.Context()).Info("tenant: the path tenant was refused",
		"path", c.Request.URL.Path, "segment", segment, "reason", why)
	speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusNotFound})
}

// resolveSegment turns a path segment into the identifier of the one tenant it
// names. ByAlias and Get are two lookups over one roster: an alias may equal
// another tenant's identifier, so a segment that both resolve to different
// identifiers is a segment that names no unique tenant, and unique reports false
// for it.
//
// A segment outside the identifier's domain is still tried as an alias, because
// an alias's domain is the roster's to judge rather than this layer's, and it is
// not tried as an identifier, because no row carries it and the lookup would only
// restate that.
func (g *guard) resolveSegment(ctx context.Context, segment string) (id tenant.ID, unique bool, err error) {
	byAlias, foundAlias, err := g.roster.ByAlias(ctx, segment)
	if err != nil {
		return "", false, err
	}
	byID, foundID, err := g.lookupID(ctx, segment)
	if err != nil {
		return "", false, err
	}
	switch {
	case foundAlias && foundID && byAlias.ID != byID.ID:
		return "", false, nil
	case foundAlias:
		return byAlias.ID, true, nil
	case foundID:
		return byID.ID, true, nil
	default:
		return "", false, nil
	}
}

// lookupID resolves a segment as a tenant identifier. A segment outside the
// identifier's domain resolves nothing rather than reaching the roster with a
// value no row could carry.
func (g *guard) lookupID(ctx context.Context, segment string) (tenant.Tenant, bool, error) {
	if !tenant.IsIdentifier(segment) {
		return tenant.Tenant{}, false, nil
	}
	return g.roster.Get(ctx, tenant.ID(segment))
}

// platformDomain is the tenant identifier the platform domain is carried by.
var platformDomain = tenant.ID(authz.PlatformDomain)
