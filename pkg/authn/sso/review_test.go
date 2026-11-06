package sso

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// fakeConfigReader answers the SAML key configuration with a case's value, so a
// Put can be driven with the service provider's key reported as present.
type fakeConfigReader struct{ keyFile string }

func (f fakeConfigReader) Decode(path string, target any) error {
	if path == samlNamespace {
		if s, ok := target.(*samlKeySettings); ok {
			s.SPKeyFile = f.keyFile
		}
	}
	return nil
}

func oidcProviderOf(cfg string) authn.SSOProvider {
	return authn.SSOProvider{
		Tenant: "tenant-a", Protocol: "oidc", Name: "p", Provisioning: "jit",
		UsernameStrategy: "user-confirm", Config: json.RawMessage(cfg),
	}
}

func samlProviderOf(cfg string) authn.SSOProvider {
	return authn.SSOProvider{
		Tenant: "tenant-a", Protocol: "saml", Name: "p", Provisioning: "jit",
		UsernameStrategy: "user-confirm", Config: json.RawMessage(cfg),
	}
}

// TestTheConfigRulesHoldAtTheirEdges drives each config rule at the boundary a
// value is accepted or refused, both the refusing and the succeeding half.
func TestTheConfigRulesHoldAtTheirEdges(t *testing.T) {
	r := newRigSQLite(t)
	base := `"idp_entity_id":"e","idp_sso_url":"https://idp.example.com/sso","idp_certs":["x"]`
	cases := []struct {
		name    string
		in      authn.SSOProvider
		wantErr error
	}{
		{"empty idp_entity_id", samlProviderOf(`{"idp_entity_id":"","idp_sso_url":"https://idp.example.com/sso","idp_certs":["x"]}`), authn.ErrInvalidConfig},
		{"no certificates", samlProviderOf(`{` + base + `,"idp_certs":[]}`), authn.ErrInvalidConfig},
		{"exactly one certificate", samlProviderOf(`{` + base + `}`), nil},
		{"exactly four certificates", samlProviderOf(`{"idp_entity_id":"e","idp_sso_url":"https://idp.example.com/sso","idp_certs":["a","b","c","d"]}`), nil},
		{"empty client_id", oidcProviderOf(`{"issuer":"https://idp.example.com","client_id":""}`), authn.ErrInvalidConfig},
		{"empty certificate element", samlProviderOf(`{"idp_entity_id":"e","idp_sso_url":"https://idp.example.com/sso","idp_certs":[""]}`), authn.ErrInvalidConfig},
		{"empty acr element", oidcProviderOf(`{"issuer":"https://idp.example.com","client_id":"c","mfa_acr_values":[""]}`), authn.ErrInvalidConfig},
		{"scopes of the wrong type", oidcProviderOf(`{"issuer":"https://idp.example.com","client_id":"c","scopes":5}`), authn.ErrInvalidConfig},
		{"empty claim name", oidcProviderOf(`{"issuer":"https://idp.example.com","client_id":"c","claims":{"":"email"}}`), authn.ErrInvalidConfig},
		{"attr name at the bound", oidcProviderOf(`{"issuer":"https://idp.example.com","client_id":"c","claims":{"a":"attr.` + strings.Repeat("x", 29) + `"}}`), authn.ErrInvalidConfig},
		{"an unknown claim target", oidcProviderOf(`{"issuer":"https://idp.example.com","client_id":"c","claims":{"x":"attrs.y"}}`), authn.ErrInvalidConfig},
		{"issuer with no host", oidcProviderOf(`{"issuer":"https://","client_id":"c"}`), authn.ErrInvalidConfig},
		{"sso url with no host", samlProviderOf(`{"idp_entity_id":"e","idp_sso_url":"https://","idp_certs":["x"]}`), authn.ErrInvalidConfig},
		{"a non-https metadata url", samlProviderOf(`{"idp_entity_id":"e","idp_sso_url":"https://idp.example.com/sso","idp_certs":["x"],"metadata_url":"http://x"}`), authn.ErrInvalidConfig},
		{"an empty subject attribute", samlProviderOf(`{"idp_entity_id":"e","idp_sso_url":"https://idp.example.com/sso","idp_certs":["x"],"subject_attribute":""}`), authn.ErrInvalidConfig},
		{"a valid claims map", oidcProviderOf(`{"issuer":"https://idp.example.com","client_id":"c","claims":{"name":"email"},"mfa_acr_values":["urn:x"]}`), nil},
		{"the subject claim target", oidcProviderOf(`{"issuer":"https://idp.example.com","client_id":"c","claims":{"sub":"subject"}}`), nil},
		{"a stable claim mapped to subject", oidcProviderOf(`{"issuer":"https://idp.example.com","client_id":"c","claims":{"preferred_username":"subject"}}`), nil},
		{"the email claim mapped to subject", oidcProviderOf(`{"issuer":"https://idp.example.com","client_id":"c","claims":{"email":"subject"}}`), authn.ErrInvalidConfig},
		{"a SAML subject attribute that is the mapped address", samlProviderOf(`{` + base + `,"subject_attribute":"mail","claims":{"mail":"email"}}`), authn.ErrInvalidConfig},
		{"a SAML subject attribute that is not the mapped address", samlProviderOf(`{` + base + `,"subject_attribute":"uid","claims":{"mail":"email"}}`), nil},
		{"the display-name claim target", oidcProviderOf(`{"issuer":"https://idp.example.com","client_id":"c","claims":{"cn":"display_name"}}`), nil},
		{"a valid attr claim target", oidcProviderOf(`{"issuer":"https://idp.example.com","client_id":"c","claims":{"g":"attr.given_name"}}`), nil},
		{"an emailAddress name id with a subject attribute", samlProviderOf(`{"idp_entity_id":"e","idp_sso_url":"https://idp.example.com/sso","idp_certs":["x"],"name_id_format":"emailAddress","subject_attribute":"mail"}`), nil},
		{"a persistent name id", samlProviderOf(`{"idp_entity_id":"e","idp_sso_url":"https://idp.example.com/sso","idp_certs":["x"],"name_id_format":"persistent"}`), nil},
		{"an unspecified name id", samlProviderOf(`{"idp_entity_id":"e","idp_sso_url":"https://idp.example.com/sso","idp_certs":["x"],"name_id_format":"unspecified"}`), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.p.Put(context.Background(), tc.in, nil)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Put = %v, want success", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Put = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestIsConcreteIssuerRefusesEveryAggregatorPlaceholder holds that all three
// multi-tenant placeholders are refused, whatever their case.
func TestIsConcreteIssuerRefusesEveryAggregatorPlaceholder(t *testing.T) {
	r := newRigSQLite(t)
	for _, segment := range []string{"common", "organizations", "consumers", "COMMON", "Organizations"} {
		cfg := `{"issuer":"https://login.example.com/` + segment + `/v2.0","client_id":"c"}`
		if _, err := r.p.Put(context.Background(), oidcProviderOf(cfg), nil); !errors.Is(err, authn.ErrInvalidConfig) {
			t.Errorf("an issuer with the placeholder %q was accepted: %v", segment, err)
		}
	}
}

// TestTheNameBoundIsLiteral pins the name-length bound to literal lengths, so a
// changed constant moves the bound and not the test.
func TestTheNameBoundIsLiteral(t *testing.T) {
	r := newRigSQLite(t)
	at := oidcProviderOf(`{"issuer":"https://idp.example.com","client_id":"c"}`)
	at.Name = strings.Repeat("x", 64)
	if _, err := r.p.Put(context.Background(), at, nil); err != nil {
		t.Errorf("a 64-character name was refused: %v", err)
	}
	over := oidcProviderOf(`{"issuer":"https://idp.example.com","client_id":"c"}`)
	over.Name = strings.Repeat("x", 65)
	if _, err := r.p.Put(context.Background(), over, nil); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("a 65-character name was answered %v, want ErrInvalidConfig", err)
	}
}

// TestNormalizeDomainHoldsItsLengthAndLabelBounds holds the domain rule at the
// total-length and label-length boundaries and against a character outside the
// set.
func TestNormalizeDomainHoldsItsLengthAndLabelBounds(t *testing.T) {
	at253 := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	if len(at253) != 253 {
		t.Fatalf("the fixture is %d bytes, want 253", len(at253))
	}
	if _, err := normalizeDomain(at253); err != nil {
		t.Errorf("a 253-byte domain was refused: %v", err)
	}
	if _, err := normalizeDomain(at253 + "a"); !errors.Is(err, errInvalidDomain) {
		t.Errorf("a 254-byte domain was answered %v, want errInvalidDomain", err)
	}
	if _, err := normalizeDomain(strings.Repeat("a", 63) + ".com"); err != nil {
		t.Errorf("a 63-byte label was refused: %v", err)
	}
	if _, err := normalizeDomain(strings.Repeat("a", 64) + ".com"); !errors.Is(err, errInvalidDomain) {
		t.Errorf("a 64-byte label was answered %v, want errInvalidDomain", err)
	}
	if _, err := normalizeDomain("a_b.com"); !errors.Is(err, errInvalidDomain) {
		t.Errorf("an underscore was answered %v, want errInvalidDomain", err)
	}
}

// TestTheVerifyTokenIsFullLength pins the domain-ownership token to its declared
// entropy, so a reduction is caught.
func TestTheVerifyTokenIsFullLength(t *testing.T) {
	r := newRigSQLite(t)
	idp := r.putOIDC("tenant-a", "idp")
	token, err := r.p.ClaimDomain(context.Background(), idp.ID, "example.com")
	if err != nil {
		t.Fatalf("ClaimDomain: %v", err)
	}
	if len(token) != 2*verifyTokenBytes {
		t.Errorf("the token is %d characters, want %d", len(token), 2*verifyTokenBytes)
	}
}

// TestHoldsTokenToleratesWhitespace holds that a TXT record with surrounding
// whitespace still verifies the domain.
func TestHoldsTokenToleratesWhitespace(t *testing.T) {
	r := newRigSQLite(t)
	idp := r.putOIDC("tenant-a", "idp")
	token, err := r.p.ClaimDomain(context.Background(), idp.ID, "example.com")
	if err != nil {
		t.Fatalf("ClaimDomain: %v", err)
	}
	r.txt[txtName("example.com")] = []string{" speed-authn=" + token + " "}
	if err := r.p.VerifyDomain(context.Background(), idp.ID, "example.com"); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	if !r.verifyState(idp.ID, "example.com") {
		t.Error("a domain verified from a whitespace-padded record is not marked verified")
	}
}

// TestTheNotFoundSentinelsHold holds the ErrProviderNotFound answers a read of a
// provider that is not there.
func TestTheNotFoundSentinelsHold(t *testing.T) {
	r := newRigSQLite(t)
	ctx := context.Background()
	const missing = "0192f5a0-0000-7000-8000-000000000000"
	if _, err := r.p.Secret(ctx, missing); !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("Secret of a missing provider = %v, want ErrProviderNotFound", err)
	}
	if err := r.p.Delete(ctx, missing); !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("Delete of a missing provider = %v, want ErrProviderNotFound", err)
	}
}

// TestVerifyDomainWithoutAClaimIsRefused holds the guard that a verification
// asked for with no claim returns the claim sentinel and looks up nothing.
func TestVerifyDomainWithoutAClaimIsRefused(t *testing.T) {
	r := newRigSQLite(t)
	r.txt[txtName("example.com")] = []string{"speed-authn=whatever"}
	if err := r.p.VerifyDomain(context.Background(), "0192f5a0-0000-7000-8000-000000000000", "example.com"); !errors.Is(err, errNoDomainClaim) {
		t.Errorf("VerifyDomain without a claim = %v, want errNoDomainClaim", err)
	}
}

// TestMarkVerifiedWithoutAClaimIsRefused holds that a verification write that
// affects no row reports the claim sentinel.
func TestMarkVerifiedWithoutAClaimIsRefused(t *testing.T) {
	r := newRigSQLite(t)
	idp := r.putOIDC("tenant-a", "idp")
	if err := r.p.register.markVerified(context.Background(), idp.ID, "example.com", r.p.now()); !errors.Is(err, errNoDomainClaim) {
		t.Errorf("markVerified with no claim = %v, want errNoDomainClaim", err)
	}
}

// TestRememberAssertionRefusesAnEmptyArgument holds that an empty provider or an
// empty identifier is refused.
func TestRememberAssertionRefusesAnEmptyArgument(t *testing.T) {
	r := newRigSQLite(t)
	ctx := context.Background()
	when := base.Add(time.Minute)
	if err := r.p.RememberAssertion(ctx, "", "assertion-1", when); err == nil {
		t.Error("RememberAssertion with an empty provider did not fail")
	}
	if err := r.p.RememberAssertion(ctx, "idp-1", "", when); err == nil {
		t.Error("RememberAssertion with an empty identifier did not fail")
	}
}

// TestSweepKeepsAClaimInsideTheRetentionWindow holds the retention: a claim two
// days old is inside the seven-day window and survives the sweep.
func TestSweepKeepsAClaimInsideTheRetentionWindow(t *testing.T) {
	r := newRigSQLite(t)
	r.insertClaim("two-day.example.com", "idp-1", "tenant-a", "t", false, base.Add(-48*time.Hour))

	if err := r.p.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := r.count(domainTable); got != 1 {
		t.Errorf("after the sweep the claims = %d, want the two-day claim kept", got)
	}
}

// TestASignedRequestWithAConfiguredKeyIsAccepted holds the succeeding half of the
// sign_requests rule: a deployment that carries the key takes a provider that
// signs its requests.
func TestASignedRequestWithAConfiguredKeyIsAccepted(t *testing.T) {
	r := newRigSQLite(t)
	r.p.reader = fakeConfigReader{keyFile: "/tmp/sp-key.pem"}
	cfg := samlProviderOf(`{"idp_entity_id":"e","idp_sso_url":"https://idp.example.com/sso","idp_certs":["x"],"sign_requests":true}`)
	if _, err := r.p.Put(context.Background(), cfg, nil); err != nil {
		t.Fatalf("Put with sign_requests and a configured key = %v, want success", err)
	}
}

// TestTheSettingsDomainsHoldTheirBounds holds each settings domain at the value
// just inside and just outside each end.
func TestTheSettingsDomainsHoldTheirBounds(t *testing.T) {
	domains := []struct {
		name string
		fn   func(time.Duration) bool
		in   []time.Duration
		out  []time.Duration
	}{
		{"assertionMaxAge", assertionMaxAgeInDomain, []time.Duration{time.Minute, 30 * time.Minute}, []time.Duration{59 * time.Second, 30*time.Minute + 1}},
		{"providerCacheTTL", providerCacheTTLInDomain, []time.Duration{5 * time.Second, time.Hour}, []time.Duration{4 * time.Second, time.Hour + 1}},
		{"egressTimeout", egressTimeoutInDomain, []time.Duration{time.Second, time.Minute}, []time.Duration{999 * time.Millisecond, time.Minute + 1}},
	}
	for _, d := range domains {
		for _, v := range d.in {
			if !d.fn(v) {
				t.Errorf("%sInDomain(%s) = false, want true", d.name, v)
			}
		}
		for _, v := range d.out {
			if d.fn(v) {
				t.Errorf("%sInDomain(%s) = true, want false", d.name, v)
			}
		}
	}
}

// TestTheDiscoveryTakesTheDomainAfterTheLastAt holds that the domain is
// everything after the last at sign.
func TestTheDiscoveryTakesTheDomainAfterTheLastAt(t *testing.T) {
	r := routed(t)
	w := r.post(discoverFullPath, `{"email":"a@b@example.com"}`)
	if w.Code != http.StatusOK {
		t.Errorf("the discovery of an address with two at signs answered %d, want 200: %s", w.Code, w.Body.String())
	}
}
