package main

// The notification fixture's run-time decision: the type is part of the
// compile-time ceiling, and whether this run dispatches it is stated by the
// fixture's own enable switch. These cases drive the real configuration loader,
// because the switch being absent must read the same way there as it does in a
// run.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
)

// fixtureReader loads the configuration of a run whose primary source is body.
// The registry holds only the loader's own modules and the fixture, so the case
// is not affected by another module's declaration.
func fixtureReader(t *testing.T, body string) config.Reader {
	t.Helper()
	if body == "" {
		body = "{}"
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the primary config source: %v", err)
	}
	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDFIXTURETEST", DefaultLocator: "file://" + path}},
	})
	reg.Register(demoNotificationModule())
	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	return reader
}

// prepareFixture drives the fixture's Prepare over the reader.
func prepareFixture(t *testing.T, reader config.Reader) (core.Enablement, error) {
	t.Helper()
	reg := core.New()
	reg.Register(demoNotificationModule())
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	return demoNotificationModule().Prepare(t.Context(), reg)
}

// TestTheNotificationFixtureStandsDownWhenNotEnabled holds the unconfigured
// case: without the switch, the owner of the notice is absent from the run and
// the startup succeeds.
func TestTheNotificationFixtureStandsDownWhenNotEnabled(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body string
	}{
		{"no section", ""},
		{"switch off", `{"demo":{"notification":{"enabled":false}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := prepareFixture(t, fixtureReader(t, tc.body))
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if got.State != core.StateDisabled {
				t.Errorf("Prepare = %+v, want StateDisabled", got)
			}
			if got.Reason == "" {
				t.Error("the fixture stood down without a reason")
			}
		})
	}
}

// TestTheNotificationFixtureRunsWhenEnabled holds the configured case.
func TestTheNotificationFixtureRunsWhenEnabled(t *testing.T) {
	t.Parallel()
	got, err := prepareFixture(t, fixtureReader(t, `{"demo":{"notification":{"enabled":true}}}`))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

// TestTheNotificationFixtureRefusesAHostWithNoReader holds that a missing
// reader is a host omission, not an unconfigured fixture: it fails the start
// rather than reading as "off" and losing the fixture in silence.
func TestTheNotificationFixtureRefusesAHostWithNoReader(t *testing.T) {
	t.Parallel()
	reg := core.New()
	reg.Register(demoNotificationModule())
	if _, err := demoNotificationModule().Prepare(t.Context(), reg); err == nil {
		t.Fatal("Prepare with no configuration reader succeeded, want an error naming the omission")
	}
}
