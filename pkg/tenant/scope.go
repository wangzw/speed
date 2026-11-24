package tenant

import (
	"context"

	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

// FromContext returns the tenant identifier this context carries; the second
// result is false when it carries none, and nothing is defaulted. There is no
// exported way to write one: the writing side is internal.
func FromContext(ctx context.Context) (ID, bool) {
	id, ok := tenantctx.ID(ctx)
	if !ok {
		return "", false
	}
	return ID(id), true
}

// CrossTenant returns a context in which scoped statements are neither filtered
// by a tenant nor made to fill the tenant column. The result belongs to no
// tenant: FromContext reports none and the logger's tenant field is removed. A
// write under it that leaves the tenant column empty or null, puts a value outside
// the identifier's domain there, or assigns through a raw target not readable as
// one column is still refused.
func CrossTenant(ctx context.Context) context.Context {
	return tenantctx.WithCrossTenant(ctx)
}
