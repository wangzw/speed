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

// TestTheSHA1PreCheckSeesEveryXMLSpellingOfAnAlgorithm holds that the pre-check
// reads the parsed document and not the raw text, so every legal XML spelling of
// an attribute value — a single-quoted delimiter, a decimal or hexadecimal
// character reference, and an upper-case value — is seen as the SHA-1 algorithm
// it names.
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

// TestASHA1SignatureWrittenWithSingleQuotesIsRefused holds the design's claim
// that the pre-check is the one place a SHA-1 signature is turned away: a
// response whose Algorithm attributes are delimited with single quotes is the
// same document to the parser, so the pre-check has to refuse it before the
// library, which accepts SHA-1, ever runs.
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

// TestAnAssertionWithNoIdentifierIsRefused holds the replay line's guard: an
// assertion that names no identifier cannot be recorded, so it is refused rather
// than collapsed onto a single replay key with every other id-less assertion.
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

// TestATransientReplayFailureIsAnOutage holds the direction of the replay write:
// a failure that is not a replay is an outage, never an acceptance, so an
// assertion is not proven without being recorded.
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

// TestAnIdPInitiatedResponseThatAnswersARequestIsRefused holds that a response on
// the request-less path may not name a request at all: the flow drew none, so an
// envelope that names one did not come from this flow.
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

// TestAnAssertionThatAnswersAnotherRequestIsRefused holds the assertion-level
// binding: an envelope that matches the drawn request but whose subject
// confirmation answers another one is refused, so an assertion substituted within
// a valid envelope is turned away.
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

// TestAnIdPInitiatedAssertionOlderThanItsTightWindowIsRefused holds the shorter
// freshness bound of the request-less path: an assertion four minutes old is
// refused there even though it is inside the configured assertion-max-age.
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

// TestATamperedAssertionIsRefused holds the wrapping defence in the direction the
// design states it: the element the checks read is the element the library
// verified, so a change to the assertion after signing is refused rather than
// proven from an unverified element.
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

// TestAProxyRestrictionIsRefused holds that an assertion carrying a
// ProxyRestriction is refused, which the library reports and does not itself
// turn away.
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

// TestParseIDPMetadataRefusesADescriptorWithNoIDPDescriptor holds the guard that
// keeps a metadata document naming no identity provider descriptor from being
// dereferenced.
func TestParseIDPMetadataRefusesADescriptorWithNoIDPDescriptor(t *testing.T) {
	doc := `<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="https://sp.example.com">` +
		`<md:SPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol"/></md:EntityDescriptor>`
	if _, err := parseIDPMetadata([]byte(doc)); !errors.Is(err, errNoIDPDescriptor) {
		t.Errorf("parseIDPMetadata = %v, want errNoIDPDescriptor", err)
	}
}

// TestParseIDPMetadataRefusesMetadataWithNoCertificate holds the guard that stops
// a certificate-less document from overwriting a provider's trusted certificates
// with an empty list.
func TestParseIDPMetadataRefusesMetadataWithNoCertificate(t *testing.T) {
	doc := `<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="https://idp.example.com">` +
		`<md:IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">` +
		`<md:SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="https://idp.example.com/sso"/>` +
		`</md:IDPSSODescriptor></md:EntityDescriptor>`
	if _, err := parseIDPMetadata([]byte(doc)); !errors.Is(err, errNoCertificate) {
		t.Errorf("parseIDPMetadata = %v, want errNoCertificate", err)
	}
}

// TestRefreshRefusesANonOKStatus holds that a metadata endpoint that does not
// answer 200 is an outage, even when its body happens to parse as metadata.
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

// TestRefreshRefusesANonSAMLProvider holds the refresher's protocol guard: a
// provider that is not a SAML one is refused rather than overwritten with SAML
// keys.
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

// TestBuildServiceProviderRefusesAnEmptyCertificateList holds that a provider
// whose certificate trust store would be empty is refused as a bad configuration
// rather than built.
func TestBuildServiceProviderRefusesAnEmptyCertificateList(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, map[string]any{"idp_certs": []string{}}))
	p, _ := newTestProduct(t, newFakeProviders(row))

	if _, err := p.buildServiceProvider(row); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("buildServiceProvider = %v, want ErrInvalidConfig", err)
	}
}

// TestSPCacheRebuildsOnAVersionChangeAndOnExpiry holds the cache invalidation:
// an entry is reused only while both its version matches the row's and its
// lifetime has not run out.
func TestSPCacheRebuildsOnAVersionChangeAndOnExpiry(t *testing.T) {
	now := time.Unix(0, 0)
	c := newSPCache()
	builds := 0
	row := authn.SSOProvider{ID: "idp-1", Version: 1}
	// The real build stamps the row's version on the built value; the fake does
	// the same, so the version comparison the cache makes is exercised.
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

// TestReadStateRefusesMalformedState holds the private state's integrity check: a
// wrong version, an empty field and trailing data all name no drawn flow.
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

// TestTheSAMLSettingsDefaultsAndDomains holds the declared defaults against
// literal values and every settings domain at its bounds, so a changed default
// or bound is caught.
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
