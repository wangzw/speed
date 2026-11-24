// Package stdout exports telemetry to the process's standard output.
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
		// No Close: what this module hands over is driven by the providers the
		// assembler built and released by the assembler once both providers have
		// flushed.
	}
}

func prepare(_ context.Context, reg *core.Registry) (core.Enablement, error) {
	if _, err := read(reg); err != nil {
		return core.Enablement{}, err
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

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
