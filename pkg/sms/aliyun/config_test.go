package aliyun

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/sms"
)

func completeConfig() moduleConfig {
	return moduleConfig{
		AccessKeyID:     "the-id",
		AccessKeySecret: "the-secret",
		Endpoint:        defaultEndpoint,
		SignName:        "Speed",
	}
}

// TestACompleteConfigurationResolves checks that a complete section resolves and carries the values through.
func TestACompleteConfigurationResolves(t *testing.T) {
	got := completeConfig().resolve()
	if got.accessKeyID != "the-id" || got.accessKeySecret != "the-secret" ||
		got.endpoint != defaultEndpoint || got.signName != "Speed" {
		t.Errorf("resolved = %+v, want the configured values", got)
	}
}

// TestTheDeclaredItemsCarryTheDesignsFlags checks the schema: the two credentials and the
// signature are required, the secret is sensitive, and the endpoint has a declared default.
func TestTheDeclaredItemsCarryTheDesignsFlags(t *testing.T) {
	s, ok := Module().Resources[0].(config.Schema)
	if !ok {
		t.Fatalf("the module carries %T, want a config.Schema", Module().Resources[0])
	}
	if s.Namespace != ModuleName {
		t.Errorf("Namespace = %q, want %q", s.Namespace, ModuleName)
	}
	secret := s.Items[accessKeyValueItem]
	if !secret.Sensitive || secret.Description == "" {
		t.Errorf("access-key-secret = %+v, want sensitive and described", secret)
	}
	for _, item := range []string{accessKeyIDItem, accessKeyValueItem, signNameItem} {
		if s.Items[item].Required {
			t.Errorf("%s is declared required; the stance judges an unconfigured carrier instead", item)
		}
	}
	if _, ok := s.Items["templates"]; ok {
		t.Error("the schema declares a templates item, which belongs to the registry now")
	}
	if configDefaults.Endpoint != defaultEndpoint {
		t.Errorf("the declared endpoint default = %q, want %q", configDefaults.Endpoint, defaultEndpoint)
	}
}

// TestAnIncompleteSectionIsRefused checks that a half-configured section is refused with the missing item named.
func TestAnIncompleteSectionIsRefused(t *testing.T) {
	body := `{"sms":{"aliyun":{` +
		`"access-key-secret":"the-secret","sign-name":"Speed"}}}`
	reader := configtest.Reader(t, body, Module())
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		t.Fatalf("reading the section: %v", err)
	}
	if _, err := stance(&cfg); !errors.Is(err, sms.ErrInvalidConfig) {
		t.Fatalf("the half-configured section gave %v, want one wrapping ErrInvalidConfig", err)
	}
}

// TestTheReaderServesACompleteSection checks that the loader serves a complete section.
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
