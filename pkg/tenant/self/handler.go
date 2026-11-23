package self

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

// requireIdentity puts the route's identity check in front of the declared
// handler.
//
// A route on a guarded endpoint is already authenticated by the gateway, and
// this wrapper changes nothing there. It is the fallback for the assembly in
// which the endpoint is not guarded, or in which the handler is reached another
// way: the tenant read belongs to a caller, so a request that carries no
// identity is answered 401 before anything is read. The check sits in front of
// the typed handler rather than inside it so that the challenge is written by
// the layer that knows the endpoint, and so that the handler's signature carries
// no gin context.
func (p *product) requireIdentity(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := authn.FromContext(c.Request.Context()); !ok {
			writeNoIdentity401(c, realmOf(c))
			return
		}
		next(c)
	}
}

// handleTenants answers the tenants the caller holds an active membership in.
//
// The memberships are the account module's read across tenants, which is
// ordered by identifier; this filter preserves that order and never sorts
// again. The view exposes no identifier, so the order is a stable enumeration
// rather than one a client can name. A membership enters the answer when it is
// itself active and its roster row is active. A read that cannot answer —
// either the membership table or the roster snapshot — is a 503 with its cause
// in the log, never an empty list.
//
// An impersonating caller is confined to the grant's tenant, so only that
// membership enters its answer. The route is where a host gets tenant names after
// a full page load, when the roster cache it read before is gone; the grant
// already names the tenant, and the answer gives it the name without handing
// over the impersonated account's memberships in other tenants. The gateway's
// exit check guarantees an impersonating principal carries a scope; a direct
// call without one answers nothing rather than everything.
func (p *product) handleTenants(ctx context.Context, _ struct{}) (tenantsBody, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		// The mounted route is behind requireIdentity; a direct call without an
		// identity is refused here rather than answered as an empty list.
		return tenantsBody{}, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	scope, confined := id.Scope()
	impersonating := id.Impersonating()
	memberships, err := p.memberships.Tenants(ctx, id.Account())
	if err != nil {
		return tenantsBody{}, p.unavailable(err, "the caller's tenants could not be read")
	}
	// out is non-nil whatever the memberships hold, so an empty answer
	// serialises as [] and never null.
	out := make([]tenantSelfTenant, 0, len(memberships))
	for _, membership := range memberships {
		if impersonating && (!confined || membership.Tenant != scope) {
			continue
		}
		if !membership.Active {
			continue
		}
		found, ok, err := p.catalog.Get(ctx, tenant.ID(membership.Tenant))
		if err != nil {
			return tenantsBody{}, p.unavailable(err, "the caller's tenants could not be read")
		}
		if !ok || found.Status != tenant.StatusActive {
			continue
		}
		kind := tenant.KindBusiness
		if p.kind != nil {
			kind, err = p.kind.KindOf(ctx, found.ID)
			if err != nil {
				return tenantsBody{}, p.unavailable(err, "a tenant's kind could not be read")
			}
		}
		out = append(out, tenantSelfTenant{
			Name:        found.Name,
			DisplayName: found.Profile.DisplayName,
			Kind:        string(kind),
		})
	}
	return tenantsBody{Tenants: out}, nil
}

// unavailable records a read that could not answer and returns the 503 the
// caller answers with. The cause goes to the log and never to the response: the
// answer says a dependency could not answer, not what it holds.
func (p *product) unavailable(err error, what string) error {
	p.logger.Error(ModuleName+": "+what, "error", err)
	return speedhttp.Fault{Status: http.StatusServiceUnavailable}
}

// writeNoIdentity401 writes the 401 of the tenant read reached with no
// identity. It is the same answer the admission and authorization layers write
// for the same condition: a 401 with no problem type and the realm challenge
// naming both carriers.
func writeNoIdentity401(c *gin.Context, realm string) {
	realm = quoteRealm(realm)
	c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", Cookie realm="`+realm+`"`)
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
}

// quoteRealm escapes what goes between the quotes of a realm: a quote or a
// backslash gets a backslash, and a line ending is dropped because it would
// start another header.
//
// The same rule is written in the authorization layer, the management surface,
// the admission layer and the mount layer as byte-identical copies, because no
// package boundary between them carries a symbol the others may share: an
// unexported one is out of reach and an internal one is out of the tree. The
// no-identity 401 of these faces must be the same shape, so a change to the
// rule has to be made in all of them by hand.
func quoteRealm(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		switch ch := name[i]; ch {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(ch)
		case '\r', '\n':
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}
