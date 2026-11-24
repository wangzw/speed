package admin

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

// TestThePolicyViewsNameTheName checks that the policy views name the name.
func TestThePolicyViewsNameTheName(t *testing.T) {
	p := &product{}
	role := p.roleViewOf(context.Background(), authz.RoleRecord{Tenant: "t1", Role: "editor"}, 0, nil, false, "acme")
	if role.Tenant != "acme" {
		t.Errorf("the role view names %q, want the name acme", role.Tenant)
	}
	binding := bindingViewOf(authz.Binding{Tenant: "t1"}, "acme")
	if binding.Tenant != "acme" {
		t.Errorf("the binding view names %q, want the name acme", binding.Tenant)
	}
}
