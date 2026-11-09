package admin

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

// TestThePolicyViewsNameTheName holds the role, grant and binding views to the
// name: the policy store keys rows by the identifier, and a client reads the
// name it addresses the tenant by.
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
