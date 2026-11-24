// Package catalog is the engine of the i18n capability: it collects the message
// catalogs modules declare, merges them into one index, and delivers Localizer,
// Negotiator and Resolver. It registers the module named "i18n".
package catalog

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
)

// ModuleName is this module's name in the registry.
const ModuleName = "i18n"

func init() { core.ProcessRegistry.Register(Module()) }

var (
	_ i18n.Localizer  = (*engine)(nil)
	_ i18n.Negotiator = (*engine)(nil)
	_ i18n.Resolver   = (*engine)(nil)
)

// Module is this module's descriptor. The three capabilities are exclusive; the
// Profile dependency is optional and non-exclusive.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*i18n.Profile)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*i18n.Localizer)(nil), Exclusive: true},
			{Token: (*i18n.Negotiator)(nil), Exclusive: true},
			{Token: (*i18n.Resolver)(nil), Exclusive: true},
		},
		Resources: []any{schema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			cfg, err := configuration(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if err := validate(core.Resources[i18n.Catalog](reg), cfg.DefaultLocale, cfg.CatalogsDir); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			cfg, err := configuration(reg)
			if err != nil {
				return nil, err
			}
			return newEngine(core.Resources[i18n.Catalog](reg), core.ResolveAll[i18n.Profile](reg), cfg.DefaultLocale, cfg.CatalogsDir)
		},
	}
}

func configuration(reg *core.Registry) (resolved, error) {
	reader, err := readerOf(reg)
	if err != nil {
		return resolved{}, err
	}
	return readConfig(reader)
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: the default locale is read from configuration, and no reader was handed to "+
			"the registry before the assembly began: %w", ModuleName, err)
	}
	return reader, nil
}
