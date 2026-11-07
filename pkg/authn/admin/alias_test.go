package admin

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// TestTheTenantViewsNameTheAlias holds the value every management view answers
// for a tenant: the alias the request surface uses, never the internal
// identifier.
func TestTheTenantViewsNameTheAlias(t *testing.T) {
	member := memberViewOf(authn.Membership{Account: "acct-1", Tenant: "t1"}, "", "", nil, "acme")
	if member.Tenant != "acme" {
		t.Errorf("the member view names %q, want the alias acme", member.Tenant)
	}
	provider := providerViewOf(authn.SSOProvider{Tenant: "t1"}, nil, "acme")
	if provider.Tenant != "acme" {
		t.Errorf("the provider view names %q, want the alias acme", provider.Tenant)
	}
	grant := grantViewOf(authn.ImpersonationGrant{Tenant: "t1"}, "acme")
	if grant.Tenant != "acme" {
		t.Errorf("the impersonation view names %q, want the alias acme", grant.Tenant)
	}
}

// mappingAliases answers one identifier's alias.
type mappingAliases map[string]string

func (m mappingAliases) TenantAliasOf(_ context.Context, id string) (string, error) {
	return m[id], nil
}

// TestTheProtectionDetailsNameTheAlias holds the 409 details of the account
// floor and the organization-owner rule to the alias, never the identifier.
func TestTheProtectionDetailsNameTheAlias(t *testing.T) {
	p := &product{tenantAliases: mappingAliases{"t1": "acme"}}
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
				t.Errorf("detail = %q, want the alias acme", fault.Detail)
			}
		})
	}
}
