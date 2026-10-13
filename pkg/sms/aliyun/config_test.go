package aliyun

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/sms/internal/configtest"
)

// completeConfig is a moduleConfig every required item is present in.
func completeConfig() moduleConfig {
	return moduleConfig{
		AccessKeyID:     "the-id",
		AccessKeySecret: "the-secret",
		Endpoint:        defaultEndpoint,
		SignName:        "Speed",
		Templates:       validTemplates(),
	}
}

// templateDoc renders the nested documents the configuration loader decodes:
// the templates item is a container leaf whose values decode by their config
// tags, so the keys are params and codes.
func templateDoc(templates map[string]sms.TemplateSpec) map[string]any {
	out := make(map[string]any, len(templates))
	for name, spec := range templates {
		out[name] = map[string]any{"params": spec.Params, "codes": spec.Codes}
	}
	return out
}

// templatesJSON writes the covering template map as the JSON a case configures.
func templatesJSON() string {
	b, err := json.Marshal(templateDoc(validTemplates()))
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestAnInconsistentTemplateIsRefusedAtStartup holds that a template set the
// carrier cannot send under is a startup failure: a template with no parameter,
// no language, an empty code or a repeated parameter is refused, naming the
// template, rather than a message the carrier fails to build only when a code is
// first sent.
func TestAnInconsistentTemplateIsRefusedAtStartup(t *testing.T) {
	cases := map[string]sms.TemplateSpec{
		"no parameter":     {Codes: map[string]string{"zh-CN": "SMS_X"}},
		"no language":      {Params: []string{"code"}},
		"an empty code":    {Params: []string{"code"}, Codes: map[string]string{"zh-CN": "  "}},
		"a repeated param": {Params: []string{"code", "code"}, Codes: map[string]string{"zh-CN": "SMS_X"}},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := completeConfig()
			cfg.Templates["authn.code.mfa"] = spec
			_, err := cfg.resolve()
			if !errors.Is(err, sms.ErrInvalidConfig) {
				t.Fatalf("resolving with %s gave %v, want one wrapping ErrInvalidConfig", name, err)
			}
			if !strings.Contains(err.Error(), "authn.code.mfa") {
				t.Errorf("the failure %q does not name the offending template", err.Error())
			}
		})
	}
}

// TestACompleteConfigurationResolves holds the control: a self-consistent set
// resolves and carries the values through.
func TestACompleteConfigurationResolves(t *testing.T) {
	got, err := completeConfig().resolve()
	if err != nil {
		t.Fatalf("resolving a complete configuration: %v", err)
	}
	if got.accessKeyID != "the-id" || got.accessKeySecret != "the-secret" ||
		got.endpoint != defaultEndpoint || got.signName != "Speed" {
		t.Errorf("resolved = %+v, want the configured values", got)
	}
	if decl := got.decl.Declared(); len(decl) != len(validTemplates()) {
		t.Errorf("the declaration holds %d templates, want %d", len(decl), len(validTemplates()))
	}
}

// TestTheResolvedDeclarationIsPrivate holds that a host keeping a handle to its
// decoded configuration cannot change the declaration after the startup checks.
func TestTheResolvedDeclarationIsPrivate(t *testing.T) {
	cfg := completeConfig()
	got, err := cfg.resolve()
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	delete(cfg.Templates, "authn.code.login")
	m := sms.Message{Template: "authn.code.login", Locale: "zh-CN", Params: map[string]string{"code": "1", "ttl_minutes": "5"}}
	if _, err := got.decl.Resolve(m); err != nil {
		t.Errorf("the resolved declaration followed the caller's map: %v", err)
	}
}

// TestTheDeclaredItemsCarryTheDesignsFlags holds the schema: the two credentials
// and the signature are required, the secret is sensitive, the template item is a
// container leaf read from the primary source alone, and the endpoint has a
// declared default.
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
	templates := s.Items[templatesItem]
	if !templates.Required || templates.Origins != config.OriginPrimary {
		t.Errorf("templates = %+v, want required and read from the primary source alone", templates)
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
		`"access-key-secret":"the-secret","sign-name":"Speed","templates":` + templatesJSON() + `}}}`
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
	got, err := cfg.resolve()
	if err != nil {
		t.Fatalf("resolving a complete section: %v", err)
	}
	if got.signName != "Speed" || got.endpoint != defaultEndpoint {
		t.Errorf("resolved = %+v, want the configured signature and the declared endpoint", got)
	}
}
