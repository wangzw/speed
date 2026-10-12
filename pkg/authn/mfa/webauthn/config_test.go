package webauthn

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// Without a relying party id or origins the module takes the public address's
// host and origin, and the fixed display name.
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

// A relying party id that is the host or a parent domain of it is accepted, at a
// label boundary and no other way.
func TestTheRelyingPartyIDMustCoverTheHost(t *testing.T) {
	cases := []struct {
		rpID string
		ok   bool
	}{
		{"auth.example.com", true}, // the host itself
		{"example.com", true},      // a parent domain
		{"com", true},              // a further parent, the public suffix table not being consulted
		{"evil.com", false},        // an unrelated domain
		{"notauth.example.com", false},
		{"xample.com", false}, // a suffix but not at a label boundary
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

// An empty public address is refused: it is the base the relying party id is
// checked against and the origins are drawn from.
func TestAnEmptyPublicAddressIsRefused(t *testing.T) {
	if _, err := (moduleConfig{}).resolve(gateMount{}); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("resolve with no public address returned %v, want ErrInvalidConfig", err)
	}
}

// Explicit origins and a display name are kept rather than defaulted.
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

// readConfig draws the same result from a reader as the resolve step does, which
// is what Prepare and New both go through.
func TestReadConfigReadsTheGateAddress(t *testing.T) {
	got, err := readConfig(configReader("https://auth.example.com"))
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if got.rpID != "auth.example.com" {
		t.Errorf("readConfig gave the relying party id %q, want the host", got.rpID)
	}
}

// A host with a port compares without it, because a relying party id carries no
// port.
func TestTheHostIsComparedWithoutItsPort(t *testing.T) {
	got, err := moduleConfig{}.resolve(gateMount{PublicURL: "http://localhost:8080"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.rpID != "localhost" {
		t.Errorf("the relying party id is %q, want localhost without the port", got.rpID)
	}
}
