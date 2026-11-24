package policy

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
)

// Lock implements authn.MembershipGuard by taking the tenant lock that
// Manager.DeleteBinding also takes. The same object on both paths keeps a
// membership removal and a binding delete from both reading the same count and
// then leaving the domain with no tenant administrator.
func (p *product) Lock(ctx context.Context, tenant string) (func(), error) {
	return p.tenantLock.Lock(ctx, tenant)
}

// MembershipLock implements authn.SharedMembershipLock: it names the lock object
// this guard takes, so the account module takes a shared lock once per removal.
func (p *product) MembershipLock() any {
	return p.tenantLock
}

var _ authn.SharedMembershipLock = (*product)(nil)
