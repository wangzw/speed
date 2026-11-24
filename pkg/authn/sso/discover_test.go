package sso

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

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

// TestDiscoverIgnoresTheDeliveredProtocols checks that Discover does not filter by the delivered protocols.
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

// TestDiscoverReturnsOnlyVerifiedAndEnabledRows checks that Discover returns only verified domains of enabled providers.
func TestDiscoverReturnsOnlyVerifiedAndEnabledRows(t *testing.T) {
	r := newRigSQLite(t)

	live := r.verified("tenant-a", "live", "example.com")

	off := r.verified("tenant-b", "off", "disabled.example.com")
	if _, err := r.p.Put(context.Background(), authn.SSOProvider{
		ID: off.ID, Tenant: "tenant-b", Protocol: "oidc", Name: "off", Enabled: false,
		Provisioning: "jit", UsernameStrategy: "user-confirm", Config: oidcConfig(),
	}, nil); err != nil {
		t.Fatalf("disabling a provider: %v", err)
	}

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

// TestDiscoverOfAnUnknownDomainIsEmptyAndShaped checks that an unknown domain answers an empty answer of the declared shape.
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

// TestDiscoverRefusesADomainThatIsNotOne checks that a domain that cannot be normalized is refused.
func TestDiscoverRefusesADomainThatIsNotOne(t *testing.T) {
	r := newRigSQLite(t)
	if _, err := r.p.Discover(context.Background(), "例子.测试"); !errors.Is(err, errInvalidDomain) {
		t.Errorf("Discover of a non-ASCII domain returned %v, want errInvalidDomain", err)
	}
}
