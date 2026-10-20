package self

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

// requireIdentity puts the route's identity check in front of the declared
// handler.
//
// A route on a guarded endpoint is already authenticated by the gateway, and
// this wrapper changes nothing there. It is the fallback for the assembly in
// which the endpoint is not guarded, or in which the handler is reached another
// way: the permission read belongs to a principal, so a request that carries
// none is answered 401 before anything is read. The check sits in front of the
// typed handler rather than inside it so that the challenge is written by the
// layer that knows the endpoint, and so that the handler's signature carries no
// gin context.
func (p *product) requireIdentity(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := authn.FromContext(c.Request.Context()); !ok {
			writeNoIdentity401(c, realmOf(c))
			return
		}
		next(c)
	}
}

// handlePermissions answers the caller's permissions in the platform domain and
// in the tenant the request resolved to.
//
// The tenant is read through the same seam the enforcement middleware uses, and
// an absent tenant leaves the tenant list empty: falling back to the platform
// domain would answer the platform list twice under two names. A read the
// policy cannot answer is a 503 with its cause in the log, never an empty set.
func (p *product) handlePermissions(ctx context.Context, _ struct{}) (permissionsBody, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		// The mounted route is behind requireIdentity; a direct call without an
		// identity is refused here rather than answered as an empty set, which
		// would say the caller holds nothing.
		return permissionsBody{}, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	platform, err := p.permissions.Held(ctx, authz.PlatformDomain, id.Key())
	if err != nil {
		return permissionsBody{}, p.unavailable(err)
	}
	names := permissionsBody{
		Platform: permissionNames(platform),
		Tenant:   []string{},
	}
	if tenantID, ok := tenant.FromContext(ctx); ok {
		held, err := p.permissions.Held(ctx, authz.Domain(tenantID), id.Key())
		if err != nil {
			return permissionsBody{}, p.unavailable(err)
		}
		names.Tenant = permissionNames(held)
	}
	return names, nil
}

// permissionNames renders held permissions as the names the answer carries. An
// empty result is an empty slice, so the answer writes an empty list and never
// a null.
func permissionNames(held []authz.Permission) []string {
	names := make([]string, 0, len(held))
	for _, perm := range held {
		names = append(names, string(perm))
	}
	return names
}

// unavailable records a permission read that could not answer and returns the
// 503 the caller answers with. The cause goes to the log and never to the
// response: the answer says a dependency could not answer, not what it holds.
func (p *product) unavailable(err error) error {
	p.logger.Error(ModuleName+": the caller's permissions could not be read", "error", err)
	return speedhttp.Fault{Status: http.StatusServiceUnavailable}
}

// writeNoIdentity401 writes the 401 of the permission read reached with no
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
// design promises the no-identity 401 of these faces are the same shape, so a
// change to the rule has to be made in all of them by hand.
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
