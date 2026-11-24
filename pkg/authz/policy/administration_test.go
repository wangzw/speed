package policy

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
)

type membershipStub struct {
	calls  []struct{ tenant, account string }
	member bool
	err    error
}

func (m *membershipStub) IsMember(_ context.Context, tenant, account string) (bool, error) {
	m.calls = append(m.calls, struct{ tenant, account string }{tenant: tenant, account: account})
	return m.member, m.err
}

const platform = string(authz.PlatformDomain)

// TestPutRoleRefusesThePlatformAdministratorRoleOutsideThePlatformDomain checks that PutRole refuses the platform administrator role outside the platform domain.
func TestPutRoleRefusesThePlatformAdministratorRoleOutsideThePlatformDomain(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	err := f.PutRole(ctx, authz.RoleRecord{Tenant: "acme", Role: authz.PlatformAdminRole, DescriptionKey: "k"})
	if !errors.Is(err, authz.ErrReservedRole) {
		t.Fatalf("writing the platform administrator role into acme gave %v, want ErrReservedRole", err)
	}
	roles, err := f.Roles(ctx, "acme")
	if err != nil {
		t.Fatalf("Roles(acme): %v", err)
	}
	for _, r := range roles {
		if r.Role == authz.PlatformAdminRole {
			t.Fatalf("the refused role %q was created anyway", r.Role)
		}
	}

	if err := f.PutRole(ctx, authz.RoleRecord{Tenant: platform, Role: authz.PlatformAdminRole, DescriptionKey: "k"}); err != nil {
		t.Fatalf("writing the platform administrator role into the platform domain: %v", err)
	}
}

// TestPutBindingRefusesThePlatformAdministratorRoleOutsideThePlatformDomain checks that PutBinding refuses the platform administrator role outside the platform domain.
func TestPutBindingRefusesThePlatformAdministratorRoleOutsideThePlatformDomain(t *testing.T) {
	f := newFixture(t)

	err := f.PutBinding(t.Context(), authz.Binding{Tenant: "acme", Account: "acct-1", Role: authz.PlatformAdminRole})
	if !errors.Is(err, authz.ErrReservedRole) {
		t.Fatalf("binding the platform administrator role in acme gave %v, want ErrReservedRole", err)
	}
	bindings, err := f.Bindings(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Bindings(acme): %v", err)
	}
	if len(bindings) != 0 {
		t.Fatalf("the refused binding was written anyway: %+v", bindings)
	}
}

// TestTheTenantAdministratorRoleBelongsToEveryDomain checks that the tenant administrator role belongs to every domain.
func TestTheTenantAdministratorRoleBelongsToEveryDomain(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	mustPutRole(t, ctx, f, platform, string(authz.TenantAdminRole))
	mustPutRole(t, ctx, f, "acme", string(authz.TenantAdminRole))
}

// TestPutBindingOfThePlatformAdministratorNeedsAMembershipChecker checks that put binding of the platform administrator needs a membership checker.
func TestPutBindingOfThePlatformAdministratorNeedsAMembershipChecker(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, platform, string(authz.PlatformAdminRole))

	err := f.PutBinding(ctx, authz.Binding{Tenant: platform, Account: "acct-1", Role: authz.PlatformAdminRole})
	if !errors.Is(err, authz.ErrMissingMembershipChecker) {
		t.Fatalf("binding the platform administrator role with no checker gave %v, want ErrMissingMembershipChecker", err)
	}
	bindings, err := f.Bindings(ctx, platform)
	if err != nil {
		t.Fatalf("Bindings(platform): %v", err)
	}
	if len(bindings) != 0 {
		t.Fatalf("the binding was written without a membership check: %+v", bindings)
	}
}

// TestPutBindingOfThePlatformAdministratorChecksMembership checks that put binding of the platform administrator checks membership.
func TestPutBindingOfThePlatformAdministratorChecksMembership(t *testing.T) {
	t.Run("an active member is bound", func(t *testing.T) {
		f := newFixture(t)
		ctx := t.Context()
		mustPutRole(t, ctx, f, platform, string(authz.PlatformAdminRole))
		checker := &membershipStub{member: true}
		f.membership = checker

		mustPutBinding(t, ctx, f, platform, "acct-1", string(authz.PlatformAdminRole))

		if len(checker.calls) != 1 || checker.calls[0].tenant != platform || checker.calls[0].account != "acct-1" {
			t.Fatalf("the checker was asked %+v, want one question about acct-1 in %q", checker.calls, platform)
		}
		bindings, err := f.Bindings(ctx, platform)
		if err != nil {
			t.Fatalf("Bindings(platform): %v", err)
		}
		if len(bindings) != 1 || bindings[0].Account != "acct-1" {
			t.Fatalf("the bindings = %+v, want the one the checker allowed", bindings)
		}
	})

	t.Run("a non-member is refused", func(t *testing.T) {
		f := newFixture(t)
		ctx := t.Context()
		mustPutRole(t, ctx, f, platform, string(authz.PlatformAdminRole))
		f.membership = &membershipStub{member: false}

		err := f.PutBinding(ctx, authz.Binding{Tenant: platform, Account: "acct-1", Role: authz.PlatformAdminRole})
		if err == nil {
			t.Fatal("binding a non-member as platform administrator succeeded, want a refusal")
		}
		if errors.Is(err, authz.ErrMissingMembershipChecker) {
			t.Fatalf("a refused membership was reported as a missing checker: %v", err)
		}
		if !errors.Is(err, authz.ErrPlatformMembershipRequired) {
			t.Fatalf("a refused membership gave %v, want ErrPlatformMembershipRequired", err)
		}
		bindings, err := f.Bindings(ctx, platform)
		if err != nil {
			t.Fatalf("Bindings(platform): %v", err)
		}
		if len(bindings) != 0 {
			t.Fatalf("the non-member's binding was written: %+v", bindings)
		}
	})
}

// TestOtherBindingsDoNotConsultTheMembershipChecker checks that other bindings do not consult the membership checker.
func TestOtherBindingsDoNotConsultTheMembershipChecker(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", string(authz.TenantAdminRole))
	checker := &membershipStub{member: false}
	f.membership = checker

	mustPutBinding(t, ctx, f, "acme", "acct-1", string(authz.TenantAdminRole))
	if len(checker.calls) != 0 {
		t.Fatalf("the checker was asked %+v for a tenant administrator binding outside the platform domain", checker.calls)
	}
}

// TestTheManagerTakesUpAMembershipCheckerOptionally checks that the manager takes up a membership checker optionally.
func TestTheManagerTakesUpAMembershipCheckerOptionally(t *testing.T) {
	p := newProductOver(t)
	ctx := t.Context()
	if err := p.PutRole(ctx, authz.RoleRecord{Tenant: platform, Role: authz.PlatformAdminRole, DescriptionKey: "k"}); err != nil {
		t.Fatalf("PutRole(platform, platform-admin): %v", err)
	}

	checker := &membershipStub{member: true}
	reg := core.New()
	reg.Register(core.Module{
		Name:     "stand-in.membership",
		Provides: []core.Provision{{Token: (*authz.MembershipChecker)(nil)}},
	})
	reg.Supply(checker, "stand-in.membership")
	if err := p.wire(reg); err != nil {
		t.Fatalf("wiring the product: %v", err)
	}

	if err := p.PutBinding(ctx, authz.Binding{Tenant: platform, Account: "acct-1", Role: authz.PlatformAdminRole}); err != nil {
		t.Fatalf("PutBinding with a wired checker: %v", err)
	}
	if len(checker.calls) != 1 {
		t.Fatalf("the wired checker was asked %d times, want one", len(checker.calls))
	}
}

// TestDeletingTheLastTenantAdministratorBindingIsRefused checks that deleting the last tenant administrator binding is refused.
func TestDeletingTheLastTenantAdministratorBindingIsRefused(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", string(authz.TenantAdminRole))
	mustPutBinding(t, ctx, f, "acme", "acct-1", string(authz.TenantAdminRole))

	err := f.DeleteBinding(ctx, "acme", "acct-1", authz.TenantAdminRole)
	if !errors.Is(err, authz.ErrLastTenantAdmin) {
		t.Fatalf("deleting the last tenant administrator gave %v, want ErrLastTenantAdmin", err)
	}
	bindings, err := f.BindingsOf(ctx, "acme", "acct-1")
	if err != nil {
		t.Fatalf("BindingsOf(acme, acct-1): %v", err)
	}
	if len(bindings) != 1 || bindings[0].Role != authz.TenantAdminRole {
		t.Fatalf("the refused delete removed the binding: %+v", bindings)
	}
}

// TestDeletingATenantAdministratorBindingLeavesTheOthers checks that deleting a tenant administrator binding leaves the others.
func TestDeletingATenantAdministratorBindingLeavesTheOthers(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", string(authz.TenantAdminRole))
	mustPutBinding(t, ctx, f, "acme", "acct-1", string(authz.TenantAdminRole))
	mustPutBinding(t, ctx, f, "acme", "acct-2", string(authz.TenantAdminRole))

	if err := f.DeleteBinding(ctx, "acme", "acct-1", authz.TenantAdminRole); err != nil {
		t.Fatalf("deleting one of two tenant administrators: %v", err)
	}
	if got := f.bindingsOf(t, "acme", "acct-1"); len(got) != 0 {
		t.Fatalf("the deleted binding is still there: %+v", got)
	}
	if got := f.bindingsOf(t, "acme", "acct-2"); len(got) != 1 {
		t.Fatalf("the other administrator's binding = %+v, want it untouched", got)
	}
	err := f.DeleteBinding(ctx, "acme", "acct-2", authz.TenantAdminRole)
	if !errors.Is(err, authz.ErrLastTenantAdmin) {
		t.Fatalf("deleting the remaining tenant administrator gave %v, want ErrLastTenantAdmin", err)
	}
}

// TestDeletingTheLastBindingOfAnotherRoleIsAllowed checks that deleting the last binding of another role is allowed.
func TestDeletingTheLastBindingOfAnotherRoleIsAllowed(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	mustPutRole(t, ctx, f, "acme", "editor")
	mustPutBinding(t, ctx, f, "acme", "acct-1", "editor")

	if err := f.DeleteBinding(ctx, "acme", "acct-1", "editor"); err != nil {
		t.Fatalf("deleting the last binding of an ordinary role: %v", err)
	}
}

func (f *fixture) bindingsOf(t *testing.T, tenant, account string) []authz.Binding {
	t.Helper()
	bindings, err := f.BindingsOf(t.Context(), tenant, account)
	if err != nil {
		t.Fatalf("BindingsOf(%s, %s): %v", tenant, account, err)
	}
	return bindings
}
