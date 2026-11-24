// Package oidc is the enterprise OIDC login method: the first factor that proves an external identity by an authorization-code flow against a tenant's identity provider. Every outbound request goes through the restricted egress client, and the provider cache is dropped on authn.idp.changed.
package oidc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/egress"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command whose tenants sign in through an OpenID Connect identity provider.
const ModuleName = "authn.sso.oidc"

// ConfigNamespace is where this module's input item is mounted, which is the module name.
const ConfigNamespace = ModuleName

const methodName = "oidc"

const diagnosticPrefix = "authn: "

func init() { core.ProcessRegistry.Register(Module()) }

var _ authn.LoginMethod = (*product)(nil)

// Module is the descriptor a host registers. It requires the enterprise provider register and the account module, takes the logging capability and settings store as optional, and delivers the login method without claiming it. Prepare reads the module's own enable switch.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*authn.SSOProviders)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides:  []core.Provision{{Token: (*authn.LoginMethod)(nil)}},
		Resources: []any{schema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			return prepare(reader)
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newProduct(reg)
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.wire(reg)
		},
	}
}

func prepare(reader config.Reader) (core.Enablement, error) {
	enabled, err := readEnabled(reader)
	if err != nil {
		return core.Enablement{}, err
	}
	if !enabled {
		return core.Enablement{
			State: core.StateDisabled,
			Reason: "authn.sso.oidc.enabled is off, so this run offers no enterprise OIDC sign-in " +
				"and the method is not delivered",
		}, nil
	}
	if _, err := readConfig(reader); err != nil {
		return core.Enablement{}, err
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

type product struct {
	redirect redirectBase
	client   *http.Client
	cache    *providerCache
	sso      authn.SSOProviders
	settings *reader

	meters *meters
	audit  audit.Audit
	bus    event.Bus
	logger *slog.Logger
}

func newProduct(reg *core.Registry) (*product, error) {
	reader, err := readerOf(reg)
	if err != nil {
		return nil, err
	}
	cfg, err := readConfig(reader)
	if err != nil {
		return nil, fmt.Errorf("%s%s: %w", diagnosticPrefix, ModuleName, err)
	}
	sso, err := core.Resolve[authn.SSOProviders](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the enterprise provider register: %w", diagnosticPrefix, ModuleName, err)
	}
	if _, aerr := core.Resolve[authn.Accounts](reg); aerr != nil {
		return nil, fmt.Errorf("%s%s: taking up the account module: %w", diagnosticPrefix, ModuleName, aerr)
	}
	store, err := optional[settings.Store](reg, "the settings store")
	if err != nil {
		return nil, err
	}
	client := egress.New(egress.Options{AllowLoopback: cfg.allowPrivateEgress, RootCAs: cfg.rootCAs})
	client.Transport = observability.Transport(log.Transport(client.Transport))
	settings := newReader(store, log.Default())
	return build(cfg, sso, client, settings), nil
}

func build(cfg resolved, sso authn.SSOProviders, client *http.Client, settings *reader) *product {
	logger := log.Default()
	return &product{
		redirect: cfg.redirect,
		client:   client,
		cache:    newProviderCache(client, settings),
		sso:      sso,
		settings: settings,
		logger:   logger,
	}
}

func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
		p.settings.logger = p.logger
	}
	store, err := optional[settings.Store](reg, "the settings store")
	if err != nil {
		return err
	}
	p.settings.store = store
	if p.audit, err = optional[audit.Audit](reg, "the audit capability"); err != nil {
		return err
	}
	if p.bus, err = optional[event.Bus](reg, "the event bus"); err != nil {
		return err
	}
	if provider, err := optional[observability.MeterProvider](reg, "the metric capability"); err != nil {
		return err
	} else if provider != nil {
		if p.meters, err = newMeters(provider); err != nil {
			return fmt.Errorf("%s%s: creating the instruments: %w", diagnosticPrefix, ModuleName, err)
		}
	}
	if p.bus != nil {
		p.subscribe(p.bus)
	}
	return nil
}

func optional[T any](reg *core.Registry, what string) (T, error) {
	var zero T
	v, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return v, nil
	case errors.Is(err, core.ErrMissingProvider):
		return zero, nil
	default:
		return zero, fmt.Errorf("%s%s: taking up %s: %w", diagnosticPrefix, ModuleName, what, err)
	}
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: the redirect target and the egress relaxation come from configuration, and no "+
			"reader was handed to the registry before the assembly began: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}
