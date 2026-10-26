package admin

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// domainClaimJSON is one entry of a provider view's domains member.
type domainClaimJSON struct {
	Domain   string `json:"domain"`
	Verified bool   `json:"verified"`
}

// TestProviderViewsCarryTheirDomains holds that a provider view reports every
// email domain the provider has claimed and whether it is verified, on both the
// list and the single-provider read. A claim awaiting its DNS record reads as
// unverified; the domain that record proved reads as verified.
func TestProviderViewsCarryTheirDomains(t *testing.T) {
	r := newRig(t)
	r.providers.put(authn.SSOProvider{
		ID: idpA, Tenant: tenantA, Protocol: authn.SSOProtocolSAML, Provisioning: authn.ProvisioningJIT,
	})
	r.providers.claimDomain(idpA, authn.SSOProviderDomain{Domain: "claimed.example", Verified: false})
	r.providers.claimDomain(idpA, authn.SSOProviderDomain{Domain: "proved.example", Verified: true})

	want := []domainClaimJSON{
		{Domain: "claimed.example", Verified: false},
		{Domain: "proved.example", Verified: true},
	}

	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/sso/providers", nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Providers []struct {
			Domains []domainClaimJSON `json:"domains"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decoding the list body: %v", err)
	}
	if len(listed.Providers) != 1 {
		t.Fatalf("the list holds %d providers, want 1", len(listed.Providers))
	}
	if got := listed.Providers[0].Domains; !slices.Equal(got, want) {
		t.Errorf("the listed provider's domains = %+v, want %+v", got, want)
	}

	rec = r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/sso/providers/"+idpA, nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var one struct {
		Provider struct {
			Domains []domainClaimJSON `json:"domains"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil {
		t.Fatalf("decoding the get body: %v", err)
	}
	if got := one.Provider.Domains; !slices.Equal(got, want) {
		t.Errorf("the single provider's domains = %+v, want %+v", got, want)
	}
}

// TestAProviderWithNoDomainsCarriesAnEmptyArray holds the response contract the
// console reads: the domains member is always an array and never null.
func TestAProviderWithNoDomainsCarriesAnEmptyArray(t *testing.T) {
	r := newRig(t)
	r.providers.put(authn.SSOProvider{
		ID: idpA, Tenant: tenantA, Protocol: authn.SSOProtocolSAML, Provisioning: authn.ProvisioningJIT,
	})

	rec := r.do(http.MethodGet, "/api/v1/tenants/"+tenantA+"/sso/providers/"+idpA, nil, fresh()...)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var body map[string]struct {
		Domains *[]domainClaimJSON `json:"domains"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the get body: %v", err)
	}
	domains := body["provider"].Domains
	if domains == nil {
		t.Fatal("the provider view carries no domains member")
	}
	if len(*domains) != 0 {
		t.Errorf("a provider with no claims carries %d domains, want none", len(*domains))
	}
}
