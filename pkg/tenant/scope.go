package tenant

import (
	"context"

	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

// FromContext returns the tenant identifier this context carries; the second result is false when
// it carries none. There is no injection point beside the roster's entries and the resolving layer,
// and it does not fall back to a default.
func FromContext(ctx context.Context) (ID, bool) {
	id, ok := tenantctx.ID(ctx)
	if !ok {
		return "", false
	}
	return ID(id), true
}

// CrossTenant returns a context in which scoped statements are neither filtered by a tenant nor
// made to fill the tenant column; it is the only way across tenants. What crossing lifts is the
// condition and the fill, not the column: a row written under the mark still has to carry a tenant.
func CrossTenant(ctx context.Context) context.Context {
	return tenantctx.WithCrossTenant(ctx)
}
