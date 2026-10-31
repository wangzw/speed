package config

import (
	"fmt"
	"strings"
	"testing"
)

// layerLoader declares one item that accepts every source, so a case can put a
// value on one layer at a time and ask which layer took effect.
func layerLoader(t *testing.T) *loader {
	t.Helper()
	return loadOver(t,
		identifying("host", HostIdentity{Prefix: "MYAPP"}),
		declaring("cache", Schema{
			Namespace: "cache",
			Mounts:    []Mount{{Value: &cacheOptions{}}},
			Items: map[string]Item{
				"addr": {
					Origins:  OriginPrimary | OriginEnv | OriginFlag,
					FlagName: "cache-addr",
				},
			},
		}),
	)
}

// TestEffectiveLayerReportsTheDefaultLayer pins the bottom of the stack: an
// item no source gave a value keeps the layer its prototype declared.
func TestEffectiveLayerReportsTheDefaultLayer(t *testing.T) {
	layer, err := EffectiveLayer(layerLoader(t).Reader(), "cache.addr")
	if err != nil {
		t.Fatalf("asking for the layer failed: %v", err)
	}
	if layer != LayerDefault {
		t.Fatalf("cache.addr reports layer %v, want LayerDefault", layer)
	}
}

// TestEffectiveLayerReportsThePrimarySource pins the layer under the
// environment and the command line.
func TestEffectiveLayerReportsThePrimarySource(t *testing.T) {
	l := layerLoader(t)
	givePrimaryWhole(t, l, map[string]any{"cache": map[string]any{"addr": "from-file"}})
	layer, err := EffectiveLayer(l.Reader(), "cache.addr")
	if err != nil {
		t.Fatalf("asking for the layer failed: %v", err)
	}
	if layer != LayerPrimary {
		t.Fatalf("cache.addr reports layer %v, want LayerPrimary", layer)
	}
}

// TestEffectiveLayerReportsTheEnvironment pins the layer between the primary
// source and the command line.
func TestEffectiveLayerReportsTheEnvironment(t *testing.T) {
	t.Setenv("MYAPP_CACHE__ADDR", "from-env")
	layer, err := EffectiveLayer(layerLoader(t).Reader(), "cache.addr")
	if err != nil {
		t.Fatalf("asking for the layer failed: %v", err)
	}
	if layer != LayerEnvironment {
		t.Fatalf("cache.addr reports layer %v, want LayerEnvironment", layer)
	}
}

// TestEffectiveLayerReportsTheCommandLine pins the top of the stack.
func TestEffectiveLayerReportsTheCommandLine(t *testing.T) {
	l := layerLoader(t)
	if err := giveFlags(t, l, []string{"cache"}, "--cache-addr=from-flag"); err != nil {
		t.Fatalf("binding the command line failed: %v", err)
	}
	layer, err := EffectiveLayer(l.Reader(), "cache.addr")
	if err != nil {
		t.Fatalf("asking for the layer failed: %v", err)
	}
	if layer != LayerCommandLine {
		t.Fatalf("cache.addr reports layer %v, want LayerCommandLine", layer)
	}
}

// TestEffectiveLayerRejectsAPathNoModuleDeclares pins that an undeclared path
// is an error: it has no input item, so no layer gave it a value and the
// default layer would be an answer about something that does not exist.
func TestEffectiveLayerRejectsAPathNoModuleDeclares(t *testing.T) {
	layer, err := EffectiveLayer(layerLoader(t).Reader(), "cache.missing")
	if err == nil {
		t.Fatalf("an undeclared path reported layer %v, want an error", layer)
	}
	if !strings.Contains(err.Error(), "cache.missing") {
		t.Fatalf("the error reads %q, which does not name the path", err)
	}
	if layer == LayerDefault {
		t.Fatal("an undeclared path fell back to the default layer")
	}
}

// TestEffectiveLayerReportsOnlyTheLayerOfASensitiveItem pins that a sensitive
// item is answerable: the query returns the layer and nothing that could carry
// the value, which is what lets an operator place a credential without reading
// it.
func TestEffectiveLayerReportsOnlyTheLayerOfASensitiveItem(t *testing.T) {
	t.Setenv("MYAPP_VAULT__PIN", secret)
	l := loadOver(t,
		identifying("host", HostIdentity{Prefix: "MYAPP"}),
		declaring("vault", Schema{
			Namespace: "vault",
			Mounts:    []Mount{{Value: &secretOptions{}}},
			Items: map[string]Item{
				"pin": {Sensitive: true, Description: "the unlock PIN"},
			},
		}),
	)
	layer, err := EffectiveLayer(l.Reader(), "vault.pin")
	if err != nil {
		t.Fatalf("asking for a sensitive item's layer failed: %v", err)
	}
	if layer != LayerEnvironment {
		t.Fatalf("vault.pin reports layer %v, want LayerEnvironment", layer)
	}
	if rendered := fmt.Sprint(layer); strings.Contains(rendered, secret) {
		t.Fatalf("the answer reads %q, and it carries the sensitive value", rendered)
	}
}
