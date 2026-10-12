package totp

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestResolveDefaultsTheIssuerToThePublicHost holds the declared default: an
// empty issuer is the host name of the gateway's public address, without a
// scheme, a port or a path.
func TestResolveDefaultsTheIssuerToThePublicHost(t *testing.T) {
	got, err := moduleConfig{}.resolve(gateMount{PublicURL: "https://id.example.com:8443/auth"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.issuer != "id.example.com" {
		t.Errorf("issuer = %q, want the public host id.example.com", got.issuer)
	}
}

// TestResolveKeepsAnExplicitIssuer holds that a configured issuer is used as it
// is, trimmed, over the public address.
func TestResolveKeepsAnExplicitIssuer(t *testing.T) {
	got, err := moduleConfig{Issuer: "  Acme Corp  "}.resolve(gateMount{PublicURL: "https://id.example.com"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.issuer != "Acme Corp" {
		t.Errorf("issuer = %q, want the configured Acme Corp", got.issuer)
	}
}

// TestResolveRefusesWhenThereIsNoIssuerAndNoPublicHost holds that an assembly
// with neither is a configuration the module cannot run on, because the otpauth
// address has no issuer to name.
func TestResolveRefusesWhenThereIsNoIssuerAndNoPublicHost(t *testing.T) {
	if _, err := (moduleConfig{}).resolve(gateMount{}); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("resolve returned %v, want ErrInvalidConfig", err)
	}
}

// TestHostOfIsTheHostAlone holds the host extraction the default goes through.
func TestHostOfIsTheHostAlone(t *testing.T) {
	cases := map[string]string{
		"https://id.example.com":           "id.example.com",
		"https://id.example.com:8443/auth": "id.example.com",
		"http://localhost:8080":            "localhost",
		"":                                 "",
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
