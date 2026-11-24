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

// TestConfigReaderServesTheLoadersValues checks that the reader serves the real
// loader's values: a value in the body reaches its module, an absent one the default.
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

// TestConfigReaderAllServesEachModuleItsOwnSection checks that two modules read
// two sections of one body.
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

// loadFailure is a testing.TB whose Fatalf records the failure instead of
// ending the test, so a refused body can be observed.
type loadFailure struct {
	testing.TB
	failed bool
}

func (l *loadFailure) Helper() {}

func (l *loadFailure) Fatalf(string, ...any) { l.failed = true }

// TestConfigReaderFailsTheCaseOnABodyTheLoaderRefuses checks that a malformed body
// fails the case rather than silently serving defaults.
func TestConfigReaderFailsTheCaseOnABodyTheLoaderRefuses(t *testing.T) {
	l := &loadFailure{TB: t}
	authntest.ConfigReader(l, probeModule("probe", "probe"), `{not json`)
	if !l.failed {
		t.Error("ConfigReader accepted a body that is not JSON")
	}
}
