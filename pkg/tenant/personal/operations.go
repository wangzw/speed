package personal

import (
	"context"

	"github.com/vislake/speed/pkg/tenant"
)

var _ tenant.KindResolver = (*product)(nil)

// KindOf implements tenant.KindResolver: a tenant whose identifier carries the
// personal prefix is personal, every other tenant is business.
func (p *product) KindOf(_ context.Context, id tenant.ID) (tenant.Kind, error) {
	if IsPersonalTenantID(string(id)) {
		return tenant.KindPersonal, nil
	}
	return tenant.KindBusiness, nil
}
