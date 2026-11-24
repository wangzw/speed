package policy

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
)

// TestMembershipRemovedWithdrawsThePlatformBindings checks that membership removed withdraws the platform bindings.
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

	if err := p.MembershipRemoved(t.Context(), authn.AccountID("acct-1"), string(authz.PlatformDomain)); err != nil {
		t.Fatalf("repeating MembershipRemoved: %v", err)
	}
	assertBindingCount(t, p, string(authz.PlatformDomain), "acct-1", 0)
}

// TestMembershipRemovedInABusinessTenantClearsItsOwnBindings checks that membership removed in a business tenant clears its own bindings.
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

// TestCheckMembershipRemovalRefusesTheLastTenantAdmin checks that check membership removal refuses the last tenant admin.
func TestCheckMembershipRemovalRefusesTheLastTenantAdmin(t *testing.T) {
	p := newFixture(t).product
	seedBinding(t, p, "acme", "acct-1", string(authz.TenantAdminRole))

	err := p.CheckMembershipRemoval(t.Context(), authn.AccountID("acct-1"), "acme")
	if !errors.Is(err, authn.ErrLastTenantAdmin) {
		t.Fatalf("CheckMembershipRemoval of the last tenant administrator = %v, want ErrLastTenantAdmin", err)
	}
}

// TestCheckMembershipRemovalAllowsAnotherAdministratorOrNoAdministrator checks that check membership removal allows another administrator or no administrator.
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

// TestCheckMembershipRemovalCountsOnlyTheNamedTenant checks that check membership removal counts only the named tenant.
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
