package google

import (
	"context"
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

func googleBody(extra string) string {
	items := `"client-id":"the-id","client-secret":"the-secret"`
	if extra != "" {
		items += "," + extra
	}
	return `{"authn":{"gate":{"mount":{"public-url":"https://app.example"}},` +
		`"login":{"google":{` + items + `}}}}`
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

// TestTheReaderAcceptsTheConfiguredIssuer drives the real loader: the issuer is
// an item this module accepts, and the value reaches the provider cache.
func TestTheReaderAcceptsTheConfiguredIssuer(t *testing.T) {
	reader := readerOver(t, googleBody(`"issuer":"https://idp.example"`))
	var built *product
	if err := driveWith(t, reader, func(_ context.Context, reg *core.Registry) error {
		for _, m := range core.ResolveAll[authn.LoginMethod](reg) {
			if p, ok := m.(*product); ok {
				built = p
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("drive: %v", err)
	}
	if built == nil {
		t.Fatal("the registry delivered no product")
	}
	if built.provider.issuer != "https://idp.example" {
		t.Errorf("the provider cache issuer = %q, want the configured issuer", built.provider.issuer)
	}
}

// TestTheReaderAcceptsTheLoopbackSwitch holds that the loopback switch is an
// item this module accepts.
func TestTheReaderAcceptsTheLoopbackSwitch(t *testing.T) {
	if _, err := readConfig(readerOver(t, googleBody(`"allow-loopback":true`))); err != nil {
		t.Fatalf("reading a section with the loopback switch: %v", err)
	}
}

// TestTheReaderAcceptsTheRootCAFile holds that a readable root CA file is an
// item this module accepts.
func TestTheReaderAcceptsTheRootCAFile(t *testing.T) {
	if _, err := readConfig(readerOver(t, googleBody(fmt.Sprintf(`"root-ca-file":%q`, writeRootCA(t))))); err != nil {
		t.Fatalf("reading a section with a root CA file: %v", err)
	}
}

// TestTheReaderRefusesAnUnreadableRootCAFile holds that an unreadable root CA file fails the start instead of silently using the system roots.
func TestTheReaderRefusesAnUnreadableRootCAFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.pem")
	_, err := readConfig(readerOver(t, googleBody(fmt.Sprintf(`"root-ca-file":%q`, missing))))
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("an unreadable root CA file gave %v, want one wrapping ErrInvalidConfig", err)
	}
}

// TestAnExplicitIssuerMustBeHTTPS holds the issuer's scheme rule: https in
// general, and a loopback http issuer only while the loopback switch is on.
func TestAnExplicitIssuerMustBeHTTPS(t *testing.T) {
	cases := []struct {
		name     string
		issuer   string
		loopback bool
		wantErr  bool
	}{
		{name: "an https issuer", issuer: "https://idp.example", wantErr: false},
		{name: "an http issuer", issuer: "http://idp.example", wantErr: true},
		{name: "an http loopback issuer without the switch", issuer: "http://127.0.0.1:8080", wantErr: true},
		{name: "an http loopback issuer with the switch", issuer: "http://127.0.0.1:8080", loopback: true, wantErr: false},
		{name: "an https loopback issuer with the switch", issuer: "https://127.0.0.1:8080", loopback: true, wantErr: false},
		{name: "another scheme", issuer: "ftp://idp.example", wantErr: true},
		{name: "no host", issuer: "https://", wantErr: true},
		{name: "blank falls back to the default", issuer: "  ", wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := googleBody(fmt.Sprintf(`"issuer":%q,"allow-loopback":%t`, tc.issuer, tc.loopback))
			_, err := readConfig(readerOver(t, body))
			if tc.wantErr {
				if !errors.Is(err, authn.ErrInvalidConfig) {
					t.Fatalf("issuer %q with loopback %t gave %v, want one wrapping ErrInvalidConfig",
						tc.issuer, tc.loopback, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("issuer %q with loopback %t: %v", tc.issuer, tc.loopback, err)
			}
		})
	}
}

// TestResolveBuildsTheRedirectTarget holds the shape of the redirect URI: the
// public address, the base path and this provider's callback path.
func TestResolveBuildsTheRedirectTarget(t *testing.T) {
	cases := []struct {
		name string
		cfg  moduleConfig
		gm   gateMount
		want string
	}{
		{
			name: "the default base path",
			cfg:  moduleConfig{ClientID: "id", ClientSecret: "secret"},
			gm:   gateMount{PublicURL: "https://app.example.com"},
			want: "https://app.example.com/api/v1/auth/callback/google",
		},
		{
			name: "a configured base path",
			cfg:  moduleConfig{ClientID: "id", ClientSecret: "secret"},
			gm: gateMount{PublicURL: "https://app.example.com", Bases: struct {
				Default string `config:"default"`
			}{Default: "/identity"}},
			want: "https://app.example.com/identity/callback/google",
		},
		{
			name: "a trailing slash on the address",
			cfg:  moduleConfig{ClientID: "id", ClientSecret: "secret"},
			gm: gateMount{PublicURL: "https://app.example.com/", Bases: struct {
				Default string `config:"default"`
			}{Default: "/api/v1/auth/"}},
			want: "https://app.example.com/api/v1/auth/callback/google",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.cfg.resolve(tc.gm)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got.redirectURL != tc.want {
				t.Errorf("redirectURL = %q, want %q", got.redirectURL, tc.want)
			}
		})
	}
}

// TestResolveRefusesAnIncompleteConfiguration holds that a missing identifier, secret or public address fails the start rather than reading as absence.
func TestResolveRefusesAnIncompleteConfiguration(t *testing.T) {
	cases := []struct {
		name string
		cfg  moduleConfig
		gm   gateMount
	}{
		{"no client id", moduleConfig{ClientSecret: "secret"}, gateMount{PublicURL: "https://app.example.com"}},
		{"a blank client id", moduleConfig{ClientID: "  ", ClientSecret: "secret"}, gateMount{PublicURL: "https://app.example.com"}},
		{"no secret", moduleConfig{ClientID: "id"}, gateMount{PublicURL: "https://app.example.com"}},
		{"a blank secret", moduleConfig{ClientID: "id", ClientSecret: "  "}, gateMount{PublicURL: "https://app.example.com"}},
		{"no public address", moduleConfig{ClientID: "id", ClientSecret: "secret"}, gateMount{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.cfg.resolve(tc.gm); !errors.Is(err, authn.ErrInvalidConfig) {
				t.Errorf("resolve error = %v, want authn.ErrInvalidConfig", err)
			}
		})
	}
}

// TestResolveTrimsTheHostedDomain holds that the hosted domain is kept trimmed
// and reads as absent when it is only white space.
func TestResolveTrimsTheHostedDomain(t *testing.T) {
	got, err := moduleConfig{ClientID: "id", ClientSecret: "secret", HostedDomain: "  example.com  "}.
		resolve(gateMount{PublicURL: "https://app.example.com"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.hostedDomain != "example.com" {
		t.Errorf("hostedDomain = %q, want example.com", got.hostedDomain)
	}
	blank, err := moduleConfig{ClientID: "id", ClientSecret: "secret", HostedDomain: "   "}.
		resolve(gateMount{PublicURL: "https://app.example.com"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if blank.hostedDomain != "" {
		t.Errorf("a blank hosted domain = %q, want empty", blank.hostedDomain)
	}
}

// TestTheSchemaMarksTheClientItems holds the two required, sensitive-capable
// items and the optional hosted domain.
func TestTheSchemaMarksTheClientItems(t *testing.T) {
	s := schema()
	if s.Namespace != ConfigNamespace {
		t.Errorf("namespace = %q, want %q", s.Namespace, ConfigNamespace)
	}
	if !s.Items[clientIDItem].Required {
		t.Error("client-id is not required")
	}
	secret := s.Items[clientSecretItem]
	if !secret.Required || !secret.Sensitive || secret.Description == "" {
		t.Errorf("client-secret = %+v, want required, sensitive and described", secret)
	}
	if s.Items[hostedDomainItem].Required {
		t.Error("hosted-domain is marked required")
	}
	for _, key := range []string{issuerItem, allowLoopbackItem, rootCAFileItem} {
		item := s.Items[key]
		if item.Required {
			t.Errorf("%s is marked required", key)
		}
		if item.Description == "" {
			t.Errorf("%s carries no description", key)
		}
	}
}

// TestTheConfigKeysMatchTheSchemaAndTags holds that the namespace, schema keys and config tags name the same items.
func TestTheConfigKeysMatchTheSchemaAndTags(t *testing.T) {
	if ConfigNamespace != ModuleName {
		t.Errorf("ConfigNamespace = %q, want the module name %q", ConfigNamespace, ModuleName)
	}
	s := schema()
	want := map[string]string{
		clientIDItem:      "client-id",
		clientSecretItem:  "client-secret",
		hostedDomainItem:  "hosted-domain",
		issuerItem:        "issuer",
		allowLoopbackItem: "allow-loopback",
		rootCAFileItem:    "root-ca-file",
	}
	if len(s.Items) != len(want) {
		t.Errorf("the schema declares %d items, want %d", len(s.Items), len(want))
	}
	for key, tag := range want {
		if _, ok := s.Items[key]; !ok {
			t.Errorf("the schema declares no item %q", key)
		}
		if key != tag {
			t.Errorf("the item key %q is not the config tag %q", key, tag)
		}
	}
}
