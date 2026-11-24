package webauthn

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheRelyingPartyDefaultsToThePublicAddress checks that the relying party id, origin and display name default off the public address.
func TestTheRelyingPartyDefaultsToThePublicAddress(t *testing.T) {
	got, err := moduleConfig{}.resolve(gateMount{PublicURL: "https://auth.example.com"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.rpID != "auth.example.com" {
		t.Errorf("the relying party id defaults to %q, want the host", got.rpID)
	}
	if len(got.origins) != 1 || got.origins[0] != "https://auth.example.com" {
		t.Errorf("the origins default to %v, want the public address's origin", got.origins)
	}
	if got.rpName != rpNameDefault {
		t.Errorf("the display name defaults to %q, want %q", got.rpName, rpNameDefault)
	}
}

// TestTheRelyingPartyIDMustCoverTheHost checks that only the host or a parent domain at a label boundary is accepted.
func TestTheRelyingPartyIDMustCoverTheHost(t *testing.T) {
	cases := []struct {
		rpID string
		ok   bool
	}{
		{"auth.example.com", true},
		{"example.com", true},
		{"com", true},
		{"evil.com", false},
		{"notauth.example.com", false},
		{"xample.com", false},
	}
	for _, tc := range cases {
		_, err := moduleConfig{RPID: tc.rpID}.resolve(gateMount{PublicURL: "https://auth.example.com"})
		if tc.ok && err != nil {
			t.Errorf("resolve with rp-id %q: %v, want it accepted", tc.rpID, err)
		}
		if !tc.ok && !errors.Is(err, authn.ErrInvalidConfig) {
			t.Errorf("resolve with rp-id %q returned %v, want ErrInvalidConfig", tc.rpID, err)
		}
	}
}

// TestAnEmptyPublicAddressIsRefused checks that an empty public address is refused.
func TestAnEmptyPublicAddressIsRefused(t *testing.T) {
	if _, err := (moduleConfig{}).resolve(gateMount{}); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("resolve with no public address returned %v, want ErrInvalidConfig", err)
	}
}

// TestExplicitValuesAreKept checks that explicit origins and display name are kept.
func TestExplicitValuesAreKept(t *testing.T) {
	got, err := moduleConfig{
		RPName:  "Acme",
		Origins: []string{"https://auth.example.com", "https://login.example.com"},
	}.resolve(gateMount{PublicURL: "https://auth.example.com"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.rpName != "Acme" {
		t.Errorf("the display name is %q, want the configured one", got.rpName)
	}
	if len(got.origins) != 2 {
		t.Errorf("the origins are %v, want the two configured", got.origins)
	}
}

// TestReadConfigReadsTheGateAddress checks that readConfig takes the gate's host as the relying party id.
func TestReadConfigReadsTheGateAddress(t *testing.T) {
	got, err := readConfig(configReader("https://auth.example.com"))
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if got.rpID != "auth.example.com" {
		t.Errorf("readConfig gave the relying party id %q, want the host", got.rpID)
	}
}

// TestTheHostIsComparedWithoutItsPort checks that a host compares without its port.
func TestTheHostIsComparedWithoutItsPort(t *testing.T) {
	got, err := moduleConfig{}.resolve(gateMount{PublicURL: "http://localhost:8080"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.rpID != "localhost" {
		t.Errorf("the relying party id is %q, want localhost without the port", got.rpID)
	}
}
