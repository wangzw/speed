package admin

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// TestTheTenantViewsNameTheTenantName checks that the member, provider and impersonation views carry the tenant's name.
func TestTheTenantViewsNameTheTenantName(t *testing.T) {
	member := memberViewOf(authn.Membership{Account: "acct-1", Tenant: "t1"}, "", "", nil, "acme")
	if member.Tenant != "acme" {
		t.Errorf("the member view names %q, want the name acme", member.Tenant)
	}
	provider := providerViewOf(authn.SSOProvider{Tenant: "t1"}, nil, "acme")
	if provider.Tenant != "acme" {
		t.Errorf("the provider view names %q, want the name acme", provider.Tenant)
	}
	grant := grantViewOf(authn.ImpersonationGrant{Tenant: "t1"}, "acme")
	if grant.Tenant != "acme" {
		t.Errorf("the impersonation view names %q, want the name acme", grant.Tenant)
	}
}

type mappingNames map[string]string

func (m mappingNames) TenantNameOf(_ context.Context, id string) (string, error) {
	return m[id], nil
}

// TestTheProtectionDetailsNameTheName holds the 409 details of the account floor and the organization-owner rule to the name, never the identifier.
func TestTheProtectionDetailsNameTheName(t *testing.T) {
	p := &product{tenantNames: mappingNames{"t1": "acme"}}
	for name, err := range map[string]error{
		"last administrator": &authn.AccountLastTenantAdminError{Tenant: "t1"},
		"organization owner": &authn.AccountOwnsOrganizationError{Tenant: "t1"},
	} {
		t.Run(name, func(t *testing.T) {
			got := p.answer(context.Background(), "test", err)
			var fault speedhttp.Fault
			if !errors.As(got, &fault) {
				t.Fatalf("the answer is %T, want a fault", got)
			}
			if fault.Status != http.StatusConflict {
				t.Fatalf("status = %d, want 409", fault.Status)
			}
			if fault.Detail != "acme" {
				t.Errorf("detail = %q, want the name acme", fault.Detail)
			}
		})
	}
}
