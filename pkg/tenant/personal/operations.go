package personal

import (
	"context"

	"github.com/vislake/speed/pkg/tenant"
)

var _ tenant.KindResolver = (*product)(nil)

// KindOf implements tenant.KindResolver. The kind is a property of the row, not
// of the caller: the reserved identifier prefix makes the answer independent of
// who asks.
func (p *product) KindOf(_ context.Context, id tenant.ID) (tenant.Kind, error) {
	if IsPersonalTenantID(string(id)) {
		return tenant.KindPersonal, nil
	}
	return tenant.KindBusiness, nil
}
