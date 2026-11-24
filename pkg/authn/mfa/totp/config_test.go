package totp

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestResolveDefaultsTheIssuerToThePublicHost checks that an empty issuer defaults to the public address's host.
func TestResolveDefaultsTheIssuerToThePublicHost(t *testing.T) {
	got, err := moduleConfig{}.resolve(gateMount{PublicURL: "https://id.example.com:8443/api/v1/auth"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.issuer != "id.example.com" {
		t.Errorf("issuer = %q, want the public host id.example.com", got.issuer)
	}
}

// TestResolveKeepsAnExplicitIssuer checks that a configured issuer is kept, trimmed.
func TestResolveKeepsAnExplicitIssuer(t *testing.T) {
	got, err := moduleConfig{Issuer: "  Acme Corp  "}.resolve(gateMount{PublicURL: "https://id.example.com"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.issuer != "Acme Corp" {
		t.Errorf("issuer = %q, want the configured Acme Corp", got.issuer)
	}
}

// TestResolveRefusesWhenThereIsNoIssuerAndNoPublicHost checks that neither an issuer nor a public host is refused.
func TestResolveRefusesWhenThereIsNoIssuerAndNoPublicHost(t *testing.T) {
	if _, err := (moduleConfig{}).resolve(gateMount{}); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("resolve returned %v, want ErrInvalidConfig", err)
	}
}

// TestHostOfIsTheHostAlone checks that hostOf returns the host alone.
func TestHostOfIsTheHostAlone(t *testing.T) {
	cases := map[string]string{
		"https://id.example.com":                  "id.example.com",
		"https://id.example.com:8443/api/v1/auth": "id.example.com",
		"http://localhost:8080":                   "localhost",
		"":                                        "",
	}
	for in, want := range cases {
		got, err := hostOf(in)
		if err != nil {
			t.Fatalf("hostOf(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}
