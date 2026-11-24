package saml

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
)

const (
	testProviderID = "idp-1"
	testTenant     = "acme"
	testPublicURL  = "https://sp.example.com"
	testBasePath   = "/api/v1/auth"
	testIssuer     = "https://idp.example.com/entity"
	testSSOURL     = "https://idp.example.com/sso"
)

type idp struct {
	key     *rsa.PrivateKey
	certDER []byte
	certPEM string
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating the identity provider key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * 365 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating the identity provider certificate: %v", err)
	}
	return &idp{
		key:     key,
		certDER: der,
		certPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
	}
}

func (i *idp) signingContext(t *testing.T) *dsig.SigningContext {
	t.Helper()
	ctx, err := dsig.NewSigningContext(i.key, [][]byte{i.certDER})
	if err != nil {
		t.Fatalf("building the signing context: %v", err)
	}
	return ctx
}

type responseOpts struct {
	issuer            string
	assertionIssuer   string
	destination       string
	inResponseTo      string
	assertionID       string
	scdInResponse     string
	scdRecipient      string
	scdNotOnOrAfter   time.Time
	issueInstant      time.Time
	notBefore         time.Time
	notOnOrAfter      time.Time
	audience          string
	omitAudience      bool
	noAudienceListing bool
	username          string
	nameIDFormat      string
	attributes        map[string]string
	acrValue          string
	extraAssertion    bool
	encrypted         bool
	proxyRestriction  bool
	status            string
}

func defaultOpts() responseOpts {
	now := time.Now().UTC()
	return responseOpts{
		issuer:          testIssuer,
		destination:     testACS(),
		inResponseTo:    "_request-1",
		assertionID:     "_assertion-1",
		scdInResponse:   "_request-1",
		scdRecipient:    testACS(),
		scdNotOnOrAfter: now.Add(5 * time.Minute),
		issueInstant:    now,
		notBefore:       now.Add(-time.Minute),
		notOnOrAfter:    now.Add(5 * time.Minute),
		audience:        testEntityID(),
		username:        "alice",
	}
}

func testEntityID() string {
	return testPublicURL + testBasePath + "/sso/saml/" + testProviderID + "/metadata"
}

func testACS() string { return testPublicURL + testBasePath + "/callback/" + testProviderID }

func buildResponse(o responseOpts) string {
	var attrs strings.Builder
	for name, value := range o.attributes {
		fmt.Fprintf(&attrs, `<saml:Attribute Name="%s"><saml:AttributeValue>%s</saml:AttributeValue></saml:Attribute>`, name, value)
	}
	attributeStatement := ""
	if attrs.Len() > 0 {
		attributeStatement = "<saml:AttributeStatement>" + attrs.String() + "</saml:AttributeStatement>"
	}
	status := o.status
	if status == "" {
		status = "urn:oasis:names:tc:SAML:2.0:status:Success"
	}
	var scd strings.Builder
	if o.scdRecipient != "" || !o.scdNotOnOrAfter.IsZero() || o.scdInResponse != "" {
		scd.WriteString(`<saml:SubjectConfirmationData`)
		if o.scdInResponse != "" {
			fmt.Fprintf(&scd, ` InResponseTo="%s"`, o.scdInResponse)
		}
		if !o.scdNotOnOrAfter.IsZero() {
			fmt.Fprintf(&scd, ` NotOnOrAfter="%s"`, formatTime(o.scdNotOnOrAfter))
		}
		if o.scdRecipient != "" {
			fmt.Fprintf(&scd, ` Recipient="%s"`, o.scdRecipient)
		}
		scd.WriteString(`/>`)
	}
	conditions := ""
	switch {
	case o.omitAudience:
	case o.noAudienceListing:
		conditions = fmt.Sprintf(`<saml:Conditions NotBefore="%s" NotOnOrAfter="%s"/>`,
			formatTime(o.notBefore), formatTime(o.notOnOrAfter))
	default:
		proxy := ""
		if o.proxyRestriction {
			proxy = `<saml:ProxyRestriction Count="1"/>`
		}
		conditions = fmt.Sprintf(`<saml:Conditions NotBefore="%s" NotOnOrAfter="%s"><saml:AudienceRestriction><saml:Audience>%s</saml:Audience></saml:AudienceRestriction>%s</saml:Conditions>`,
			formatTime(o.notBefore), formatTime(o.notOnOrAfter), o.audience, proxy)
	}
	nameID := ""
	if o.username != "" {
		nameID = fmt.Sprintf(`<saml:NameID Format="%s">%s</saml:NameID>`, o.nameIDFormat, o.username)
	}
	authn := ""
	if o.acrValue != "" {
		authn = fmt.Sprintf(`<saml:AuthnStatement><saml:AuthnContext><saml:AuthnContextClassRef>%s</saml:AuthnContextClassRef></saml:AuthnContext></saml:AuthnStatement>`, o.acrValue)
	}
	assertionIssuer := o.assertionIssuer
	if assertionIssuer == "" {
		assertionIssuer = o.issuer
	}
	assertion := fmt.Sprintf(`<saml:Assertion ID="%s" IssueInstant="%s" Version="2.0"><saml:Issuer>%s</saml:Issuer><saml:Subject>%s<saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer">%s</saml:SubjectConfirmation></saml:Subject>%s%s%s</saml:Assertion>`,
		o.assertionID, formatTime(o.issueInstant), assertionIssuer, nameID, scd.String(), conditions, attributeStatement, authn)
	body := assertion
	if o.extraAssertion {
		body += assertion
	}
	if o.encrypted {
		body += `<saml:EncryptedAssertion/>`
	}
	return fmt.Sprintf(`<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" Destination="%s" ID="_response-1" InResponseTo="%s" IssueInstant="%s" Version="2.0"><saml:Issuer>%s</saml:Issuer><samlp:Status><samlp:StatusCode Value="%s"/></samlp:Status>%s</samlp:Response>`,
		o.destination, o.inResponseTo, formatTime(o.issueInstant), o.issuer, status, body)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func signResponse(t *testing.T, i *idp, o responseOpts) string {
	t.Helper()
	signed, err := i.signingContext(t).SignEnveloped(readDoc(t, buildResponse(o)).Root())
	if err != nil {
		t.Fatalf("signing the response: %v", err)
	}
	return encodeDoc(t, signed)
}

func readDoc(t *testing.T, text string) *etree.Document {
	t.Helper()
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes([]byte(text)); err != nil {
		t.Fatalf("parsing XML: %v", err)
	}
	return doc
}

func encodeDoc(t *testing.T, el *etree.Element) string {
	t.Helper()
	out := etree.NewDocument()
	out.SetRoot(el)
	out.WriteSettings = etree.WriteSettings{CanonicalAttrVal: true, CanonicalEndTags: true, CanonicalText: true}
	text, err := out.WriteToString()
	if err != nil {
		t.Fatalf("writing XML: %v", err)
	}
	return base64.StdEncoding.EncodeToString([]byte(text))
}

func stripSignature(t *testing.T, encoded string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decoding the response: %v", err)
	}
	doc := readDoc(t, string(raw))
	for _, sig := range doc.Root().FindElements("//Signature") {
		sig.Parent().RemoveChild(sig)
	}
	return encodeDoc(t, doc.Root())
}

func base64Of(text string) string { return base64.StdEncoding.EncodeToString([]byte(text)) }

func providerConfigJSON(t *testing.T, i *idp, extra map[string]any) json.RawMessage {
	t.Helper()
	cfg := map[string]any{
		"idp_entity_id": testIssuer,
		"idp_sso_url":   testSSOURL,
		"idp_certs":     []string{i.certPEM},
	}
	for k, v := range extra {
		cfg[k] = v
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("encoding the provider config: %v", err)
	}
	return out
}

func providerRow(cfg json.RawMessage) authn.SSOProvider {
	return authn.SSOProvider{
		ID:       testProviderID,
		Tenant:   testTenant,
		Protocol: providerProtocol,
		Name:     "Acme IdP",
		Enabled:  true,
		Config:   cfg,
		Version:  1,
	}
}

type fakeProviders struct {
	row       authn.SSOProvider
	domain    []authn.SSOProvider
	seen      map[string]bool
	replay    bool
	replayErr error
	getErr    error
	puts      int
	lastPut   authn.SSOProvider
}

func newFakeProviders(row authn.SSOProvider) *fakeProviders {
	return &fakeProviders{row: row, seen: map[string]bool{}}
}

func (f *fakeProviders) Get(_ context.Context, id string) (authn.SSOProvider, error) {
	if f.getErr != nil {
		return authn.SSOProvider{}, f.getErr
	}
	if id != f.row.ID {
		return authn.SSOProvider{}, authn.ErrProviderNotFound
	}
	return f.row.Clone(), nil
}

func (f *fakeProviders) Secret(context.Context, string) (string, error) { return "", nil }
func (f *fakeProviders) List(context.Context, string) ([]authn.SSOProvider, error) {
	return []authn.SSOProvider{f.row}, nil
}
func (f *fakeProviders) Tenant(context.Context, string) (string, error) { return testTenant, nil }
func (f *fakeProviders) Discover(context.Context, string) ([]authn.SSOProvider, error) {
	return f.domain, nil
}

func (f *fakeProviders) Put(_ context.Context, in authn.SSOProvider, _ *string) (authn.SSOProvider, error) {
	f.puts++
	f.lastPut = in
	return in, nil
}
func (f *fakeProviders) Delete(context.Context, string) error { return nil }
func (f *fakeProviders) ClaimDomain(context.Context, string, string) (string, error) {
	return "", nil
}
func (f *fakeProviders) VerifyDomain(context.Context, string, string) error { return nil }
func (f *fakeProviders) RemoveDomain(context.Context, string, string) error { return nil }
func (f *fakeProviders) ReverifyDomains(context.Context, string) error      { return nil }

// RememberAssertion records an identifier once; a second call is ErrAssertionReplay.
func (f *fakeProviders) RememberAssertion(_ context.Context, idpID, assertionID string, _ time.Time) error {
	if f.replayErr != nil {
		return f.replayErr
	}
	if f.replay {
		return authn.ErrAssertionReplay
	}
	key := idpID + "/" + assertionID
	if f.seen[key] {
		return authn.ErrAssertionReplay
	}
	f.seen[key] = true
	return nil
}

type fakeAudit struct {
	entries []audit.Entry
}

func (f *fakeAudit) Record(_ context.Context, e audit.Entry) error {
	f.entries = append(f.entries, e)
	return nil
}

func newTestProduct(t *testing.T, providers *fakeProviders) (*product, *fakeAudit) {
	t.Helper()
	return newTestProductAtBase(t, providers, testBasePath)
}

func newTestProductAtBase(t *testing.T, providers *fakeProviders, base string) (*product, *fakeAudit) {
	t.Helper()
	m := mounttest.New(t, mounttest.WithPublicURL(testPublicURL), mounttest.WithBase(base))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sink := &fakeAudit{}
	return &product{
		providers:  providers,
		mounter:    m,
		assertions: providers,
		audit:      sink,
		logger:     logger,
		settings:   newReader(nil, logger),
		cache:      newSPCache(),
	}, sink
}

func rejectOutcome(sink *fakeAudit) string {
	if len(sink.entries) == 0 {
		return ""
	}
	return sink.entries[len(sink.entries)-1].Outcome
}

func consume(t *testing.T, p *product, row authn.SSOProvider, encoded, relay string) authn.Step {
	t.Helper()
	b, err := p.buildServiceProvider(row)
	if err != nil {
		t.Fatalf("building the service provider: %v", err)
	}
	st := methodState{Version: stateVersion, Request: "_request-1", Relay: relay, Provider: row.ID}
	return p.consume(context.Background(), row, b, st, encoded, relay)
}
