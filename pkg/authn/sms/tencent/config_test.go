package tencent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
)

// validTemplates maps every (purpose, locale) pair of the closed sets to a
// carrier template number.
func validTemplates() map[string]string {
	return map[string]string{
		"login/zh-CN":  "1001",
		"login/en-US":  "1002",
		"mfa/zh-CN":    "1003",
		"mfa/en-US":    "1004",
		"enroll/zh-CN": "1005",
		"enroll/en-US": "1006",
	}
}

// section renders this module's section out of the given template table and the
// given override of the scalar items.
func section(templates map[string]string, override map[string]any) map[string]any {
	out := map[string]any{
		"secret-id":  "AKIDexample",
		"secret-key": "secret-key-example",
		"sdk-app-id": "1400006666",
		"region":     "ap-guangzhou",
		"sign-name":  "Speed",
		"templates":  templates,
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
	doc := map[string]any{"authn": map[string]any{"sms": map[string]any{"tencent": s}}}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// loadBody runs the real configuration loader over a document holding this
// module's section and returns the reader or the error, so a case can see what
// the loader refuses before the module ever reads a value.
func loadBody(t *testing.T, document string) (config.Reader, error) {
	t.Helper()
	locator := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(locator, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDTENCENTTEST", DefaultLocator: "file://" + locator}},
	})
	reg.Register(Module())
	return config.Load(t.Context(), reg, nil)
}

// readBody loads a document and decodes this module's section, returning the
// configuration or the error the module would refuse the startup with.
func readBody(t *testing.T, document string) (resolved, error) {
	t.Helper()
	reader, err := loadBody(t, document)
	if err != nil {
		return resolved{}, err
	}
	return readConfig(reader)
}

// A section with every item and every template combination resolves into the
// credentials and the table the module runs on.
func TestACompleteSectionResolves(t *testing.T) {
	cfg, err := readBody(t, body(t, section(validTemplates(), nil)))
	if err != nil {
		t.Fatalf("resolving a complete section: %v", err)
	}
	if cfg.secretID != "AKIDexample" || cfg.sdkAppID != "1400006666" || cfg.region != "ap-guangzhou" ||
		cfg.signName != "Speed" {
		t.Errorf("the scalar items did not survive: %+v", cfg)
	}
	if len(cfg.templates) != len(validTemplates()) {
		t.Errorf("the template table holds %d entries, want %d", len(cfg.templates), len(validTemplates()))
	}
}

// A table that misses any one (purpose, locale) pair is a startup failure naming
// the pair. Without this the purpose could never be sent for in that language.
func TestAMissingTemplateCombinationIsRefused(t *testing.T) {
	for _, missing := range []string{"login/zh-CN", "login/en-US", "mfa/zh-CN", "mfa/en-US", "enroll/zh-CN", "enroll/en-US"} {
		t.Run(missing, func(t *testing.T) {
			templates := validTemplates()
			templates[missing] = ""
			_, err := readBody(t, body(t, section(templates, nil)))
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("an empty %s was accepted: %v", missing, err)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("the failure does not name the pair %q: %v", missing, err)
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
	s := schema()
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
