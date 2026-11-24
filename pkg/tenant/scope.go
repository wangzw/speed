package tenant

import (
	"context"

	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

// FromContext returns the tenant identifier this context carries. The second
// result is false when it carries none.
func FromContext(ctx context.Context) (ID, bool) {
	id, ok := tenantctx.ID(ctx)
	if !ok {
		return "", false
	}
	return ID(id), true
}

// CrossTenant returns a context in which scoped statements are neither filtered
// by a tenant nor made to fill the tenant column. The returned context belongs
// to no tenant: FromContext reports none and the logger's tenant field is
// removed. A write under it must still carry a valid tenant column value.
func CrossTenant(ctx context.Context) context.Context {
	return tenantctx.WithCrossTenant(ctx)
}
