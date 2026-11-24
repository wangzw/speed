package tencent

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/sms"
)

func section(override map[string]any) map[string]any {
	out := map[string]any{
		"secret-id":  "AKIDexample",
		"secret-key": "secret-key-example",
		"sdk-app-id": "1400006666",
		"region":     "ap-guangzhou",
		"sign-name":  "Speed",
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

func body(t *testing.T, s map[string]any) string {
	t.Helper()
	doc := map[string]any{"sms": map[string]any{"tencent": s}}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func readBody(t *testing.T, document string) (resolved, error) {
	t.Helper()
	reader := configtest.Reader(t, document, Module())
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	if _, err := stance(&cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve(), nil
}

// A section with every item resolves into the credentials and the signature the module runs on.
func TestACompleteSectionResolves(t *testing.T) {
	cfg, err := readBody(t, body(t, section(nil)))
	if err != nil {
		t.Fatalf("resolving a complete section: %v", err)
	}
	if cfg.secretID != "AKIDexample" || cfg.sdkAppID != "1400006666" || cfg.region != "ap-guangzhou" ||
		cfg.signName != "Speed" {
		t.Errorf("the scalar items did not survive: %+v", cfg)
	}
}

// Each item is required: a section that leaves one out while the rest are set is half-configured and fails the startup.
func TestEachItemIsRequired(t *testing.T) {
	for _, item := range []string{"secret-id", "secret-key", "sdk-app-id", "region", "sign-name"} {
		t.Run(item, func(t *testing.T) {
			_, err := readBody(t, body(t, section(map[string]any{item: nil})))
			if !errors.Is(err, sms.ErrInvalidConfig) {
				t.Fatalf("a section with no %s was accepted: %v", item, err)
			}
		})
	}
}

// The declaration marks the secret sensitive, which keeps the key out of help output.
func TestTheSchemaDeclaresTheSensitiveItems(t *testing.T) {
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
	for _, item := range []string{secretIDItem, secretKeyItem, sdkAppIDItem, regionItem, signNameItem} {
		if s.Items[item].Required {
			t.Errorf("the item %q is declared required; the stance judges an unconfigured carrier instead", item)
		}
	}
}
