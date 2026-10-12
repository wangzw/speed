package saml

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/event"
)

// generateSPKeys builds a service provider key and a current and next
// certificate, as the deployment configuration carries them.
func generateSPKeys(t *testing.T) resolved {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating the service provider key: %v", err)
	}
	return resolved{signer: key, certDER: selfSignedDER(t, key), nextDER: selfSignedDER(t, key)}
}

func selfSignedDER(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * 365 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating the service provider certificate: %v", err)
	}
	return der
}

func TestTheMetadataRoutePublishesTheEntityAndBothCertificates(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, _ := newTestProduct(t, newFakeProviders(row))
	p.sp = generateSPKeys(t)
	p.mountMetadata()

	m := p.mounter.(*mounttest.Mounter)
	req := httptest.NewRequest(http.MethodGet, testBasePath+"/sso/saml/"+testProviderID+"/metadata", nil)
	rec := m.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("metadata answered %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, testEntityID()) {
		t.Errorf("the metadata does not name the entity identifier %q: %s", testEntityID(), body)
	}
	if !strings.Contains(body, testACS()) {
		t.Errorf("the metadata does not name the ACS %q", testACS())
	}
	if !strings.Contains(body, `WantAssertionsSigned="true"`) {
		t.Errorf("the metadata does not want assertions signed")
	}
	if n := strings.Count(body, `use="signing"`); n != 2 {
		t.Errorf("the metadata carries %d signing key descriptors, want the current and next certificates", n)
	}
	if strings.Contains(body, "localhost") || strings.Contains(body, "Host") {
		t.Errorf("the metadata was derived from something other than the configured public address")
	}
}

func TestTheMetadataRouteIsNotFoundForAProviderThatDoesNotAnswer(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	row.Enabled = false
	p, _ := newTestProduct(t, newFakeProviders(row))
	p.sp = generateSPKeys(t)
	p.mountMetadata()

	m := p.mounter.(*mounttest.Mounter)
	req := httptest.NewRequest(http.MethodGet, testBasePath+"/sso/saml/"+testProviderID+"/metadata", nil)
	if rec := m.Do(req); rec.Code != http.StatusNotFound {
		t.Errorf("metadata for a disabled provider answered %d, want 404", rec.Code)
	}
}

func TestTheMetadataRouteAnswersTheRegisterFailure(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	providers := newFakeProviders(row)
	providers.getErr = errors.New("the database is down")
	p, _ := newTestProduct(t, providers)
	p.sp = generateSPKeys(t)
	p.mountMetadata()

	m := p.mounter.(*mounttest.Mounter)
	req := httptest.NewRequest(http.MethodGet, testBasePath+"/sso/saml/"+testProviderID+"/metadata", nil)
	if rec := m.Do(req); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("metadata under a register outage answered %d, want 503", rec.Code)
	}
}

// roundTripFunc adapts a function to an http.RoundTripper, so a test can serve a
// fixed metadata document.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func staticClient(status int, body string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{},
		}, nil
	})}
}

// metadataDoc builds an identity provider's entity descriptor naming an entity
// identifier, a single sign-on URL and one signing certificate.
func metadataDoc(entityID, ssoURL string, certDER []byte) string {
	return fmt.Sprintf(`<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="%s"><md:IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol"><md:KeyDescriptor use="signing"><ds:KeyInfo xmlns:ds="http://www.w3.org/2000/09/xmldsig#"><ds:X509Data><ds:X509Certificate>%s</ds:X509Certificate></ds:X509Data></ds:KeyInfo></md:KeyDescriptor><md:SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="%s"/></md:IDPSSODescriptor></md:EntityDescriptor>`,
		entityID, base64.StdEncoding.EncodeToString(certDER), ssoURL)
}

func TestRefreshParsesTheMetadataAndWritesItBack(t *testing.T) {
	idp := newIDP(t)
	const newEntity = "https://idp.example.com/new-entity"
	const newSSO = "https://idp.example.com/new-sso"
	row := providerRow(providerConfigJSON(t, idp, map[string]any{
		"metadata_url": "https://idp.example.com/metadata",
	}))
	providers := newFakeProviders(row)
	p, _ := newTestProduct(t, providers)
	p.client = staticClient(http.StatusOK, metadataDoc(newEntity, newSSO, idp.certDER))

	if _, err := p.Refresh(context.Background(), testProviderID); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if providers.puts != 1 {
		t.Fatalf("Put was called %d times, want once", providers.puts)
	}
	cfg, err := parseProviderConfig(providers.lastPut.Config)
	if err != nil {
		t.Fatalf("the written config does not decode: %v", err)
	}
	if cfg.IDPEntityID != newEntity || cfg.IDPSSOURL != newSSO {
		t.Errorf("the written config = %+v, want the refreshed entity and SSO URL", cfg)
	}
	if len(cfg.IDPCerts) != 1 || !strings.Contains(cfg.IDPCerts[0], "CERTIFICATE") {
		t.Errorf("the written certificates = %v, want the metadata's certificate", cfg.IDPCerts)
	}
}

func TestRefreshKeepsTheStoredConfigWhenTheMetadataCannotBeParsed(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, map[string]any{
		"metadata_url": "https://idp.example.com/metadata",
	}))
	providers := newFakeProviders(row)
	p, _ := newTestProduct(t, providers)
	p.client = staticClient(http.StatusOK, "not a metadata document")

	if _, err := p.Refresh(context.Background(), testProviderID); err == nil {
		t.Fatal("Refresh accepted a document that is not metadata")
	}
	if providers.puts != 0 {
		t.Errorf("Put was called %d times, want none: a failed refresh changes nothing", providers.puts)
	}
	if string(providers.row.Config) == "" {
		t.Error("the stored config was cleared")
	}
}

func TestRefreshWithoutAMetadataURLIsRefused(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	providers := newFakeProviders(row)
	p, _ := newTestProduct(t, providers)
	p.client = staticClient(http.StatusOK, "")

	if _, err := p.Refresh(context.Background(), testProviderID); !errors.Is(err, errNoMetadataURL) {
		t.Fatalf("Refresh = %v, want errNoMetadataURL", err)
	}
}

func TestAProviderChangeDropsTheCachedServiceProvider(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	providers := newFakeProviders(row)
	p, _ := newTestProduct(t, providers)

	if _, err := p.serviceProvider(context.Background(), row); err != nil {
		t.Fatalf("building the service provider: %v", err)
	}
	if len(p.cache.m) != 1 {
		t.Fatalf("the cache holds %d entries, want one", len(p.cache.m))
	}
	if err := p.onIDPChanged(context.Background(), event.Event{Payload: authn.IDPChanged{IDPID: testProviderID}}); err != nil {
		t.Fatalf("onIDPChanged: %v", err)
	}
	if len(p.cache.m) != 0 {
		t.Errorf("the cache still holds %d entries after the provider changed", len(p.cache.m))
	}
}

func TestAProviderChangeWithAnUnreadablePayloadDropsEverything(t *testing.T) {
	idp := newIDP(t)
	row := providerRow(providerConfigJSON(t, idp, nil))
	p, _ := newTestProduct(t, newFakeProviders(row))
	if _, err := p.serviceProvider(context.Background(), row); err != nil {
		t.Fatalf("building the service provider: %v", err)
	}
	if err := p.onIDPChanged(context.Background(), event.Event{Payload: nil}); err != nil {
		t.Fatalf("onIDPChanged: %v", err)
	}
	if len(p.cache.m) != 0 {
		t.Errorf("the cache still holds %d entries after an unreadable change", len(p.cache.m))
	}
}
