package github

import (
	"errors"
	"testing"

	"golang.org/x/oauth2/endpoints"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// gateDefaults is the carrier the stand-in gateway module declares the keys this
// module reads from the gateway's section under, with the same default the real
// gateway declares for the mount path.
type gateDefaults struct {
	Mount struct {
		PublicURL string `config:"public-url"`
		Bases     struct {
			Default string `config:"default"`
		} `config:"bases"`
	} `config:"mount"`
}

// gateStandIn declares the authn.gate keys this module reads, so a case's body
// may configure them without pulling the whole gateway in.
func gateStandIn() core.Module {
	d := &gateDefaults{}
	d.Mount.Bases.Default = "/api/v1/auth"
	return core.Module{
		Name:      "standin.gate",
		Resources: []any{config.Schema{Namespace: gateNamespace, Mounts: []config.Mount{{Value: d}}}},
	}
}

// readerOver builds the real configuration loader over a body, with this
// module's schema and the gateway keys it reads declared.
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
}

// TestTheReaderRefusesAMissingCredential drives the real configuration loader: a
// required credential no source gave stops the startup, which is what has to
// happen before the module builds a client with an empty secret.
func TestTheReaderRefusesAMissingCredential(t *testing.T) {
	body := `{"authn":{"gate":{"mount":{"public-url":"https://app.example"}},` +
		`"login":{"github":{"client-secret":"the-secret"}}}}`
	_, err := readConfig(readerOver(t, body))
	if !errors.Is(err, config.ErrMissingRequired) {
		t.Fatalf("reading a section with no client-id gave %v, want one wrapping ErrMissingRequired", err)
	}
}

// TestTheReaderServesACompleteSection is the control for the loader case: a
// complete section resolves, the callback address is built from the public base
// and the mount path, the endpoint is GitHub's and the API base is the API host.
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

// TestAnEmptyPublicBaseIsRefused holds that a configuration with no public base
// is a startup failure: the callback address is built from it and must never be
// guessed from a request.
func TestAnEmptyPublicBaseIsRefused(t *testing.T) {
	body := `{"authn":{"login":{"github":{"client-id":"the-id","client-secret":"the-secret"}}}}`
	_, err := readConfig(readerOver(t, body))
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("reading a section with no public base gave %v, want one wrapping ErrInvalidConfig", err)
	}
}

// stubReader answers readConfig from the two sections a case chooses, without a
// loader, so a case can drive a shape the stand-in gateway's defaults would
// otherwise fill or a decode failure the loader would raise first.
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
	// The base path is left empty.
	_, err := readConfig(stubReader{cfg: moduleConfig{ClientID: "the-id", ClientSecret: "the-secret"}, gate: gate})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("an empty mount path gave %v, want one wrapping ErrInvalidConfig", err)
	}
}

// TestAGateDecodeErrorIsRefused holds that a failure to read the gateway's
// section fails the read rather than being swallowed into a half-configured
// method.
func TestAGateDecodeErrorIsRefused(t *testing.T) {
	boom := errors.New("the gateway section cannot be read")
	_, err := readConfig(stubReader{cfg: moduleConfig{ClientID: "the-id", ClientSecret: "the-secret"}, gateErr: boom})
	if !errors.Is(err, boom) {
		t.Fatalf("a gateway decode error gave %v, want it propagated", err)
	}
}
