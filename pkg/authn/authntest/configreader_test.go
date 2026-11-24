package authntest_test

import (
	"testing"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

type probeConfig struct {
	Greeting string `config:"greeting"`
}

var probeDefaults = probeConfig{Greeting: "default"}

func probeModule(name, namespace string) core.Module {
	return core.Module{
		Name: name,
		Resources: []any{config.Schema{
			Namespace: namespace,
			Mounts:    []config.Mount{{Value: &probeDefaults}},
			Items:     map[string]config.Item{"greeting": {Description: "what the probe says"}},
		}},
	}
}

// TestConfigReaderServesTheLoadersValues holds that the reader is the real
// loader's: a value in the body reaches a module that declared the item, and
// a value the body leaves out reads as the declared default.
func TestConfigReaderServesTheLoadersValues(t *testing.T) {
	reader := authntest.ConfigReader(t, probeModule("probe", "probe"), `{"probe": {"greeting": "hello"}}`)
	var got probeConfig
	if err := reader.Decode("probe", &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Greeting != "hello" {
		t.Errorf("the reader serves %q, want the body's %q", got.Greeting, "hello")
	}

	empty := authntest.ConfigReader(t, probeModule("probe", "probe"), `{}`)
	var def probeConfig
	if err := empty.Decode("probe", &def); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if def.Greeting != "default" {
		t.Errorf("the reader serves %q for an absent item, want the declared default", def.Greeting)
	}
}

// TestConfigReaderAllServesEachModuleItsOwnSection holds the several-module
// form: two modules read two sections of one body.
func TestConfigReaderAllServesEachModuleItsOwnSection(t *testing.T) {
	reader := authntest.ConfigReaderAll(t,
		`{"one": {"greeting": "a"}, "two": {"greeting": "b"}}`,
		probeModule("one", "one"), probeModule("two", "two"))
	for path, want := range map[string]string{"one": "a", "two": "b"} {
		var got probeConfig
		if err := reader.Decode(path, &got); err != nil {
			t.Fatalf("decoding %s: %v", path, err)
		}
		if got.Greeting != want {
			t.Errorf("%s reads %q, want %q", path, got.Greeting, want)
		}
	}
}

// loadFailure stands in for the handle a case gave ConfigReader, with the real
// one underneath for the directory and the context, so that a body the loader
// refuses can be observed as the failure ConfigReader reports.
type loadFailure struct {
	testing.TB
	failed bool
}

func (l *loadFailure) Helper() {}

func (l *loadFailure) Fatalf(string, ...any) { l.failed = true }

// TestConfigReaderFailsTheCaseOnABodyTheLoaderRefuses holds that a malformed
// body is a failure of the case and not a reader that silently serves
// defaults.
func TestConfigReaderFailsTheCaseOnABodyTheLoaderRefuses(t *testing.T) {
	l := &loadFailure{TB: t}
	authntest.ConfigReader(l, probeModule("probe", "probe"), `{not json`)
	if !l.failed {
		t.Error("ConfigReader accepted a body that is not JSON")
	}
}
