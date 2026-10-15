// Package oidc carries the enterprise OIDC login method: the first factor that
// proves an external identity by an OpenID Connect authorization-code flow
// against a tenant's identity provider.
//
// It is one authn.LoginMethod beside the social methods, delivered
// non-exclusively. The provider is data, not a module: it is read from the
// enterprise provider register (authn.SSOProviders) by the identifier the start
// input names, and its client secret is read through Secret alone. Google and
// this method run the same OIDC checks; the difference is where the provider
// comes from.
//
// The method answers with a Step and does nothing else. In Begin it reads the
// provider, draws the state, the PKCE verifier and the nonce, keeps them in the
// flow record's method state and answers the authorization URL. In Answer it
// checks the redirect against the state it drew, exchanges the authorization
// code and verifies the identity token through go-oidc, checking the nonce, the
// authorized party and the subject itself. It touches no HTTP route, session,
// token or flow record: account resolution is the engine's.
//
// Every outbound request goes through the restricted egress client, injected
// through oidc.ClientContext, so a discovery document, a key set and a token
// endpoint can only be reached over https at a public address, each bounded by
// the deadline of the context the engine hands in. The discovered provider and
// its OAuth client are cached per identity provider and version, expiry from
// authn.sso.provider-cache-ttl, and dropped when an authn.idp.changed event for
// that provider arrives; a construction that fails ends that provider's calls
// with an outage and leaves every other provider alone.
//
// A host takes the method in by writing ModuleName into a command alongside the
// login engine and the enterprise provider register.
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

// ConfigNamespace is where this module's input items would be mounted, which is
// the module name. The method declares no item of its own: the identity
// providers are data in the register, and the two items it reads are the
// gateway's and the SSO module's.
const ConfigNamespace = ModuleName

// methodName is the name this method reports. It is a constant of the code: it
// appears in routes, flow records and metric attributes, and a published name
// never changes.
const methodName = "oidc"

// diagnosticPrefix starts every line this module writes to the startup
// diagnostics, which have several writers; it is the authentication capability's
// prefix, so a reader scanning for what authentication did finds both.
const diagnosticPrefix = "authn: "

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// product must satisfy the capability it delivers.
var _ authn.LoginMethod = (*product)(nil)

// Module is the descriptor a host registers.
//
// The enterprise provider register and the account module are required, each
// delivered exclusively by another module, so a command that names this one
// without them fails in resolution. The method reads providers, not accounts:
// account resolution belongs to the login engine, and the account module is
// required by the capability's wiring contract, the same way every login method
// runs beside an account store. The logging capability and the settings store
// are taken up as optional requirements — the settings store so that it is
// initialised first and its snapshot is loaded when a setting is read. The
// event bus, the audit capability and the metric capability are taken up in
// Init, each with a stated behaviour for its absence.
//
// The login method capability is delivered without being claimed, which is what
// lets several methods stand side by side. The stance is StateEnabled and never
// StateAuto: the module is taken in at run level and has no way to stand itself
// down.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*authn.SSOProviders)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{{Token: (*authn.LoginMethod)(nil)}},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if _, err := readConfig(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
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

// productOf narrows an instance to this module's product. The callbacks are also
// handed an instance a host supplied, which can be any value that delivers the
// capability; they act on the module's own product and leave a supplied one as
// it is.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// product is the module's instance. It delivers the login method.
type product struct {
	// redirect is the base the redirect target is built from: the gateway's
	// public address and base path, plus this provider's callback path.
	redirect redirectBase
	// client is the restricted egress client every outbound call goes through.
	client *http.Client
	// cache holds the discovered provider and its OAuth client, per provider.
	cache *providerCache
	// sso is the enterprise provider register.
	sso authn.SSOProviders
	// settings reads the settings the module acts on.
	settings *reader

	// What Init takes up. They are read-only after Init.
	meters *meters
	audit  audit.Audit
	bus    event.Bus
	logger *slog.Logger
}

// newProduct takes up what the descriptor requires and assembles the product.
// The restricted egress client is built here, from the SSO module's developer
// relaxation.
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
	// The account module is required by the capability's wiring contract, though
	// the method resolves no account itself; taking it up holds the assembly to
	// that contract and names the capability when it is absent.
	if _, aerr := core.Resolve[authn.Accounts](reg); aerr != nil {
		return nil, fmt.Errorf("%s%s: taking up the account module: %w", diagnosticPrefix, ModuleName, aerr)
	}
	store, err := optional[settings.Store](reg, "the settings store")
	if err != nil {
		return nil, err
	}
	client := egress.New(egress.Options{AllowLoopback: cfg.allowPrivateEgress})
	// The restricted client is wrapped from outside, so a discovery, key-set or
	// token request carries the span of the call's context across the hop to the
	// identity provider; internal/egress offers no constructor of its own for it.
	client.Transport = observability.Transport(client.Transport)
	settings := newReader(store, log.Default())
	return build(cfg, sso, client, settings), nil
}

// build assembles a product from its parts. The optional parts start absent and
// the logger starts as the process's, so that a product that has not been
// through Init is still one that does not panic.
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

// wire runs in Init, where every instance exists: it takes up the optional
// capabilities and subscribes to the event that drops a cached provider.
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

// optional takes up a capability whose absence is a legal assembly: the zero
// value says it is absent, and any other failure to take it up ends the startup,
// because a capability that is there and cannot be taken up is not an absence.
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

// readerOf takes up the configuration reader. It reads the gateway's public
// address and the SSO module's developer relaxation, so its absence is the
// host's omission and is carried out as it stands.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: the redirect target and the egress relaxation come from configuration, and no "+
			"reader was handed to the registry before the assembly began: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}
