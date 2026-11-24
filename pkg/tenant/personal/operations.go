package personal

import (
	"context"

	"github.com/vislake/speed/pkg/tenant"
)

var _ tenant.KindResolver = (*product)(nil)

// KindOf implements tenant.KindResolver: personal for a personal tenant identifier,
// business otherwise.
func (p *product) KindOf(_ context.Context, id tenant.ID) (tenant.Kind, error) {
	if IsPersonalTenantID(string(id)) {
		return tenant.KindPersonal, nil
	}
	return tenant.KindBusiness, nil
}
