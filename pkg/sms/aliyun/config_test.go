package aliyun

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/sms/internal/configtest"
)

// completeConfig is a moduleConfig every required item is present in.
func completeConfig() moduleConfig {
	return moduleConfig{
		AccessKeyID:     "the-id",
		AccessKeySecret: "the-secret",
		Endpoint:        defaultEndpoint,
		SignName:        "Speed",
	}
}

// TestACompleteConfigurationResolves holds the control: a complete section
// resolves and carries the values through.
func TestACompleteConfigurationResolves(t *testing.T) {
	got := completeConfig().resolve()
	if got.accessKeyID != "the-id" || got.accessKeySecret != "the-secret" ||
		got.endpoint != defaultEndpoint || got.signName != "Speed" {
		t.Errorf("resolved = %+v, want the configured values", got)
	}
}

// TestTheDeclaredItemsCarryTheDesignsFlags holds the schema: the two credentials
// and the signature are required, the secret is sensitive, and the endpoint has a
// declared default. There is no templates item: the set a message is resolved
// against belongs to the registry, not to the carrier.
func TestTheDeclaredItemsCarryTheDesignsFlags(t *testing.T) {
	s, ok := Module().Resources[0].(config.Schema)
	if !ok {
		t.Fatalf("the module carries %T, want a config.Schema", Module().Resources[0])
	}
	if s.Namespace != ModuleName {
		t.Errorf("Namespace = %q, want %q", s.Namespace, ModuleName)
	}
	secret := s.Items[accessKeyValueItem]
	if !secret.Required || !secret.Sensitive || secret.Description == "" {
		t.Errorf("access-key-secret = %+v, want required, sensitive and described", secret)
	}
	if !s.Items[accessKeyIDItem].Required {
		t.Error("access-key-id is not required")
	}
	if !s.Items[signNameItem].Required {
		t.Error("sign-name is not required")
	}
	if _, ok := s.Items["templates"]; ok {
		t.Error("the schema declares a templates item, which belongs to the registry now")
	}
	if configDefaults.Endpoint != defaultEndpoint {
		t.Errorf("the declared endpoint default = %q, want %q", configDefaults.Endpoint, defaultEndpoint)
	}
}

// TestTheReaderRefusesAMissingRequiredItem drives the real configuration loader:
// a required item no source gave stops the startup, which is what has to happen
// before the module builds a client with an empty credential.
func TestTheReaderRefusesAMissingRequiredItem(t *testing.T) {
	body := `{"sms":{"aliyun":{` +
		`"access-key-secret":"the-secret","sign-name":"Speed"}}}`
	reader := configtest.Reader(t, body, Module())
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); !errors.Is(err, config.ErrMissingRequired) {
		t.Fatalf("reading a section with no access-key-id gave %v, want one wrapping ErrMissingRequired", err)
	}
}

// TestTheReaderServesACompleteSection is the control for the loader case.
func TestTheReaderServesACompleteSection(t *testing.T) {
	reader := configtest.Reader(t, completeBody(), Module())
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		t.Fatalf("reading a complete section: %v", err)
	}
	got := cfg.resolve()
	if got.signName != "Speed" || got.endpoint != defaultEndpoint {
		t.Errorf("resolved = %+v, want the configured signature and the declared endpoint", got)
	}
}
