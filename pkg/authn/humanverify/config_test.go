package humanverify

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/internal/configtest"
)

// body renders a whole configuration document holding this module's section. An
// empty section is left out: the loader refuses an empty object at a declared
// section path, and a deployment that states nothing omits the section as well,
// leaving the mounted defaults in force.
func body(t *testing.T, s map[string]any) string {
	t.Helper()
	doc := map[string]any{}
	if len(s) > 0 {
		doc["authn"] = map[string]any{"humanverify": s}
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// readBody loads a document with the real configuration loader and decodes this
// module's section, returning the configuration the module runs on or the error
// the loader refused the startup with.
func readBody(t *testing.T, document string) (resolved, error) {
	t.Helper()
	reader := configtest.Reader(t, document, Module())
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}

// The gate is off without a configuration file, which is the product's default.
func TestTheModeDefaultsToOff(t *testing.T) {
	cfg, err := readBody(t, body(t, nil))
	if err != nil {
		t.Fatalf("resolving an empty section: %v", err)
	}
	if cfg.Mode != ModeOff {
		t.Errorf("the default mode is %q, want %q", cfg.Mode, ModeOff)
	}
}

// Every mode in the closed set resolves and survives the decode.
func TestEveryModeInTheClosedSetResolves(t *testing.T) {
	for _, mode := range []string{ModeOff, ModePoW, ModeTurnstile} {
		t.Run(mode, func(t *testing.T) {
			cfg, err := readBody(t, body(t, map[string]any{"mode": mode}))
			if err != nil {
				t.Fatalf("resolving mode %q: %v", mode, err)
			}
			if cfg.Mode != mode {
				t.Errorf("the resolved mode is %q, want %q", cfg.Mode, mode)
			}
		})
	}
}

// A mode outside the closed set is refused, and the failure names the key to
// correct rather than silently running the gate off.
func TestAnUnknownModeIsRefusedNamingTheKey(t *testing.T) {
	for _, mode := range []string{"captcha", "PoW", "turnstile ", "none"} {
		t.Run(mode, func(t *testing.T) {
			_, err := readBody(t, body(t, map[string]any{"mode": mode}))
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("mode %q resolved with %v, want ErrInvalidConfig", mode, err)
			}
			if !strings.Contains(err.Error(), ConfigNamespace+"."+modeItem) {
				t.Errorf("the failure %q does not name %s.%s", err, ConfigNamespace, modeItem)
			}
			if !strings.Contains(err.Error(), mode) {
				t.Errorf("the failure %q does not name the value %q", err, mode)
			}
		})
	}
}

// The declaration carries the mode under the module's own namespace and mounts
// the default off, so a section that omits it runs the gate off.
func TestTheSchemaDeclaresTheModeUnderTheNamespace(t *testing.T) {
	s, ok := Module().Resources[0].(config.Schema)
	if !ok {
		t.Fatalf("the module carries %T, want a config.Schema", Module().Resources[0])
	}
	if s.Namespace != ConfigNamespace {
		t.Errorf("the schema namespace is %q, want %q", s.Namespace, ConfigNamespace)
	}
	if _, ok := s.Items[modeItem]; !ok {
		t.Errorf("the schema declares no %q item", modeItem)
	}
	if len(s.Mounts) != 1 || s.Mounts[0].Value == nil {
		t.Fatalf("the schema mounts %+v, want the default configuration", s.Mounts)
	}
}
