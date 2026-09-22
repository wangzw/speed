package ratelimit

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// The module registers itself. Importing this package is what puts ratelimit
// in a binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The capability is delivered exclusively: two limiting layers in one assembly
// would each decide on the same request, and which of the two refusals a
// caller saw is not a fact anything could resolve.
//
// There are no construction-time dependencies. The entry point, settings and
// the key-value primitives are all taken up in Init, where every instance
// exists already, so there is no order to arrange and no Requires edge to
// declare. Authentication and tenant resolution are not taken up at all: they
// are read out of the request context.
//
// Where it cannot start — no configuration reader, a section that will not
// decode, no endpoint named — Prepare returns an error and the startup ends
// with it. The stance is StateEnabled and never StateAuto: taking this module
// into a command that configures no endpoint is a mistake, not a request to
// limit nothing.
func Module() core.Module {
	return core.Module{
		Name:      ModuleName,
		Provides:  []core.Provision{{Token: (*Limiter)(nil), Exclusive: true}},
		Resources: []any{schema(), settingsSchema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			// A stance handed back beside a failure is not read, so the failing
			// paths return the zero Enablement with their error.
			cfg, err := configOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if len(cfg.Endpoints) == 0 {
				return core.Enablement{}, fmt.Errorf("%s%s: no listening endpoint to guard: %w",
					diagnosticPrefix, ModuleName, config.ErrMissingRequired)
			}
			if name, twice := duplicateEndpoint(cfg.Endpoints); twice {
				return core.Enablement{}, fmt.Errorf("%s%s: listening endpoint %q is named twice: %w",
					diagnosticPrefix, ModuleName, name, ErrDuplicateEndpoint)
			}
			return stance(), nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			cfg, err := configOf(reg)
			if err != nil {
				return nil, err
			}
			return newLimiter(cfg.Endpoints), nil
		},

		// The layer is wired here and not in New: the entry point, settings and
		// the key-value primitives are taken up at this stage, where every
		// instance exists already.
		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			l, ok := productOf(instance)
			if !ok {
				return nil
			}
			return l.wire(reg)
		},
	}
}

// stance is what this module states in Prepare: StateEnabled, never
// StateAuto.
func stance() core.Enablement { return core.Enablement{State: core.StateEnabled} }

// configOf takes up the configuration reader and decodes this module's
// section.
func configOf(reg *core.Registry) (Config, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return Config{}, fmt.Errorf("%s%s: taking up the configuration reader: %w",
			diagnosticPrefix, ModuleName, err)
	}
	cfg, err := readConfig(reader)
	if err != nil {
		return Config{}, fmt.Errorf("%s%s: reading the configuration: %w",
			diagnosticPrefix, ModuleName, err)
	}
	return cfg, nil
}

// productOf narrows an instance to this module's product. A product of another
// shape means the registry handed this callback something it did not build,
// which is not something a host can cause.
func productOf(instance any) (*limiter, bool) {
	l, ok := instance.(*limiter)
	return l, ok
}
