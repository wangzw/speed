package google

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

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
			want: "https://app.example.com/auth/callback/google",
		},
		{
			name: "a configured base path",
			cfg:  moduleConfig{ClientID: "id", ClientSecret: "secret"},
			gm:   gateMount{PublicURL: "https://app.example.com", BasePath: "/identity"},
			want: "https://app.example.com/identity/callback/google",
		},
		{
			name: "a trailing slash on the address",
			cfg:  moduleConfig{ClientID: "id", ClientSecret: "secret"},
			gm:   gateMount{PublicURL: "https://app.example.com/", BasePath: "/auth/"},
			want: "https://app.example.com/auth/callback/google",
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

// TestResolveRefusesAnIncompleteConfiguration holds that a missing client
// identifier, secret or public address is a startup failure and not a silent
// absence.
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
}

// TestTheConfigKeysMatchTheSchemaAndTags holds the naming contract: the module's
// namespace is its name, the three item keys are the schema's keys, and each is
// the config tag the mount struct reads it by. A drift between the two would let
// the schema declare a key the reader never fills.
func TestTheConfigKeysMatchTheSchemaAndTags(t *testing.T) {
	if ConfigNamespace != ModuleName {
		t.Errorf("ConfigNamespace = %q, want the module name %q", ConfigNamespace, ModuleName)
	}
	s := schema()
	want := map[string]string{
		clientIDItem:     "client-id",
		clientSecretItem: "client-secret",
		hostedDomainItem: "hosted-domain",
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
