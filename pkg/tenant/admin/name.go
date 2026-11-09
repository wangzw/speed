package admin

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// systemTenantPrefix is the path every platform route that names a tenant as
// its data hangs under. The name layer acts on requests below it and on
// nothing else: the tenant-domain surface under /api/v1/tenants/ resolves its
// own tenant through the path guard.
const systemTenantPrefix = "/api/v1/system/tenants/"

// tenantNameLayer resolves the tenant a platform path names and replaces the
// path parameter with its internal identifier.
//
// The platform management surface is addressed by the name a client reads,
// while every handler behind it keys its rows by the identifier. This
// layer is the boundary where the two meet, and it covers every module's routes
// under the prefix, not only this module's: the authorization and authentication
// management routes name the same tenant as data.
//
// The lookup is one roster read and no status judgement: a disabled tenant and
// a tombstone resolve, because the platform surface manages both. A name no
// tenant carries is answered with the same bare 404 the tenant resolution layer
// writes, so a caller cannot tell an unknown name from a switched-off tenant.
//
// The layer runs on the endpoint rather than on this module's routes because the
// other management modules mount their own routes on the same endpoint; it is
// registered by this module because this module owns the roster-facing prefix.
func (p *product) tenantNameLayer() speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:  ModuleName + ".tenant-name",
		Stage: speedhttp.StageIdentify,
		Handler: func(c *gin.Context) {
			if !strings.HasPrefix(c.Request.URL.Path, systemTenantPrefix) {
				c.Next()
				return
			}
			segment := c.Param("tenant")
			if segment == "" {
				c.Next()
				return
			}
			found, ok, err := p.catalog.ByName(c.Request.Context(), segment)
			if err != nil {
				log.FromContext(c.Request.Context()).Error(ModuleName+
					": the path tenant could not be resolved; the request was not carried out",
					"path", c.Request.URL.Path, "segment", segment, slog.String("cause", err.Error()))
				speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusInternalServerError})
				return
			}
			if !ok {
				speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusNotFound})
				return
			}
			replaceParam(c, "tenant", string(found.ID))
			c.Next()
		},
	}
}

// replaceParam writes value into the path parameter name, when the route
// carries one. The entry point binds a handler's declared path input from the
// router's parameters, so replacing the value here is what makes the handler
// read the internal identifier.
func replaceParam(c *gin.Context, name, value string) {
	for i := range c.Params {
		if c.Params[i].Key == name {
			c.Params[i].Value = value
			return
		}
	}
}

// nameOf answers the name a tenant identifier is addressed by. With no
// resolver assembled it answers the empty string: the identifier is internal
// and must not stand in for the name.
func (p *product) nameOf(ctx context.Context, id tenant.ID) (string, error) {
	if p.names == nil {
		return "", nil
	}
	return p.names.NameOf(ctx, id)
}

// kindOf answers one tenant's kind. With no resolver assembled every tenant is
// a business tenant, the same rule the caller's tenant read applies.
func (p *product) kindOf(ctx context.Context, id tenant.ID) (tenant.Kind, error) {
	if p.kind == nil {
		return tenant.KindBusiness, nil
	}
	return p.kind.KindOf(ctx, id)
}
