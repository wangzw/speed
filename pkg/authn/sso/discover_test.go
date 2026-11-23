package sso

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// verified registers a provider and verifies a domain for it.
func (r *rig) verified(tenant, name, domain string) authn.SSOProvider {
	r.t.Helper()
	idp := r.putOIDC(tenant, name)
	token, err := r.p.ClaimDomain(context.Background(), idp.ID, domain)
	if err != nil {
		r.t.Fatalf("ClaimDomain: %v", err)
	}
	r.publishTXT(domain, token)
	if err := r.p.VerifyDomain(context.Background(), idp.ID, domain); err != nil {
		r.t.Fatalf("VerifyDomain: %v", err)
	}
	return idp
}

// verifiedSAML registers a SAML provider and verifies a domain for it.
func (r *rig) verifiedSAML(tenant, name, domain string) authn.SSOProvider {
	r.t.Helper()
	idp := r.putSAML(tenant, name)
	token, err := r.p.ClaimDomain(context.Background(), idp.ID, domain)
	if err != nil {
		r.t.Fatalf("ClaimDomain: %v", err)
	}
	r.publishTXT(domain, token)
	if err := r.p.VerifyDomain(context.Background(), idp.ID, domain); err != nil {
		r.t.Fatalf("VerifyDomain: %v", err)
	}
	return idp
}

// TestDiscoverIgnoresTheDeliveredProtocols holds that Discover itself does not
// filter by what the run delivers: the same call decides which provider owns a
// domain, so a provider whose protocol is not delivered still answers for the
// domain it holds, and the answer layer alone leaves it out of the discovery.
func TestDiscoverIgnoresTheDeliveredProtocols(t *testing.T) {
	r := newRigSQLite(t)
	idp := r.verified("tenant-a", "live", "example.com")
	r.deliverProtocols()

	got, err := r.p.Discover(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 || got[0].ID != idp.ID {
		t.Errorf("Discover(example.com) = %+v, want the provider even though its protocol is not delivered", got)
	}
}

// TestDiscoverReturnsOnlyVerifiedAndEnabledRows holds the discovery's filter: a
// verified domain of an enabled provider, and nothing else.
func TestDiscoverReturnsOnlyVerifiedAndEnabledRows(t *testing.T) {
	r := newRigSQLite(t)

	live := r.verified("tenant-a", "live", "example.com")

	// A verified domain, but the provider is disabled.
	off := r.verified("tenant-b", "off", "disabled.example.com")
	if _, err := r.p.Put(context.Background(), authn.SSOProvider{
		ID: off.ID, Tenant: "tenant-b", Protocol: "oidc", Name: "off", Enabled: false,
		Provisioning: "jit", UsernameStrategy: "user-confirm", Config: oidcConfig(),
	}, nil); err != nil {
		t.Fatalf("disabling a provider: %v", err)
	}

	// An enabled provider with a claim that is not verified.
	unverified := r.putOIDC("tenant-c", "unverified")
	if _, err := r.p.ClaimDomain(context.Background(), unverified.ID, "pending.example.com"); err != nil {
		t.Fatalf("ClaimDomain: %v", err)
	}

	got, err := r.p.Discover(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 || got[0].ID != live.ID {
		t.Errorf("Discover(example.com) = %+v, want only the live provider", got)
	}
	if got, err := r.p.Discover(context.Background(), "disabled.example.com"); err != nil || len(got) != 0 {
		t.Errorf("Discover of a disabled provider's domain = %+v, %v, want none", got, err)
	}
	if got, err := r.p.Discover(context.Background(), "pending.example.com"); err != nil || len(got) != 0 {
		t.Errorf("Discover of an unverified domain = %+v, %v, want none", got, err)
	}
}

// TestDiscoverOfAnUnknownDomainIsEmptyAndShaped holds that a domain nobody holds
// answers the same empty list, with no error.
func TestDiscoverOfAnUnknownDomainIsEmptyAndShaped(t *testing.T) {
	r := newRigSQLite(t)
	r.verified("tenant-a", "live", "example.com")

	got, err := r.p.Discover(context.Background(), "nobody.example.org")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Discover of an unknown domain = %+v, want none", got)
	}
	if got == nil {
		t.Error("Discover of an unknown domain returned a nil slice, want an empty one")
	}
}

// TestDiscoverRefusesADomainThatIsNotOne holds the failure a request with a
// domain that cannot be reduced carries.
func TestDiscoverRefusesADomainThatIsNotOne(t *testing.T) {
	r := newRigSQLite(t)
	if _, err := r.p.Discover(context.Background(), "例子.测试"); !errors.Is(err, errInvalidDomain) {
		t.Errorf("Discover of a non-ASCII domain returned %v, want errInvalidDomain", err)
	}
}
