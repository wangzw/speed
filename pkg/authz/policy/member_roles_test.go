package policy

import (
	"errors"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestThePolicyProductAnswersTheMemberRoleSeam holds that this module delivers
// the optional seam the member listing reads: an assembly that takes the policy
// module in can show each member's roles without the membership store learning
// about policy.
func TestThePolicyProductAnswersTheMemberRoleSeam(t *testing.T) {
	if _, ok := any(newFixture(t).product).(authn.MemberRoleReader); !ok {
		t.Fatal("the policy product does not implement authn.MemberRoleReader")
	}
}

// TestTheModuleProvidesTheMemberRoleSeam holds the descriptor: the seam is a
// provision of this module, so an assembly resolves it without a stand-in.
func TestTheModuleProvidesTheMemberRoleSeam(t *testing.T) {
	for _, provision := range Module().Provides {
		if provision.Token == (*authn.MemberRoleReader)(nil) {
			return
		}
	}
	t.Error("the policy module's descriptor does not provide authn.MemberRoleReader")
}

// TestMemberRolesAnswersOneTenantByName holds the bulk read: every account's
// role names in one tenant, ascending and without duplicates, and no account of
// another tenant.
func TestMemberRolesAnswersOneTenantByName(t *testing.T) {
	p := newFixture(t).product
	reader, ok := any(p).(authn.MemberRoleReader)
	if !ok {
		t.Fatal("the policy product does not implement authn.MemberRoleReader")
	}
	seedPolicy(t, p, "acme", "zulu", "acct-1", "account", "read")
	seedPolicy(t, p, "acme", "alpha", "acct-1", "account", "read")
	seedBinding(t, p, "acme", "acct-2", "alpha")
	seedPolicy(t, p, "globex", "globex-only", "acct-1", "account", "read")

	roles, err := reader.MemberRoles(t.Context(), "acme")
	if err != nil {
		t.Fatalf("MemberRoles(acme): %v", err)
	}
	if got := roles["acct-1"]; !slices.Equal(got, []string{"alpha", "zulu"}) {
		t.Errorf("the roles of acct-1 = %v, want [alpha zulu]", got)
	}
	if got := roles["acct-2"]; !slices.Equal(got, []string{"alpha"}) {
		t.Errorf("the roles of acct-2 = %v, want [alpha]", got)
	}
	if got, present := roles["acct-3"]; present && len(got) != 0 {
		t.Errorf("the roles of the unbound acct-3 = %v, want no entry", got)
	}
	if _, present := roles["acct-1"]; present {
		if got := roles["acct-1"]; slices.Contains(got, "globex-only") {
			t.Errorf("the roles of acct-1 = %v, want no role of another tenant", got)
		}
	}
}

// TestMemberRolesRefusesAnEmptyTenant holds the seam's stated refusal: a read
// that names no tenant names no rows.
func TestMemberRolesRefusesAnEmptyTenant(t *testing.T) {
	p := newFixture(t).product
	reader, ok := any(p).(authn.MemberRoleReader)
	if !ok {
		t.Fatal("the policy product does not implement authn.MemberRoleReader")
	}
	if _, err := reader.MemberRoles(t.Context(), ""); !errors.Is(err, authn.ErrInvalidArgument) {
		t.Errorf("MemberRoles(\"\") = %v, want ErrInvalidArgument", err)
	}
}
