package config

import (
	"fmt"
	"strings"
	"testing"
)

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

// TestEffectiveLayerReportsTheDefaultLayer checks that effective layer reports the default layer.
func TestEffectiveLayerReportsTheDefaultLayer(t *testing.T) {
	layer, err := EffectiveLayer(layerLoader(t).Reader(), "cache.addr")
	if err != nil {
		t.Fatalf("asking for the layer failed: %v", err)
	}
	if layer != LayerDefault {
		t.Fatalf("cache.addr reports layer %v, want LayerDefault", layer)
	}
}

// TestEffectiveLayerReportsThePrimarySource checks that effective layer reports the primary source.
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

// TestEffectiveLayerReportsTheEnvironment checks that effective layer reports the environment.
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

// TestEffectiveLayerReportsTheCommandLine checks that effective layer reports the command line.
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

// TestEffectiveLayerRejectsAPathNoModuleDeclares checks that effective layer rejects a path no module declares.
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

// TestEffectiveLayerReportsOnlyTheLayerOfASensitiveItem checks that effective layer reports only the layer of a sensitive item.
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
