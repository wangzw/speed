// Package saml carries the SAML 2.0 enterprise single sign-on method, the service provider metadata an IdP is registered against, and the on-demand metadata refresher. It runs its own ordered assertion checks instead of the library's retrieval, and every outbound request goes through the restricted egress client.
package saml

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/authn/internal/egress"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry.
const ModuleName = "authn.sso.saml"

// ConfigNamespace is where this module's input items are mounted, which is the
// module name.
const ConfigNamespace = ModuleName

const methodName = "saml"

const providerProtocol = "saml"

const diagnosticPrefix = "authn: "

const defaultEndpoint = "account"

//go:embed openapi/saml.json
var samlFragment []byte

var samlSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: samlFragment,
}

var (
	_ authn.LoginMethod          = (*product)(nil)
	_ authn.SSOMetadataRefresher = (*product)(nil)
)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It requires the enterprise provider register, the account reads and the route mounter, declares the logging capability as optional to order construction, and delivers the login method and metadata refresher without claiming them.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*authn.SSOProviders)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*mount.Mounter)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authn.LoginMethod)(nil)},
			{Token: (*authn.SSOMetadataRefresher)(nil)},
		},
		Resources: []any{
			configSchema(),
			samlSpec,
			operations,
		},

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
			Reason: "authn.sso.saml.enabled is off, so this run offers no enterprise SAML sign-in " +
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
	providers authn.SSOProviders
	accounts  authn.Accounts
	mounter   mount.Mounter

	client *http.Client
	sp     resolved

	assertions assertionLog
	store      settings.Store
	audit      audit.Audit
	bus        event.Bus
	meters     meters
	logger     *slog.Logger
	settings   *reader

	cache *spCache
	clock clock.Source
}

func newProduct(reg *core.Registry) (*product, error) {
	providers, err := core.Resolve[authn.SSOProviders](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the enterprise provider register: %w", diagnosticPrefix, ModuleName, err)
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the account read capability: %w", diagnosticPrefix, ModuleName, err)
	}
	mounter, err := core.Resolve[mount.Mounter](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the route mounter: %w", diagnosticPrefix, ModuleName, err)
	}
	logs, ok := providers.(assertionLog)
	if !ok {
		return nil, fmt.Errorf("%s%s: the enterprise provider register does not record consumed SAML "+
			"assertions, so an assertion could be replayed; the register that owns the replay line has "+
			"to record the identifier on it", diagnosticPrefix, ModuleName)
	}
	reader, err := readerOf(reg)
	if err != nil {
		return nil, err
	}
	sp, err := readConfig(reader)
	if err != nil {
		return nil, err
	}
	allowLoopback, err := readAllowPrivateEgress(reader)
	if err != nil {
		return nil, err
	}
	client := egress.New(egress.Options{AllowLoopback: allowLoopback})
	client.Transport = observability.Transport(log.Transport(client.Transport))
	return &product{
		providers:  providers,
		accounts:   accounts,
		mounter:    mounter,
		client:     client,
		sp:         sp,
		assertions: logs,
		logger:     log.Default(),
	}, nil
}

func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
	}
	var err error
	if p.store, err = optional[settings.Store](reg, "the settings store"); err != nil {
		return err
	}
	if p.audit, err = optional[audit.Audit](reg, "the audit capability"); err != nil {
		return err
	}
	if p.bus, err = optional[event.Bus](reg, "the event bus"); err != nil {
		return err
	}
	p.settings = newReader(p.store, p.logger)
	p.cache = newSPCache()

	if provider, err := optional[observability.MeterProvider](reg, "the metric capability"); err != nil {
		return err
	} else if provider != nil {
		if p.meters, err = newMeters(provider); err != nil {
			return fmt.Errorf("%s%s: creating the instruments: %w", diagnosticPrefix, ModuleName, err)
		}
	}

	p.mountMetadata()
	p.subscribe()
	p.diagnose()
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

func (p *product) diagnose() {
	if !p.sp.configured() {
		p.logger.Info(diagnosticPrefix + ModuleName + ": no service provider key is configured, so no " +
			"AuthnRequest is signed and no encrypted assertion is decrypted")
	} else if p.sp.nextDER != nil {
		p.logger.Info(diagnosticPrefix + ModuleName + ": a next service provider certificate is published " +
			"beside the current one, for a rotation in progress")
	}
	if p.audit == nil {
		p.logger.Info(diagnosticPrefix + ModuleName + ": no audit capability takes part, so a rejected " +
			"assertion writes no audit row")
	}
	if p.bus == nil {
		p.logger.Info(diagnosticPrefix + ModuleName + ": no event bus takes part, so a change of a " +
			"provider does not drop the cached service provider")
	}
}

type registerConfig struct {
	AllowPrivateEgress bool `config:"allow-private-egress"`
}

func readAllowPrivateEgress(reader config.Reader) (bool, error) {
	var rc registerConfig
	if err := reader.Decode("authn.sso", &rc); err != nil {
		return false, fmt.Errorf("%s%s: reading the register's egress switch: %w", diagnosticPrefix, ModuleName, err)
	}
	return rc.AllowPrivateEgress, nil
}
