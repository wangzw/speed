package authztest_test

import (
	"testing"

	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

type probeConfig struct {
	Greeting string `config:"greeting"`
}

var probeDefaults = probeConfig{Greeting: "default"}

func configProbeModule(name, namespace string) core.Module {
	return core.Module{
		Name: name,
		Resources: []any{config.Schema{
			Namespace: namespace,
			Mounts:    []config.Mount{{Value: &probeDefaults}},
			Items:     map[string]config.Item{"greeting": {Description: "what the probe says"}},
		}},
	}
}

// TestConfigReaderServesTheLoadersValues checks that config reader serves the loaders values.
func TestConfigReaderServesTheLoadersValues(t *testing.T) {
	reader := authztest.ConfigReader(t, configProbeModule("probe", "probe"), `{"probe": {"greeting": "hello"}}`)
	var got probeConfig
	if err := reader.Decode("probe", &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Greeting != "hello" {
		t.Errorf("the reader serves %q, want the body's %q", got.Greeting, "hello")
	}

	empty := authztest.ConfigReader(t, configProbeModule("probe", "probe"), `{}`)
	var def probeConfig
	if err := empty.Decode("probe", &def); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if def.Greeting != "default" {
		t.Errorf("the reader serves %q for an absent item, want the declared default", def.Greeting)
	}
}

// TestConfigReaderAllServesEachModuleItsOwnSection checks that config reader all serves each module its own section.
func TestConfigReaderAllServesEachModuleItsOwnSection(t *testing.T) {
	reader := authztest.ConfigReaderAll(t,
		`{"one": {"greeting": "a"}, "two": {"greeting": "b"}}`,
		configProbeModule("one", "one"), configProbeModule("two", "two"))
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

type loadFailure struct {
	testing.TB
	failed bool
}

func (l *loadFailure) Helper() {}

func (l *loadFailure) Fatalf(string, ...any) { l.failed = true }

// TestConfigReaderFailsTheCaseOnABodyTheLoaderRefuses checks that config reader fails the case on a body the loader refuses.
func TestConfigReaderFailsTheCaseOnABodyTheLoaderRefuses(t *testing.T) {
	l := &loadFailure{TB: t}
	authztest.ConfigReader(l, configProbeModule("probe", "probe"), `{not json`)
	if !l.failed {
		t.Error("ConfigReader accepted a body that is not JSON")
	}
}
