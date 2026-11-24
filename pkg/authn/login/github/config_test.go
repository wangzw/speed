package github

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2/endpoints"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

type gateDefaults struct {
	Mount struct {
		PublicURL string `config:"public-url"`
		Bases     struct {
			Default string `config:"default"`
		} `config:"bases"`
	} `config:"mount"`
}

func gateStandIn() core.Module {
	d := &gateDefaults{}
	d.Mount.Bases.Default = "/api/v1/auth"
	return core.Module{
		Name:      "standin.gate",
		Resources: []any{config.Schema{Namespace: gateNamespace, Mounts: []config.Mount{{Value: d}}}},
	}
}

func readerOver(t *testing.T, body string) config.Reader {
	t.Helper()
	return authntest.ConfigReaderAll(t, body, Module(), gateStandIn())
}

// TestTheSchemaCarriesTheDesignsFlags holds the schema: both credentials are
// required and the secret is sensitive and described.
func TestTheSchemaCarriesTheDesignsFlags(t *testing.T) {
	s := schema()
	if s.Namespace != ConfigNamespace {
		t.Errorf("Namespace = %q, want %q", s.Namespace, ConfigNamespace)
	}
	if !s.Items[clientIDItem].Required {
		t.Error("client-id is not required")
	}
	secret := s.Items[clientSecretItem]
	if !secret.Required || !secret.Sensitive || secret.Description == "" {
		t.Errorf("client-secret = %+v, want required, sensitive and described", secret)
	}
	for _, key := range []string{authorizeURLItem, tokenURLItem, apiBaseItem, allowLoopbackItem, rootCAFileItem} {
		item := s.Items[key]
		if item.Required {
			t.Errorf("%s is marked required", key)
		}
		if item.Description == "" {
			t.Errorf("%s carries no description", key)
		}
	}
}

// TestTheReaderRefusesAMissingCredential holds that a required credential no source gave stops startup.
func TestTheReaderRefusesAMissingCredential(t *testing.T) {
	body := `{"authn":{"gate":{"mount":{"public-url":"https://app.example"}},` +
		`"login":{"github":{"client-secret":"the-secret"}}}}`
	_, err := readConfig(readerOver(t, body))
	if !errors.Is(err, config.ErrMissingRequired) {
		t.Fatalf("reading a section with no client-id gave %v, want one wrapping ErrMissingRequired", err)
	}
}

// TestTheReaderServesACompleteSection holds that a complete section resolves the callback address, the GitHub endpoints and the API base.
func TestTheReaderServesACompleteSection(t *testing.T) {
	body := `{"authn":{"gate":{"mount":{"public-url":"https://app.example"}},` +
		`"login":{"github":{"client-id":"the-id","client-secret":"the-secret"}}}}`
	got, err := readConfig(readerOver(t, body))
	if err != nil {
		t.Fatalf("reading a complete section: %v", err)
	}
	if got.clientID != "the-id" || got.clientSecret != "the-secret" {
		t.Errorf("resolved credentials = %q/%q, want the configured ones", got.clientID, got.clientSecret)
	}
	if got.redirectURI != "https://app.example/api/v1/auth/callback/github" {
		t.Errorf("redirectURI = %q, want the public base, the default mount path and the callback route",
			got.redirectURI)
	}
	if got.endpoint != endpoints.GitHub {
		t.Errorf("endpoint = %+v, want the GitHub endpoint", got.endpoint)
	}
	if got.apiBase != "https://api.github.com" {
		t.Errorf("apiBase = %q, want https://api.github.com", got.apiBase)
	}
}

// TestAHostMountPathIsUsedInTheRedirect holds that a configured mount path moves
// the callback route with it.
func TestAHostMountPathIsUsedInTheRedirect(t *testing.T) {
	body := `{"authn":{"gate":{"mount":{"public-url":"https://app.example","bases":{"default":"/sso"}}},` +
		`"login":{"github":{"client-id":"the-id","client-secret":"the-secret"}}}}`
	got, err := readConfig(readerOver(t, body))
	if err != nil {
		t.Fatalf("reading a complete section: %v", err)
	}
	if got.redirectURI != "https://app.example/sso/callback/github" {
		t.Errorf("redirectURI = %q, want the configured mount path in the callback address", got.redirectURI)
	}
}

// TestAnEmptyPublicBaseIsRefused holds that a missing public base fails startup rather than being guessed from a request.
func TestAnEmptyPublicBaseIsRefused(t *testing.T) {
	body := `{"authn":{"login":{"github":{"client-id":"the-id","client-secret":"the-secret"}}}}`
	_, err := readConfig(readerOver(t, body))
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("reading a section with no public base gave %v, want one wrapping ErrInvalidConfig", err)
	}
}

type stubReader struct {
	cfg     moduleConfig
	gate    gateConfig
	gateErr error
}

func (s stubReader) Decode(path string, target any) error {
	switch path {
	case ConfigNamespace:
		*target.(*moduleConfig) = s.cfg
	case gateNamespace:
		if s.gateErr != nil {
			return s.gateErr
		}
		*target.(*gateConfig) = s.gate
	}
	return nil
}

// TestAnEmptyMountPathIsRefused holds that a configuration with a public base but
// no mount path is a startup failure: the callback path is built from it.
func TestAnEmptyMountPathIsRefused(t *testing.T) {
	gate := gateConfig{}
	gate.Mount.PublicURL = "https://app.example"
	_, err := readConfig(stubReader{cfg: moduleConfig{ClientID: "the-id", ClientSecret: "the-secret"}, gate: gate})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("an empty mount path gave %v, want one wrapping ErrInvalidConfig", err)
	}
}

// TestAGateDecodeErrorIsRefused holds that an unreadable gateway section fails the read rather than half-configuring the method.
func TestAGateDecodeErrorIsRefused(t *testing.T) {
	boom := errors.New("the gateway section cannot be read")
	_, err := readConfig(stubReader{cfg: moduleConfig{ClientID: "the-id", ClientSecret: "the-secret"}, gateErr: boom})
	if !errors.Is(err, boom) {
		t.Fatalf("a gateway decode error gave %v, want it propagated", err)
	}
}

func githubBody(extra string) string {
	items := `"client-id":"the-id","client-secret":"the-secret"`
	if extra != "" {
		items += "," + extra
	}
	return `{"authn":{"gate":{"mount":{"public-url":"https://app.example"}},` +
		`"login":{"github":{` + items + `}}}}`
}

func writeRootCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "the test root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating a certificate: %v", err)
	}
	path := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("writing the root CA file: %v", err)
	}
	return path
}

// TestTheReaderAcceptsTheEndpointOverrides drives the real loader: the three
// endpoint items are accepted and replace the built-in GitHub addresses.
func TestTheReaderAcceptsTheEndpointOverrides(t *testing.T) {
	body := githubBody(`"authorize-url":"https://idp.example/authorize",` +
		`"token-url":"https://idp.example/token","api-base":"https://idp.example/api/v3"`)
	got, err := readConfig(readerOver(t, body))
	if err != nil {
		t.Fatalf("reading a section with endpoint overrides: %v", err)
	}
	if got.endpoint.AuthURL != "https://idp.example/authorize" {
		t.Errorf("authorize URL = %q, want the configured one", got.endpoint.AuthURL)
	}
	if got.endpoint.TokenURL != "https://idp.example/token" {
		t.Errorf("token URL = %q, want the configured one", got.endpoint.TokenURL)
	}
	if got.apiBase != "https://idp.example/api/v3" {
		t.Errorf("apiBase = %q, want the configured one", got.apiBase)
	}
}

// TestATrailingSlashOnTheAPIBaseIsRemoved holds that the configured base is normalized so joined paths do not double the slash.
func TestATrailingSlashOnTheAPIBaseIsRemoved(t *testing.T) {
	body := githubBody(`"api-base":"https://idp.example/api/v3/"`)
	got, err := readConfig(readerOver(t, body))
	if err != nil {
		t.Fatalf("reading a section with a trailing slash: %v", err)
	}
	if got.apiBase != "https://idp.example/api/v3" {
		t.Errorf("apiBase = %q, want the trailing slash removed", got.apiBase)
	}
}

// TestTheReaderAcceptsTheEgressSeamKeys holds that the loopback switch and the
// root CA file are items this module accepts.
func TestTheReaderAcceptsTheEgressSeamKeys(t *testing.T) {
	body := githubBody(fmt.Sprintf(`"allow-loopback":true,"root-ca-file":%q`, writeRootCA(t)))
	if _, err := readConfig(readerOver(t, body)); err != nil {
		t.Fatalf("reading a section with the egress seam: %v", err)
	}
}

// TestTheReaderRefusesAnUnreadableRootCAFile holds that an unreadable root CA file fails the start instead of silently using the system roots.
func TestTheReaderRefusesAnUnreadableRootCAFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.pem")
	_, err := readConfig(readerOver(t, githubBody(fmt.Sprintf(`"root-ca-file":%q`, missing))))
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("an unreadable root CA file gave %v, want one wrapping ErrInvalidConfig", err)
	}
}

// TestTheEndpointOverridesMustBeHTTPS holds that every endpoint override is absolute https.
func TestTheEndpointOverridesMustBeHTTPS(t *testing.T) {
	cases := []struct {
		key   string
		value string
	}{
		{key: "authorize-url", value: "http://idp.example/authorize"},
		{key: "token-url", value: "ftp://idp.example/token"},
		{key: "api-base", value: "https://"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			_, err := readConfig(readerOver(t, githubBody(fmt.Sprintf(`%q:%q`, tc.key, tc.value))))
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("%s %q gave %v, want one wrapping ErrInvalidConfig", tc.key, tc.value, err)
			}
		})
	}
}
