// Package catalog is the engine of the i18n capability. It collects the message
// catalogs that modules declare as i18n.Catalog resources, merges them into one
// index and delivers the Localizer, Negotiator and Resolver capabilities that
// the rest of the system renders and resolves through.
//
// It registers a module named "i18n". A host that wants server-rendered text
// takes it into the command that runs the entry point; without it, i18n.Localizer
// and its siblings have no provider and a module that requires one fails to
// start.
//
// The engine is the one place that binds golang.org/x/text: it parses language
// tags and negotiates a request's preferences with language.Matcher, and the
// interface package stays on the standard library so that a consumer of a locale
// carries no matching library.
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

// init registers this module with the process registry, so a host that imports
// this package can take it into a command by name.
func init() { core.ProcessRegistry.Register(Module()) }

// The engine delivers all three capabilities and is taken up as whichever the
// consumer needs. The assertions are what the registry relies on; a mismatch
// here would be a defect caught at build time rather than at resolution.
var (
	_ i18n.Localizer  = (*engine)(nil)
	_ i18n.Negotiator = (*engine)(nil)
	_ i18n.Resolver   = (*engine)(nil)
)

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
//
// The three capabilities are delivered exclusively: one process renders in one
// set of catalogs, and two engines would leave a consumer unable to tell which
// set it read. The Profile dependency is optional and non-exclusive — the
// bridge to the account store delivers it, and an assembly with no accounts
// resolves no user preference and falls back to the default. The default locale
// is read from configuration, so the descriptor declares the config schema it
// hangs from and nothing else in Resources: this module declares no messages of
// its own, and the http.<status> fallback catalog belongs to the responder that
// needs it.
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

// configuration reads this module's section of the configuration into the
// values it runs on.
func configuration(reg *core.Registry) (resolved, error) {
	reader, err := readerOf(reg)
	if err != nil {
		return resolved{}, err
	}
	return readConfig(reader)
}

// readerOf takes up the configuration reader. Its absence is the host's omission
// and is carried out as it stands, not read as "the default is English".
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: the default locale is read from configuration, and no reader was handed to "+
			"the registry before the assembly began: %w", ModuleName, err)
	}
	return reader, nil
}
