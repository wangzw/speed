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

func (p *product) requireIdentity(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := authn.FromContext(c.Request.Context()); !ok {
			writeNoIdentity401(c, realmOf(c))
			return
		}
		next(c)
	}
}

func (p *product) handleTenants(ctx context.Context, _ struct{}) (tenantsBody, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return tenantsBody{}, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	scope, confined := id.Scope()
	impersonating := id.Impersonating()
	memberships, err := p.memberships.Tenants(ctx, id.Account())
	if err != nil {
		return tenantsBody{}, p.unavailable(err, "the caller's tenants could not be read")
	}
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

func (p *product) unavailable(err error, what string) error {
	p.logger.Error(ModuleName+": "+what, "error", err)
	return speedhttp.Fault{Status: http.StatusServiceUnavailable}
}

func writeNoIdentity401(c *gin.Context, realm string) {
	realm = quoteRealm(realm)
	c.Header("WWW-Authenticate", `Bearer realm="`+realm+`", Cookie realm="`+realm+`"`)
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
}

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
