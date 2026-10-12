package aliyun

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
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

// TestAMappingThatMissesACombinationIsRefusedAtStartup holds that a template map
// not covering every (purpose, locale) pair is a startup failure naming the
// combinations that are absent, rather than a message the carrier refuses only
// when a code is first sent for that purpose.
func TestAMappingThatMissesACombinationIsRefusedAtStartup(t *testing.T) {
	for _, drop := range [][]string{
		{"login/zh-CN"},
		{"login/en-US"},
		{"mfa/zh-CN"},
		{"enroll/en-US"},
		{"login/zh-CN", "mfa/en-US", "enroll/zh-CN"},
		{"login/zh-CN", "login/en-US", "mfa/zh-CN", "mfa/en-US", "enroll/zh-CN", "enroll/en-US"},
	} {
		t.Run(strings.Join(drop, ","), func(t *testing.T) {
			cfg := completeConfig()
			for _, key := range drop {
				delete(cfg.Templates, key)
			}
			_, err := cfg.resolve()
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("resolving with %v absent gave %v, want one wrapping ErrInvalidConfig", drop, err)
			}
			for _, key := range drop {
				if !strings.Contains(err.Error(), key) {
					t.Errorf("the failure %q does not name the missing combination %q", err.Error(), key)
				}
			}
		})
	}
}

// TestAnEmptyTemplateCodeIsAMissingCombination holds that a combination present
// with an empty value is the same fact as one that is absent.
func TestAnEmptyTemplateCodeIsAMissingCombination(t *testing.T) {
	cfg := completeConfig()
	cfg.Templates["mfa/zh-CN"] = ""
	if _, err := cfg.resolve(); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("resolving with an empty template gave %v, want one wrapping ErrInvalidConfig", err)
	}
}

// TestABlankTemplateCodeIsAMissingCombination holds that a value of only
// whitespace is the same fact as an absent one, so a blank template number does
// not pass the startup check and is not sent to the carrier.
func TestABlankTemplateCodeIsAMissingCombination(t *testing.T) {
	cfg := completeConfig()
	cfg.Templates["enroll/en-US"] = "   "
	if _, err := cfg.resolve(); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("resolving with a blank template gave %v, want one wrapping ErrInvalidConfig", err)
	}
}

// TestACompleteConfigurationResolves holds the control: every combination
// present resolves and carries the values through.
func TestACompleteConfigurationResolves(t *testing.T) {
	got, err := completeConfig().resolve()
	if err != nil {
		t.Fatalf("resolving a complete configuration: %v", err)
	}
	if got.accessKeyID != "the-id" || got.accessKeySecret != "the-secret" ||
		got.endpoint != defaultEndpoint || got.signName != "Speed" {
		t.Errorf("resolved = %+v, want the configured values", got)
	}
	if got.templates["login/zh-CN"] != "SMS_LOGIN_ZH" {
		t.Errorf("the template map was not carried through: %v", got.templates)
	}
}

// TestTheResolvedTemplatesIsPrivate holds that a host keeping a handle to its
// decoded configuration cannot change the mapping after the startup checks.
func TestTheResolvedTemplatesIsPrivate(t *testing.T) {
	cfg := completeConfig()
	got, err := cfg.resolve()
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	delete(cfg.Templates, "login/zh-CN")
	if _, ok := got.templates["login/zh-CN"]; !ok {
		t.Error("the resolved mapping followed the caller's map, which it was expected to copy")
	}
}

// TestTheDeclaredItemsCarryTheDesignsFlags holds the schema: the two credentials
// and the signature are required, the secret is sensitive, the template map is a
// container leaf read from the primary source alone, and the endpoint has a
// declared default.
func TestTheDeclaredItemsCarryTheDesignsFlags(t *testing.T) {
	s := schema()
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
	body := `{"authn":{"sms":{"aliyun":{` +
		`"access-key-secret":"the-secret","sign-name":"Speed","templates":` + templatesJSON() + `}}}}`
	reader := authntest.ConfigReader(t, Module(), body)
	_, err := readConfig(reader)
	if !errors.Is(err, config.ErrMissingRequired) {
		t.Fatalf("reading a section with no access-key-id gave %v, want one wrapping ErrMissingRequired", err)
	}
}

// TestTheReaderServesACompleteSection is the control for the loader case.
func TestTheReaderServesACompleteSection(t *testing.T) {
	body := `{"authn":{"sms":{"aliyun":{` +
		`"access-key-id":"the-id","access-key-secret":"the-secret","sign-name":"Speed",` +
		`"templates":` + templatesJSON() + `}}}}`
	reader := authntest.ConfigReader(t, Module(), body)
	got, err := readConfig(reader)
	if err != nil {
		t.Fatalf("reading a complete section: %v", err)
	}
	if got.signName != "Speed" || got.endpoint != defaultEndpoint {
		t.Errorf("resolved = %+v, want the configured signature and the declared endpoint", got)
	}
}

// templatesJSON writes the covering template map as the JSON a case configures.
func templatesJSON() string {
	var b strings.Builder
	b.WriteString("{")
	first := true
	for key, code := range validTemplates() {
		if !first {
			b.WriteString(",")
		}
		first = false
		b.WriteString(`"` + key + `":"` + code + `"`)
	}
	b.WriteString("}")
	return b.String()
}
