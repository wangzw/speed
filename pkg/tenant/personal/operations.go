package personal

import (
	"context"

	"github.com/vislake/speed/pkg/tenant"
)

var _ tenant.KindResolver = (*product)(nil)

// KindOf implements tenant.KindResolver: a tenant whose identifier is a
// personal tenant identifier is personal, and every other tenant is business.
//
// The kind is a property of the row, not of the caller. The platform roster
// lists every account's spaces, so a caller reading another account's personal
// tenant must see it as personal too; the identifier's reserved prefix is what
// makes the answer independent of who asks.
func (p *product) KindOf(_ context.Context, id tenant.ID) (tenant.Kind, error) {
	if IsPersonalTenantID(string(id)) {
		return tenant.KindPersonal, nil
	}
	return tenant.KindBusiness, nil
}
