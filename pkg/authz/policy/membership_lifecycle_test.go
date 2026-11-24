package policy

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
)

// MembershipRemoved is the synchronous half of the platform-domain cascade:
// a removal of the account's membership of the platform tenant withdraws
// every binding the account holds there, and leaves its bindings elsewhere
// and every other account's bindings alone.
func TestMembershipRemovedWithdrawsThePlatformBindings(t *testing.T) {
	p := newFixture(t).product
	seedPolicy(t, p, string(authz.PlatformDomain), string(authz.PlatformAdminRole), "acct-1", "tenant", "read")
	seedBinding(t, p, string(authz.PlatformDomain), "acct-1", string(authz.TenantAdminRole))
	seedBinding(t, p, "acme", "acct-1", string(authz.TenantAdminRole))
	seedBinding(t, p, string(authz.PlatformDomain), "acct-2", string(authz.PlatformAdminRole))

	if err := p.MembershipRemoved(t.Context(), authn.AccountID("acct-1"), string(authz.PlatformDomain)); err != nil {
		t.Fatalf("MembershipRemoved: %v", err)
	}
	assertBindingCount(t, p, string(authz.PlatformDomain), "acct-1", 0)
	assertBindingCount(t, p, "acme", "acct-1", 1)
	assertBindingCount(t, p, string(authz.PlatformDomain), "acct-2", 1)

	// A retry after a failure finds no row to remove and is no work and no
	// error.
	if err := p.MembershipRemoved(t.Context(), authn.AccountID("acct-1"), string(authz.PlatformDomain)); err != nil {
		t.Fatalf("repeating MembershipRemoved: %v", err)
	}
	assertBindingCount(t, p, string(authz.PlatformDomain), "acct-1", 0)
}

// A membership removed in a business tenant clears that tenant's bindings
// and leaves the account's bindings in every other tenant, the platform
// domain included.
func TestMembershipRemovedInABusinessTenantClearsItsOwnBindings(t *testing.T) {
	p := newFixture(t).product
	seedPolicy(t, p, string(authz.PlatformDomain), string(authz.PlatformAdminRole), "acct-1", "tenant", "read")
	seedPolicy(t, p, "acme", "alpha", "acct-1", "account", "read")

	if err := p.MembershipRemoved(t.Context(), authn.AccountID("acct-1"), "acme"); err != nil {
		t.Fatalf("MembershipRemoved: %v", err)
	}
	assertBindingCount(t, p, string(authz.PlatformDomain), "acct-1", 1)
	assertBindingCount(t, p, "acme", "acct-1", 0)
}

// The guard refuses the removal of an account that is the only tenant
// administrator binding of the tenant: the tenant would be left with nobody who
// may govern it.
func TestCheckMembershipRemovalRefusesTheLastTenantAdmin(t *testing.T) {
	p := newFixture(t).product
	seedBinding(t, p, "acme", "acct-1", string(authz.TenantAdminRole))

	err := p.CheckMembershipRemoval(t.Context(), authn.AccountID("acct-1"), "acme")
	if !errors.Is(err, authn.ErrLastTenantAdmin) {
		t.Fatalf("CheckMembershipRemoval of the last tenant administrator = %v, want ErrLastTenantAdmin", err)
	}
}

// Another tenant administrator keeps the check satisfied, and a role that is not
// tenant-admin is not counted: the floor is the tenant-administrator binding,
// not every binding.
func TestCheckMembershipRemovalAllowsAnotherAdministratorOrNoAdministrator(t *testing.T) {
	p := newFixture(t).product
	seedBinding(t, p, "acme", "acct-1", string(authz.TenantAdminRole))
	seedBinding(t, p, "acme", "acct-2", string(authz.TenantAdminRole))
	seedBinding(t, p, "acme", "acct-3", "viewer")

	if err := p.CheckMembershipRemoval(t.Context(), authn.AccountID("acct-1"), "acme"); err != nil {
		t.Errorf("CheckMembershipRemoval beside another tenant administrator = %v, want nil", err)
	}
	if err := p.CheckMembershipRemoval(t.Context(), authn.AccountID("acct-3"), "acme"); err != nil {
		t.Errorf("CheckMembershipRemoval of an account with no tenant-administrator binding = %v, want nil", err)
	}
}

// The count is the named tenant's: being the only tenant administrator of one
// tenant says nothing about another.
func TestCheckMembershipRemovalCountsOnlyTheNamedTenant(t *testing.T) {
	p := newFixture(t).product
	seedBinding(t, p, "acme", "acct-1", string(authz.TenantAdminRole))
	seedBinding(t, p, "beta", "acct-2", string(authz.TenantAdminRole))

	if err := p.CheckMembershipRemoval(t.Context(), authn.AccountID("acct-1"), "beta"); err != nil {
		t.Errorf("CheckMembershipRemoval in a tenant where the account is no administrator = %v, want nil", err)
	}
	if err := p.CheckMembershipRemoval(t.Context(), authn.AccountID("acct-2"), "acme"); err != nil {
		t.Errorf("CheckMembershipRemoval of an account bound in another tenant = %v, want nil", err)
	}
}
