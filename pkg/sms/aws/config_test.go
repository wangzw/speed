package aws

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/sms"
)

func completeConfig() moduleConfig {
	return moduleConfig{
		Region:          "us-east-1",
		AccessKeyID:     "the-id",
		AccessKeySecret: "the-secret",
		Endpoint:        "",
		DefaultIdentity: "primary",
		Identities: []identityConfig{
			{
				Name:       "primary",
				Kind:       "pool",
				Value:      "pool-primary",
				Countries:  []string{"US", "IN"},
				Attributes: map[string]string{inEntityID: "entity-7"},
			},
		},
	}
}

// TestACompleteConfigurationResolves checks that a complete section resolves and carries the values through.
func TestACompleteConfigurationResolves(t *testing.T) {
	got, err := completeConfig().resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.region != "us-east-1" || got.accessKeyID != "the-id" || got.accessKeySecret != "the-secret" {
		t.Errorf("resolved = %+v, want the configured region and credentials", got)
	}
	if got.defaultIdentity != "primary" {
		t.Errorf("defaultIdentity = %q, want primary", got.defaultIdentity)
	}
	if len(got.identities) != 1 || got.identities[0].Value != "pool-primary" {
		t.Errorf("identities = %+v, want the configured pool", got.identities)
	}
	if got.byName["primary"].Attributes[inEntityID] != "entity-7" {
		t.Errorf("the identity's attributes = %v, want the configured entity identifier",
			got.byName["primary"].Attributes)
	}
}

// TestTheDeclaredItemsCarryTheDesignsFlags checks the schema: the region, identity list and default
// identity are required, the secret is sensitive, and the list takes one source.
func TestTheDeclaredItemsCarryTheDesignsFlags(t *testing.T) {
	s, ok := Module().Resources[0].(config.Schema)
	if !ok {
		t.Fatalf("the module carries %T, want a config.Schema", Module().Resources[0])
	}
	if s.Namespace != ModuleName {
		t.Errorf("Namespace = %q, want %q", s.Namespace, ModuleName)
	}
	for _, item := range []string{regionItem, defaultIdentityItem, identitiesItem} {
		if s.Items[item].Required {
			t.Errorf("%s is declared required; the stance judges an unconfigured carrier instead", item)
		}
	}
	secret := s.Items[accessKeyValueItem]
	if !secret.Sensitive || secret.Description == "" {
		t.Errorf("access-key-secret = %+v, want sensitive and described", secret)
	}
	if s.Items[accessKeyIDItem].Required {
		t.Error("access-key-id is required, yet the SDK's own credential chain may be used instead")
	}
	if got := s.Items[identitiesItem].Origins; got != config.OriginPrimary {
		t.Errorf("identities origins = %v, want the primary config source alone", got)
	}
	if _, ok := s.Items["templates"]; ok {
		t.Error("the schema declares a templates item, which belongs to the registry now")
	}
}

// TestAnIncompleteSectionIsRefused checks that a section without a region is refused with the missing item named.
func TestAnIncompleteSectionIsRefused(t *testing.T) {
	body := `{"sms":{"aws":{` +
		`"default-identity":"primary","identities":[{"name":"primary","kind":"pool","value":"pool-1"}]}}}`
	reader := configtest.Reader(t, body, Module())
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		t.Fatalf("reading the section: %v", err)
	}
	if _, err := stance(&cfg); !errors.Is(err, sms.ErrInvalidConfig) {
		t.Fatalf("the half-configured section gave %v, want one wrapping ErrInvalidConfig", err)
	}
}

// TestTheIdentityListIsRequiredByTheStance checks that a section naming no identity is refused once it is judged.
func TestTheIdentityListIsRequiredByTheStance(t *testing.T) {
	body := `{"sms":{"aws":{"region":"us-east-1","default-identity":"primary"}}}`
	reader := configtest.Reader(t, body, Module())
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		t.Fatalf("reading the section: %v", err)
	}
	if _, err := stance(&cfg); !errors.Is(err, sms.ErrInvalidConfig) {
		t.Fatalf("the section with no identity gave %v, want one wrapping ErrInvalidConfig", err)
	}
}

// TestTheReaderServesACompleteSection checks that the loader serves a complete
// section and decodes identities with their countries and attributes.
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
	if got.region != "us-east-1" || got.defaultIdentity != "primary" {
		t.Errorf("resolved = %+v, want the configured region and default identity", got)
	}
	if len(got.identities) != 2 {
		t.Fatalf("identities = %+v, want the two the section declares", got.identities)
	}
	primary := got.byName["primary"]
	if len(primary.Countries) != 2 || primary.Countries[1] != "IN" {
		t.Errorf("the first identity's Countries = %v, want US and IN", primary.Countries)
	}
	if primary.Attributes[inEntityID] != "entity-7" {
		t.Errorf("the first identity's Attributes = %v, want the configured entity identifier", primary.Attributes)
	}
}

// TestResolveRefusesAConfigurationItCannotRunOn checks that each defect is ErrInvalidConfig at startup.
func TestResolveRefusesAConfigurationItCannotRunOn(t *testing.T) {
	cases := map[string]func(*moduleConfig){
		"no region":                      func(c *moduleConfig) { c.Region = " " },
		"an identifier without a secret": func(c *moduleConfig) { c.AccessKeySecret = "" },
		"a secret without an identifier": func(c *moduleConfig) { c.AccessKeyID = "" },
		"no identity":                    func(c *moduleConfig) { c.Identities = nil },
		"an identity with no name":       func(c *moduleConfig) { c.Identities[1].Name = " " },
		"an identity with no kind":       func(c *moduleConfig) { c.Identities[1].Kind = "" },
		"a kind outside the vocabulary":  func(c *moduleConfig) { c.Identities[1].Kind = "sender_id" },
		"an identity with no value":      func(c *moduleConfig) { c.Identities[1].Value = " " },
		"an empty country":               func(c *moduleConfig) { c.Identities[1].Countries = []string{"US", " "} },
		"a duplicated name":              func(c *moduleConfig) { c.Identities[1].Name = "primary" },
		"an undeclared default":          func(c *moduleConfig) { c.DefaultIdentity = "elsewhere" },
		"no default identity":            func(c *moduleConfig) { c.DefaultIdentity = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := completeConfig()
			cfg.Identities = append(cfg.Identities, identityConfig{
				Name: "secondary", Kind: "sender-id", Value: "SPEED2",
			})
			mutate(&cfg)
			if _, err := cfg.resolve(); !errors.Is(err, sms.ErrInvalidConfig) {
				t.Fatalf("resolve answered %v, want the invalid-config sentinel", err)
			}
		})
	}
}

// TestTheSecretsHookNamesTheSecretAlone checks that only the secret access key is registered as a redaction rule.
func TestTheSecretsHookNamesTheSecretAlone(t *testing.T) {
	cfg := completeConfig()
	cfg.AccessKeyID = "the-id"
	cfg.AccessKeySecret = "the-secret"
	got := secrets(&cfg)
	if len(got) != 1 || got[0] != "the-secret" {
		t.Errorf("secrets = %v, want the secret access key alone", got)
	}
}

// TestAnEndpointWithoutASchemeTakesHTTPS checks the endpoint rule: a bare host
// takes https, and a value naming a scheme is left as written.
func TestAnEndpointWithoutASchemeTakesHTTPS(t *testing.T) {
	for _, c := range []struct{ given, want string }{
		{"sms-voice.us-east-1.amazonaws.com", "https://sms-voice.us-east-1.amazonaws.com"},
		{"https://gateway.internal", "https://gateway.internal"},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080"},
	} {
		if got := endpointURL(c.given); got != c.want {
			t.Errorf("endpointURL(%q) = %q, want %q", c.given, got, c.want)
		}
	}
}

func completeBody() string {
	return `{"sms":{"aws":{` +
		`"region":"us-east-1",` +
		`"access-key-id":"the-id",` +
		`"access-key-secret":"the-secret",` +
		`"configuration-set-name":"speed-sends",` +
		`"default-identity":"primary",` +
		`"identities":[` +
		`{"name":"primary","kind":"pool","value":"pool-1","countries":["US","IN"],` +
		`"attributes":{"IN_ENTITY_ID":"entity-7"}},` +
		`{"name":"india","kind":"sender-id","value":"SPEEDIN","countries":["IN"]}` +
		`]}}}`
}
