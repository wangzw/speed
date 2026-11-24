package saml

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// TestTheSHA1PreCheckSeesEveryXMLSpellingOfAnAlgorithm checks that the SHA-1 pre-check sees every XML spelling of an algorithm.
func TestTheSHA1PreCheckSeesEveryXMLSpellingOfAnAlgorithm(t *testing.T) {
	cases := map[string]string{
		"double quotes":   `<a><SignatureMethod Algorithm="http://www.w3.org/2000/09/xmldsig#rsa-sha1"/></a>`,
		"single quotes":   `<a><SignatureMethod Algorithm='http://www.w3.org/2000/09/xmldsig#rsa-sha1'/></a>`,
		"decimal ref":     `<a><SignatureMethod Algorithm="http://www.w3.org/2000/09/xmldsig#rsa-&#115;ha1"/></a>`,
		"hexadecimal ref": `<a><SignatureMethod Algorithm="http://www.w3.org/2000/09/xmldsig#rsa-&#x73;ha1"/></a>`,
		"upper case":      `<a><SignatureMethod Algorithm="http://www.w3.org/2000/09/xmldsig#RSA-SHA1"/></a>`,
	}
	for name, body := range cases {
		if !usesSHA1(base64Of(body)) {
			t.Errorf("%s: the pre-check did not see the SHA-1 algorithm", name)
		}
	}
}

var algorithmAttrQuoted = regexp.MustCompile(`Algorithm="([^"]*)"`)

// TestASHA1SignatureWrittenWithSingleQuotesIsRefused checks that a SHA-1 signature written with single quotes is refused.
func TestASHA1SignatureWrittenWithSingleQuotesIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	encoded := signSHA1Response(t, idp, defaultOpts())

	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decoding the SHA-1 response: %v", err)
	}
	rewritten := algorithmAttrQuoted.ReplaceAllString(string(raw), "Algorithm='$1'")
	if !strings.Contains(rewritten, "Algorithm='") {
		t.Fatal("the fixture carries no double-quoted Algorithm attribute to rewrite")
	}
	encoded = base64.StdEncoding.EncodeToString([]byte(rewritten))

	if !usesSHA1(encoded) {
		t.Fatal("the pre-check did not see the single-quoted SHA-1 algorithm")
	}
	got := consume(t, p, row, encoded, "relay")
	if out := mustRefuse(t, got, sink); out != outcomeSignature {
		t.Errorf("outcome = %q, want %q", out, outcomeSignature)
	}
}

// TestAnAssertionWithNoIdentifierIsRefused checks that an assertion with no identifier is refused.
func TestAnAssertionWithNoIdentifierIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.assertionID = ""

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if out := mustRefuse(t, got, sink); out != outcomeOther {
		t.Errorf("outcome = %q, want %q", out, outcomeOther)
	}
}

// TestATransientReplayFailureIsAnOutage checks that a transient replay-line failure is an outage.
func TestATransientReplayFailureIsAnOutage(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	providers := newFakeProviders(row)
	providers.replayErr = errors.New("the replay store is unreachable")
	p, _ := newTestProduct(t, providers)

	got := consume(t, p, row, signResponse(t, idp, defaultOpts()), "relay")
	if step.KindOf(got) != step.KindUnavailable {
		t.Fatalf("answer = %s, want an outage", step.KindOf(got))
	}
	if !errors.Is(step.CauseOf(got), authn.ErrUnavailable) {
		t.Errorf("the cause %v does not wrap ErrUnavailable", step.CauseOf(got))
	}
}

// TestAnIdPInitiatedResponseThatAnswersARequestIsRefused checks that an IdP-initiated response that answers a request is refused.
func TestAnIdPInitiatedResponseThatAnswersARequestIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	row.AllowIDPInitiated = true
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.inResponseTo = "_stale-request"
	o.scdInResponse = ""

	st := methodState{Version: stateVersion, Provider: testProviderID}
	got := consumeWith(t, p, row, st, signResponse(t, idp, o), "")
	if out := mustRefuse(t, got, sink); out != outcomeInResponse {
		t.Errorf("outcome = %q, want %q", out, outcomeInResponse)
	}
}

// TestAnAssertionThatAnswersAnotherRequestIsRefused checks that an assertion answering another request is refused.
func TestAnAssertionThatAnswersAnotherRequestIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.inResponseTo = "_request-1"
	o.scdInResponse = "_other-request"

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if out := mustRefuse(t, got, sink); out != outcomeInResponse {
		t.Errorf("outcome = %q, want %q", out, outcomeInResponse)
	}
}

// TestAnIdPInitiatedAssertionOlderThanItsTightWindowIsRefused checks that an IdP-initiated assertion older than its tight window is refused.
func TestAnIdPInitiatedAssertionOlderThanItsTightWindowIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	row.AllowIDPInitiated = true
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.inResponseTo = ""
	o.scdInResponse = ""
	o.issueInstant = time.Now().UTC().Add(-4 * time.Minute)
	o.notBefore = o.issueInstant.Add(-time.Minute)

	st := methodState{Version: stateVersion, Provider: testProviderID}
	got := consumeWith(t, p, row, st, signResponse(t, idp, o), "")
	if out := mustRefuse(t, got, sink); out != outcomeTime {
		t.Errorf("outcome = %q, want %q", out, outcomeTime)
	}
}

// TestATamperedAssertionIsRefused checks that a tampered assertion is refused.
func TestATamperedAssertionIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	encoded := signResponse(t, idp, defaultOpts())

	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decoding the response: %v", err)
	}
	changed := strings.Replace(string(raw), ">alice<", ">mallory<", 1)
	if changed == string(raw) {
		t.Fatal("the fixture carries no subject to change")
	}
	got := consume(t, p, row, base64.StdEncoding.EncodeToString([]byte(changed)), "relay")
	if step.KindOf(got) != step.KindRefused {
		t.Fatalf("a tampered assertion was answered %s, want a refusal", step.KindOf(got))
	}
	_ = rejectOutcome(sink)
}

// TestAProxyRestrictionIsRefused checks that a proxy restriction is refused.
func TestAProxyRestrictionIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, sink := newTestProduct(t, newFakeProviders(row))
	o := defaultOpts()
	o.proxyRestriction = true

	got := consume(t, p, row, signResponse(t, idp, o), "relay")
	if out := mustRefuse(t, got, sink); out != outcomeAudience {
		t.Errorf("outcome = %q, want %q", out, outcomeAudience)
	}
}

// TestParseIDPMetadataRefusesADescriptorWithNoIDPDescriptor checks that metadata without an IdP descriptor is refused.
func TestParseIDPMetadataRefusesADescriptorWithNoIDPDescriptor(t *testing.T) {
	doc := `<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="https://sp.example.com">` +
		`<md:SPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol"/></md:EntityDescriptor>`
	if _, err := parseIDPMetadata([]byte(doc)); !errors.Is(err, errNoIDPDescriptor) {
		t.Errorf("parseIDPMetadata = %v, want errNoIDPDescriptor", err)
	}
}

// TestParseIDPMetadataRefusesMetadataWithNoCertificate checks that metadata without a certificate is refused.
func TestParseIDPMetadataRefusesMetadataWithNoCertificate(t *testing.T) {
	doc := `<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="https://idp.example.com">` +
		`<md:IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">` +
		`<md:SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="https://idp.example.com/sso"/>` +
		`</md:IDPSSODescriptor></md:EntityDescriptor>`
	if _, err := parseIDPMetadata([]byte(doc)); !errors.Is(err, errNoCertificate) {
		t.Errorf("parseIDPMetadata = %v, want errNoCertificate", err)
	}
}

// TestRefreshRefusesANonOKStatus checks that Refresh refuses a non-OK status.
func TestRefreshRefusesANonOKStatus(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, map[string]any{
		"metadata_url": "https://idp.example.com/metadata",
	}))
	providers := newFakeProviders(row)
	p, _ := newTestProduct(t, providers)
	p.client = staticClient(http.StatusInternalServerError, metadataDoc("https://idp.example.com/e", "https://idp.example.com/sso", idp.certDER))

	if _, err := p.Refresh(context.Background(), testProviderID); !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("Refresh = %v, want an ErrUnavailable-wrapped error", err)
	}
	if providers.puts != 0 {
		t.Errorf("Put was called %d times, want none", providers.puts)
	}
}

// TestRefreshRefusesANonSAMLProvider checks that Refresh refuses a non-SAML provider.
func TestRefreshRefusesANonSAMLProvider(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, map[string]any{
		"metadata_url": "https://idp.example.com/metadata",
	}))
	row.Protocol = "oidc"
	providers := newFakeProviders(row)
	p, _ := newTestProduct(t, providers)
	p.client = staticClient(http.StatusOK, metadataDoc("https://idp.example.com/e", "https://idp.example.com/sso", idp.certDER))

	if _, err := p.refresh(context.Background(), testProviderID); !errors.Is(err, errNotSAMLProvider) {
		t.Fatalf("refresh = %v, want errNotSAMLProvider", err)
	}
	if providers.puts != 0 {
		t.Errorf("Put was called %d times, want none", providers.puts)
	}
}

// TestBuildServiceProviderRefusesAnEmptyCertificateList checks that a service provider with no certificate is refused.
func TestBuildServiceProviderRefusesAnEmptyCertificateList(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, map[string]any{"idp_certs": []string{}}))
	p, _ := newTestProduct(t, newFakeProviders(row))

	if _, err := p.buildServiceProvider(row); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("buildServiceProvider = %v, want ErrInvalidConfig", err)
	}
}

// TestSPCacheRebuildsOnAVersionChangeAndOnExpiry checks that the service provider cache rebuilds on a version change and on expiry.
func TestSPCacheRebuildsOnAVersionChangeAndOnExpiry(t *testing.T) {
	now := time.Unix(0, 0)
	c := newSPCache()
	builds := 0
	row := authn.SSOProvider{ID: "idp-1", Version: 1}
	build := func() (built, error) { builds++; return built{version: row.Version}, nil }

	if _, err := c.get(row, time.Hour, now, build); err != nil {
		t.Fatalf("get: %v", err)
	}
	if builds != 1 {
		t.Fatalf("builds after the first get = %d, want 1", builds)
	}
	if _, err := c.get(row, time.Hour, now, build); err != nil {
		t.Fatalf("get: %v", err)
	}
	if builds != 1 {
		t.Fatalf("a same-version get within the TTL rebuilt: builds = %d", builds)
	}

	bumped := row
	bumped.Version = 2
	row = bumped
	if _, err := c.get(bumped, time.Hour, now, build); err != nil {
		t.Fatalf("get: %v", err)
	}
	if builds != 2 {
		t.Fatalf("a version change did not rebuild: builds = %d", builds)
	}
	if _, err := c.get(bumped, time.Hour, now.Add(2*time.Hour), build); err != nil {
		t.Fatalf("get: %v", err)
	}
	if builds != 3 {
		t.Fatalf("an entry past its TTL was not rebuilt: builds = %d", builds)
	}
}

// TestReadStateRefusesMalformedState checks that ReadState refuses malformed state.
func TestReadStateRefusesMalformedState(t *testing.T) {
	valid := writeState("_request-1", "relay", testProviderID)
	if _, ok := readState(valid); !ok {
		t.Fatal("the valid state did not read")
	}
	bad := map[string][]byte{
		"empty":          {},
		"bad version":    []byte(`{"v":999,"request":"r","relay":"rel","provider":"p"}`),
		"empty request":  writeState("", "relay", testProviderID),
		"empty relay":    writeState("_request-1", "", testProviderID),
		"empty provider": writeState("_request-1", "relay", ""),
		"trailing data":  append(append([]byte(nil), valid...), []byte("{}")...),
	}
	for name, priv := range bad {
		if _, ok := readState(priv); ok {
			t.Errorf("%s: readState accepted a malformed state", name)
		}
	}
}

// TestTheSAMLSettingsDefaultsAndDomains checks that the SAML settings declare the expected defaults and domains.
func TestTheSAMLSettingsDefaultsAndDomains(t *testing.T) {
	r := quietReader(nil)
	if got := r.assertionMaxAge(); got != 5*time.Minute {
		t.Errorf("assertionMaxAge = %s, want 5m0s", got)
	}
	if got := r.providerCacheTTL(); got != 60*time.Second {
		t.Errorf("providerCacheTTL = %s, want 1m0s", got)
	}
	if got := r.egressTimeout(); got != 10*time.Second {
		t.Errorf("egressTimeout = %s, want 10s", got)
	}

	domains := []struct {
		name string
		fn   func(time.Duration) bool
		in   []time.Duration
		out  []time.Duration
	}{
		{"assertionMaxAge", assertionMaxAgeInDomain, []time.Duration{time.Minute, 30 * time.Minute}, []time.Duration{59 * time.Second, 30*time.Minute + 1}},
		{"providerCache", providerCacheInDomain, []time.Duration{5 * time.Second, time.Hour}, []time.Duration{4 * time.Second, time.Hour + 1}},
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
