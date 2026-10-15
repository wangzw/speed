package cors

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// init registers this module with the process registry, the way the other
// modules do, so a host that imports this package can take it into a command by
// name. Importing it alone registers no layer: a registered module is available,
// and only the ones a command names take part in a run.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// It delivers no capability. Nothing orders a layer against CORS, and CORS
// orders against nothing: its position is settled entirely by its stage and its
// position within it, so it needs neither a token of its own nor an anchor in
// another layer. The layer likewise declares no Provides, and the chain listing
// shows it by name alone.
//
// There are no construction-time dependencies. The entry point is taken up in
// Init, where every instance exists already, so there is no Requires edge to
// declare.
//
// Where it cannot start — no configuration reader, an empty endpoint set, a
// policy the library would refuse — Prepare returns an error and the startup
// ends with it. The stance is StateEnabled and never StateAuto: a command that
// takes this module in and configures no endpoint is a mistake, not a request to
// serve no CORS headers.
func Module() core.Module {
	return core.Module{
		Name:      ModuleName,
		Resources: []any{schema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			cfg, err := configOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if err := cfg.validate(); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			cfg, err := configOf(reg)
			if err != nil {
				return nil, err
			}
			layers, err := cfg.build()
			if err != nil {
				return nil, err
			}
			return &bridge{layers: layers}, nil
		},

		// The layer is wired here and not in New: the entry point is taken up at
		// this stage, where every instance exists already.
		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			b, ok := instance.(*bridge)
			if !ok {
				return nil
			}
			return b.wire(reg)
		},
	}
}

// configOf takes up the configuration reader and decodes this module's section.
func configOf(reg *core.Registry) (Config, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return Config{}, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	cfg, err := readConfig(reader)
	if err != nil {
		return Config{}, fmt.Errorf("%s: reading the configuration: %w", ModuleName, err)
	}
	return cfg, nil
}

// bridge is this module's product: the layer built for each configured endpoint.
// It delivers no capability, so nothing outside reads it; it exists so that an
// instance the host supplied for this module can be told from this module's own
// product, the way every module here tells them apart.
type bridge struct {
	layers []endpointLayer
}

// wire registers each endpoint's layer on the endpoint. A name the entry point
// never declared is an error: the two lists are maintained apart, and a name that
// exists here but not there is a startup failure rather than a layer bound
// nowhere.
func (b *bridge) wire(reg *core.Registry) error {
	router, err := core.Resolve[speedhttp.Router](reg)
	if err != nil {
		return fmt.Errorf("%s: taking up the entry point: %w", ModuleName, err)
	}
	for _, l := range b.layers {
		endpoint, err := router.Endpoint(l.name)
		if err != nil {
			return fmt.Errorf("%s: endpoint %q: %w", ModuleName, l.name, err)
		}
		endpoint.Use(speedhttp.Middleware{
			Name:    ModuleName,
			Stage:   speedhttp.StageGuard,
			Order:   speedhttp.OrderOutermost,
			Handler: l.handler,
		})
	}
	return nil
}
