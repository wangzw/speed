// Package stdout exports telemetry to the process's standard output.
//
// It is the backend a development machine uses: nothing has to be running to
// receive the signals, and what was produced is visible in the terminal. On a
// deployment it writes the signals into the same stream as everything else the
// process prints, which is why it is not the backend a deployment picks.
//
// It delivers both exporter capabilities, non-exclusively, so that it can be
// configured alongside a remote destination rather than instead of one.
package stdout

import (
	"context"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/observability"
)

// ModuleName is this module's name in the registry, and the namespace its input
// items are mounted under.
const ModuleName = "observability.stdout"

// init registers this module with the process registry, so a host that imports
// the package has it available.
func init() { core.ProcessRegistry.Register(Module()) }

// Config is what this module accepts from configuration.
type Config struct {
	// Pretty writes the signal as indented JSON rather than one object per
	// line, which is easier to read and not something a log shipper can parse.
	Pretty bool `config:"pretty"`
}

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Provides: []core.Provision{
			// Not exclusive: an exporter is a destination, and a host may want
			// several at once. The assembler collects every one of them.
			{Token: (*observability.SpanExporter)(nil)},
			{Token: (*observability.MetricExporter)(nil)},
		},
		Resources: []any{config.Schema{
			Namespace: ModuleName,
			Mounts:    []config.Mount{{Value: &Config{}}},
		}},
		Prepare: prepare,
		New:     newExporters,
		// No Close: what this module hands over is driven and released by the
		// providers the assembler built, which shut their exporters down when
		// they are shut down. Releasing it here as well would be a second
		// shutdown of the same value.
	}
}

// prepare reads this module's own configuration and states that it starts.
func prepare(_ context.Context, reg *core.Registry) (core.Enablement, error) {
	if _, err := read(reg); err != nil {
		return core.Enablement{}, err
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// read decodes this module's namespace.
func read(reg *core.Registry) (*Config, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := reader.Decode(ModuleName, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
