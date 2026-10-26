package policy

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
)

// Lock implements authn.MembershipGuard by taking the tenant lock that
// Manager.DeleteBinding also takes. The host supplies that lock as
// authz.TenantLock; an assembly with none uses the product's in-process
// default.
//
// The same object on both paths is what makes the floor safe: a membership
// removal's check and a binding delete cannot both read the same count and then
// commit removals that leave the domain with no tenant administrator.
func (p *product) Lock(ctx context.Context, tenant string) (func(), error) {
	return p.tenantLock.Lock(ctx, tenant)
}

// MembershipLock implements authn.SharedMembershipLock: it names the lock object
// this guard takes, so the account module takes a lock several guards share once
// for one removal instead of re-entering it.
func (p *product) MembershipLock() any {
	return p.tenantLock
}

var _ authn.SharedMembershipLock = (*product)(nil)
