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

const systemTenantPrefix = "/api/v1/system/tenants/"

// Disabled tenants and tombstones resolve, because the platform surface manages both.
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

func replaceParam(c *gin.Context, name, value string) {
	for i := range c.Params {
		if c.Params[i].Key == name {
			c.Params[i].Value = value
			return
		}
	}
}

func (p *product) nameOf(ctx context.Context, id tenant.ID) (string, error) {
	if p.names == nil {
		return "", nil
	}
	return p.names.NameOf(ctx, id)
}

func (p *product) kindOf(ctx context.Context, id tenant.ID) (tenant.Kind, error) {
	if p.kind == nil {
		return tenant.KindBusiness, nil
	}
	return p.kind.KindOf(ctx, id)
}
