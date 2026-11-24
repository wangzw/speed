package ratelimit

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers; the capability is delivered
// exclusively, and there are no construction-time dependencies.
func Module() core.Module {
	return core.Module{
		Name:      ModuleName,
		Provides:  []core.Provision{{Token: (*Limiter)(nil), Exclusive: true}},
		Resources: []any{schema(), settingsSchema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			cfg, err := configOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if len(cfg.Endpoints) == 0 {
				return core.Enablement{}, fmt.Errorf("%s: no listening endpoint to guard: %s is "+
					"empty. List the endpoints whose requests are limited, by the names the entry "+
					"point's configuration gives them: %w",
					ModuleName, endpointsPath, config.ErrMissingRequired)
			}
			if name, twice := duplicateEndpoint(cfg.Endpoints); twice {
				return core.Enablement{}, fmt.Errorf("%s: listening endpoint %q is named twice: %w",
					ModuleName, name, ErrDuplicateEndpoint)
			}
			if name, twice := duplicateEndpoint(cfg.AddressEndpoints); twice {
				return core.Enablement{}, fmt.Errorf("%s: listening endpoint %q is named twice in %s: %w",
					ModuleName, name, addressEndpointsKey, ErrDuplicateEndpoint)
			}
			return stance(), nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			cfg, err := configOf(reg)
			if err != nil {
				return nil, err
			}
			l := newLimiter(cfg.Endpoints)
			l.addressEndpoints = append([]string(nil), cfg.AddressEndpoints...)
			return l, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			l, ok := productOf(instance)
			if !ok {
				return nil
			}
			return l.wire(reg)
		},
	}
}

func stance() core.Enablement { return core.Enablement{State: core.StateEnabled} }

func configOf(reg *core.Registry) (Config, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return Config{}, fmt.Errorf("%s: taking up the configuration reader: %w",
			ModuleName, err)
	}
	cfg, err := readConfig(reader)
	if err != nil {
		return Config{}, fmt.Errorf("%s: reading the configuration: %w",
			ModuleName, err)
	}
	return cfg, nil
}

// productOf narrows an instance to this module's product, leaving a
// host-supplied instance alone.
func productOf(instance any) (*limiter, bool) {
	l, ok := instance.(*limiter)
	return l, ok
}
