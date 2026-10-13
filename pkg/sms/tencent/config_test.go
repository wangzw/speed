package tencent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/sms/internal/configtest"
)

// validTemplates declares every template the cases send under.
func validTemplates() map[string]sms.TemplateSpec {
	return map[string]sms.TemplateSpec{
		"authn.code.login": {
			Params: []string{"code", "ttl_minutes"},
			Codes:  map[string]string{"zh-CN": "1001", "en-US": "1002"},
		},
		"authn.code.mfa": {
			Params: []string{"code", "ttl_minutes"},
			Codes:  map[string]string{"zh-CN": "1003", "en-US": "1004"},
		},
		"authn.code.enroll": {
			Params: []string{"code", "ttl_minutes"},
			Codes:  map[string]string{"zh-CN": "1005", "en-US": "1006"},
		},
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

// section renders this module's section out of the given template table and the
// given override of the scalar items.
func section(templates map[string]sms.TemplateSpec, override map[string]any) map[string]any {
	out := map[string]any{
		"secret-id":  "AKIDexample",
		"secret-key": "secret-key-example",
		"sdk-app-id": "1400006666",
		"region":     "ap-guangzhou",
		"sign-name":  "Speed",
		"templates":  templateDoc(templates),
	}
	for k, v := range override {
		if v == nil {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out
}

// body renders a whole configuration document holding this module's section.
func body(t *testing.T, s map[string]any) string {
	t.Helper()
	doc := map[string]any{"sms": map[string]any{"tencent": s}}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// readBody loads a document with the real configuration loader and decodes this
// module's section, returning the configuration or the error the module would
// refuse the startup with.
func readBody(t *testing.T, document string) (resolved, error) {
	t.Helper()
	reader := configtest.Reader(t, document, Module())
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}

// A section with every item and a self-consistent template set resolves into the
// credentials and the declaration the module runs on.
func TestACompleteSectionResolves(t *testing.T) {
	cfg, err := readBody(t, body(t, section(validTemplates(), nil)))
	if err != nil {
		t.Fatalf("resolving a complete section: %v", err)
	}
	if cfg.secretID != "AKIDexample" || cfg.sdkAppID != "1400006666" || cfg.region != "ap-guangzhou" ||
		cfg.signName != "Speed" {
		t.Errorf("the scalar items did not survive: %+v", cfg)
	}
	if decl := cfg.decl.Declared(); len(decl) != len(validTemplates()) {
		t.Errorf("the declaration holds %d templates, want %d", len(decl), len(validTemplates()))
	}
}

// A template set the carrier cannot send under is a startup failure naming the
// template: a template with an empty carrier code for a language never passes
// startup. Without this the carrier would fail to build the message only when a
// code is first sent.
func TestAnInconsistentTemplateIsRefused(t *testing.T) {
	for name := range validTemplates() {
		t.Run(name, func(t *testing.T) {
			templates := validTemplates()
			templates[name] = sms.TemplateSpec{Params: []string{"code"}, Codes: map[string]string{"zh-CN": ""}}
			_, err := readBody(t, body(t, section(templates, nil)))
			if !errors.Is(err, sms.ErrInvalidConfig) {
				t.Fatalf("an inconsistent %s was accepted: %v", name, err)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("the failure does not name the template %q: %v", name, err)
			}
		})
	}
}

// Each scalar item is required: a section that leaves one out fails the load.
func TestEachScalarItemIsRequired(t *testing.T) {
	for _, item := range []string{"secret-id", "secret-key", "sdk-app-id", "region", "sign-name", "templates"} {
		t.Run(item, func(t *testing.T) {
			override := map[string]any{item: nil}
			_, err := readBody(t, body(t, section(validTemplates(), override)))
			if !errors.Is(err, config.ErrMissingRequired) {
				t.Fatalf("a section with no %s was accepted: %v", item, err)
			}
		})
	}
}

// The declaration marks the secret sensitive and confines the template table to
// the primary source, which is what keeps the key out of help output and the
// table out of an environment variable.
func TestTheSchemaDeclaresTheSensitiveAndContainerItems(t *testing.T) {
	s, ok := Module().Resources[0].(config.Schema)
	if !ok {
		t.Fatalf("the module carries %T, want a config.Schema", Module().Resources[0])
	}
	if !s.Items[secretKeyItem].Sensitive {
		t.Error("the secret key is not declared sensitive")
	}
	if s.Items[signNameItem].Sensitive {
		t.Error("the sign name, which is public, is declared sensitive")
	}
	if got := s.Items[templatesItem].Origins; got != config.OriginPrimary {
		t.Errorf("the template table takes the origins %v, want the primary source alone", got)
	}
	for _, item := range []string{secretIDItem, secretKeyItem, sdkAppIDItem, regionItem, signNameItem} {
		if !s.Items[item].Required {
			t.Errorf("the item %q is not declared required", item)
		}
	}
}
