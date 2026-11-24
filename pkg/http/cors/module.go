package cors

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It delivers no capability.
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

		// The layer is wired here and not in New: the entry point is taken up at this stage, where every instance exists already.
		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			b, ok := instance.(*bridge)
			if !ok {
				return nil
			}
			return b.wire(reg)
		},
	}
}

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

type bridge struct {
	layers []endpointLayer
}

// wire registers each endpoint's layer on the endpoint. A name the entry point never declared is an error: the two lists are maintained apart, and a name that exists here but not there is a startup failure rather than a layer bound nowhere.
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
