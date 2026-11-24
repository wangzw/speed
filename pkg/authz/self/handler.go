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
// handler, answering 401 when the request carries no identity.
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
// in the tenant the request resolved to. An absent tenant leaves the tenant list
// empty rather than falling back to the platform domain.
func (p *product) handlePermissions(ctx context.Context, _ struct{}) (permissionsBody, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		// The mounted route is behind requireIdentity; a direct call without an
		// identity is refused here rather than answered as an empty set, which
		// would say the caller holds nothing.
		return permissionsBody{}, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	platform, err := p.permissions.Held(ctx, authz.PlatformDomain, string(id.Account()))
	if err != nil {
		return permissionsBody{}, p.unavailable(err)
	}
	names := permissionsBody{
		Platform: permissionNames(platform),
		Tenant:   []string{},
	}
	if tenantID, ok := tenant.FromContext(ctx); ok {
		held, err := p.permissions.Held(ctx, authz.Domain(tenantID), string(id.Account()))
		if err != nil {
			return permissionsBody{}, p.unavailable(err)
		}
		names.Tenant = permissionNames(held)
	}
	return names, nil
}

func permissionNames(held []authz.Permission) []string {
	names := make([]string, 0, len(held))
	for _, perm := range held {
		names = append(names, string(perm))
	}
	return names
}

// unavailable records a permission read that could not answer and returns the
// 503 the caller answers with. The cause goes to the log alone.
func (p *product) unavailable(err error) error {
	p.logger.Error(ModuleName+": the caller's permissions could not be read", "error", err)
	return speedhttp.Fault{Status: http.StatusServiceUnavailable}
}

// writeNoIdentity401 writes the 401 of the permission read reached with no
// identity, the same answer the admission and authorization layers write.
func writeNoIdentity401(c *gin.Context, realm string) {
	realm = quoteRealm(realm)
	c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", Cookie realm="`+realm+`"`)
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
}

// quoteRealm escapes what goes between the quotes of a realm: a quote or a
// backslash gets a backslash, and a line ending is dropped.
//
// The rule is copied byte for byte into every surface that writes a no-identity
// 401; change every copy by hand.
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
