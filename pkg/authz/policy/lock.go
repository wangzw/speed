package policy

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
)

func (p *product) Lock(ctx context.Context, tenant string) (func(), error) {
	return p.tenantLock.Lock(ctx, tenant)
}

func (p *product) MembershipLock() any {
	return p.tenantLock
}

var _ authn.SharedMembershipLock = (*product)(nil)
