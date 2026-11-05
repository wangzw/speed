package personal

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/tenant"
)

var _ tenant.KindResolver = (*product)(nil)

// KindOf implements tenant.KindResolver: the calling account's own personal
// tenant is personal, and every other tenant is business.
//
// The answer compares the row's identifier with the one the caller's account
// derives, exactly. A prefix test would mark another account's personal tenant
// personal too, and that row can appear in the caller's membership list; the
// identity comes off the request context.
func (p *product) KindOf(ctx context.Context, id tenant.ID) (tenant.Kind, error) {
	identity, ok := authn.FromContext(ctx)
	if !ok {
		return tenant.KindBusiness, nil
	}
	if id == TenantID(identity.Account()) {
		return tenant.KindPersonal, nil
	}
	return tenant.KindBusiness, nil
}
