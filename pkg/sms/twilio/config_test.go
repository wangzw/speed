package twilio

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/sms"
)

// TestACompleteConfigurationResolves checks that a complete section resolves with the identities in a stable order.
func TestACompleteConfigurationResolves(t *testing.T) {
	got, err := completeConfig(defaultAPIBaseURL, defaultContentBaseURL).resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.accountSID != testAccountSID || got.authToken != testAuthToken {
		t.Errorf("resolved = %+v, want the configured credentials", got)
	}
	if got.defaultIdentity != "service" {
		t.Errorf("defaultIdentity = %q, want service", got.defaultIdentity)
	}
	if len(got.identities) != 2 || got.identities[0].Name != "number" || got.identities[1].Name != "service" {
		t.Errorf("identities = %+v, want the two declared identities ordered by name", got.identities)
	}
	if got.identityByName["service"].Value != "MG00000000000000000000000000000000" {
		t.Errorf("the service identity = %+v, want the configured value", got.identityByName["service"])
	}
	if got.apiBase.String() != defaultAPIBaseURL || got.contentBase.String() != defaultContentBaseURL {
		t.Errorf("base URLs = %s and %s, want the declared defaults", got.apiBase, got.contentBase)
	}
}

// TestTheDeclaredItemsCarryTheDesignsFlags checks the schema: the credentials, default identity and
// identity list are required, the auth token is sensitive, and the access points have defaults.
func TestTheDeclaredItemsCarryTheDesignsFlags(t *testing.T) {
	s, ok := Module().Resources[0].(config.Schema)
	if !ok {
		t.Fatalf("the module carries %T, want a config.Schema", Module().Resources[0])
	}
	if s.Namespace != ModuleName {
		t.Errorf("Namespace = %q, want %q", s.Namespace, ModuleName)
	}
	for _, key := range []string{accountSIDItem, defaultIdentityItem, identitiesItem} {
		if s.Items[key].Required {
			t.Errorf("%s is declared required; the stance judges an unconfigured carrier instead", key)
		}
	}
	token := s.Items[authTokenItem]
	if !token.Sensitive || token.Description == "" {
		t.Errorf("auth-token = %+v, want sensitive and described", token)
	}
	if _, ok := s.Items["templates"]; ok {
		t.Error("the schema declares a templates item, which belongs to the registry")
	}
	if configDefaults.APIBaseURL != defaultAPIBaseURL || configDefaults.ContentBaseURL != defaultContentBaseURL {
		t.Errorf("the declared defaults = %+v, want the two public access points", configDefaults)
	}
}

// TestAnIncompleteSectionIsRefused checks that a section missing one of the
// items that make a usable carrier is half-configured and refused.
func TestAnIncompleteSectionIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"no account SID": `{"sms":{"twilio":{` +
			`"auth-token":"the-token","default-identity":"service",` +
			`"identities":[{"name":"service","kind":"messaging-service","value":"MG1"}]}}}`,
		"no identities": `{"sms":{"twilio":{` +
			`"account-sid":"AC1","auth-token":"the-token","default-identity":"service"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			reader := configtest.Reader(t, body, Module())
			var cfg moduleConfig
			if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
				t.Fatalf("reading the section: %v", err)
			}
			if _, err := stance(&cfg); !errors.Is(err, sms.ErrInvalidConfig) {
				t.Fatalf("the half-configured section gave %v, want one wrapping ErrInvalidConfig", err)
			}
		})
	}
}

func completeBody() string {
	return `{"sms":{"twilio":{` +
		`"account-sid":"` + testAccountSID + `","auth-token":"` + testAuthToken + `",` +
		`"default-identity":"service",` +
		`"identities":[{"name":"service","kind":"messaging-service","value":"MG1"}]}}}`
}

// TestTheReaderServesACompleteSection checks that the loader serves a complete section.
func TestTheReaderServesACompleteSection(t *testing.T) {
	reader := configtest.Reader(t, completeBody(), Module())
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		t.Fatalf("reading a complete section: %v", err)
	}
	got, err := cfg.resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.defaultIdentity != "service" || len(got.identities) != 1 {
		t.Errorf("resolved = %+v, want the configured default identity and its one identity", got)
	}
	if got.apiBase.String() != defaultAPIBaseURL || got.contentBase.String() != defaultContentBaseURL {
		t.Errorf("base URLs = %s and %s, want the defaults an absent key leaves", got.apiBase, got.contentBase)
	}
}

// TestOpenRefusesAnUndeclaredDefaultIdentity checks that a default identity naming nothing is refused when the product is built.
func TestOpenRefusesAnUndeclaredDefaultIdentity(t *testing.T) {
	cfg := completeConfig(defaultAPIBaseURL, defaultContentBaseURL)
	cfg.DefaultIdentity = "nobody"
	if _, err := open(t.Context(), &cfg); !errors.Is(err, sms.ErrInvalidConfig) {
		t.Fatalf("open answered %v, want the invalid-config sentinel", err)
	}
}

// TestResolveRefusesASectionItCannotRunOn checks that each startup refusal fails before a message is built.
func TestResolveRefusesASectionItCannotRunOn(t *testing.T) {
	for name, mutate := range map[string]func(*moduleConfig){
		"an unknown default identity": func(cfg *moduleConfig) { cfg.DefaultIdentity = "nobody" },
		"no identity at all":          func(cfg *moduleConfig) { cfg.Identities = nil },
		"a duplicate identity name": func(cfg *moduleConfig) {
			cfg.Identities = append(cfg.Identities, cfg.Identities[0])
		},
		"an identity with no name": func(cfg *moduleConfig) { cfg.Identities[1].Name = "" },
		"an identity with no value": func(cfg *moduleConfig) {
			cfg.Identities[1].Value = ""
		},
		"an identity of an unknown kind": func(cfg *moduleConfig) {
			cfg.Identities[1].Kind = sms.IdentityKind("telepathy")
		},
		"an access point that is not a URL": func(cfg *moduleConfig) { cfg.APIBaseURL = "::not a url::" },
		"an access point that is not http":  func(cfg *moduleConfig) { cfg.ContentBaseURL = "ftp://content.example" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := completeConfig(defaultAPIBaseURL, defaultContentBaseURL)
			mutate(&cfg)
			if _, err := cfg.resolve(); !errors.Is(err, sms.ErrInvalidConfig) {
				t.Fatalf("resolve answered %v, want the invalid-config sentinel", err)
			}
		})
	}
}
