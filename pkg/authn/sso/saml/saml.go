// Package saml carries the SAML 2.0 enterprise single sign-on method: the first
// factor that proves an external identity by a signed assertion a tenant's
// identity provider issues, together with the service provider metadata an IdP
// is registered against and the on-demand refresher of that IdP's metadata.
//
// The method answers with a Step and does nothing else. In Begin it reads the
// provider row, builds an AuthnRequest and answers the redirect the user agent
// leaves for. In Answer it takes the POSTed SAMLResponse and runs the design's
// ordered checks: it never calls the library's RetrieveAssertionInfo, which uses
// the first assertion only, does not check InResponseTo and skips a comparison
// against an empty issuer or destination. It validates the response's signature
// in a single parse, then checks the destination, the request identifiers, the
// assertion count, the conditions, the issuer and confirmation, and finally
// records the assertion's identifier on the register's replay line. It touches
// no HTTP route of the engine, no session, token or flow record, and it never
// writes a value the client sent into an answer.
//
// Every outbound request the method makes goes through the restricted egress
// client, because the IdP's URLs come from a tenant administrator's input. The
// metadata refresher fetches an IdP's metadata through the same client.
//
// A host takes the method in by writing ModuleName into a command alongside the
// login engine and the enterprise provider register. It delivers the login
// method and the metadata refresher non-exclusively, so it stands beside the
// other methods and refreshers.
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

// methodName is the name this method reports. It is a constant of the code: it
// appears in routes, flow records and metric attributes, and a published name
// never changes.
const methodName = "saml"

// providerProtocol is the value of a provider row's Protocol this method
// answers.
const providerProtocol = "saml"

// diagnosticPrefix starts every line the authentication capability writes to the
// startup diagnostics, which have several writers.
const diagnosticPrefix = "authn: "

// defaultEndpoint is the endpoint this module's fragment is declared on: the
// account endpoint under the gateway's base path, which is where the reference
// assembly mounts the authentication capability's own routes. A resource is a
// static declaration fixed before configuration is read, so the field carries
// this default; the merged document takes it as the endpoint of these
// operations. A deployment that mounts the routes elsewhere is drift, which the
// fragment is allowed to have and which produces no failure signal (see pkg/ops).
const defaultEndpoint = "account"

//go:embed openapi/saml.json
var samlFragment []byte

// product must satisfy the capabilities it delivers.
var (
	_ authn.LoginMethod          = (*product)(nil)
	_ authn.SSOMetadataRefresher = (*product)(nil)
)

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The enterprise provider register, the account reads and the route mounter are
// required, and each is delivered exclusively by another module, so a command
// that names this one without them fails in resolution. The settings store, the
// event bus, the audit capability and the metric capability are taken up in
// Init, each with a stated behaviour for its absence, so none is declared. The
// logging capability is declared as an optional requirement: the declaration
// is what orders this module's construction after the logging capability's,
// which is what lets the logger taken up in Init be the configured one. The key
// and certificate paths are read from configuration in New, which is why the
// configuration reader is resolved where it is used.
//
// The login method and the metadata refresher are delivered without being
// claimed, which is what lets several methods and refreshers stand side by side.
// The stance is StateEnabled and never StateAuto: the module is taken in at run
// level and has no way to stand itself down.
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
			speedhttp.Spec{Endpoint: defaultEndpoint, Prefix: "api", Version: "v1", Document: samlFragment},
		},

		// Prepare states the stance alone. The service provider's key comes from
		// configuration and is read in New: this module is taken in at run level
		// and has no way to stand itself down, so its configuration is validated
		// once, when the instance is built.
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
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

// productOf narrows an instance to this module's product. An instance of another
// shape is one a host supplied for this module, and the callbacks leave it
// alone.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// product is the module's instance.
type product struct {
	// Required capabilities, taken up by New.
	providers authn.SSOProviders
	accounts  authn.Accounts
	mounter   mount.Mounter

	// The restricted egress client every outbound call goes through, and the
	// service provider key the whole deployment shares.
	client *http.Client
	sp     resolved

	// Optional capabilities, taken up by Init and read-only afterwards.
	assertions assertionLog
	store      settings.Store
	audit      audit.Audit
	bus        event.Bus
	meters     meters
	logger     *slog.Logger
	settings   *reader

	// Assembled by Init.
	cache *spCache
	clock clock.Source
}

// newProduct takes up the required capabilities, reads the service provider key
// configuration and builds the restricted egress client.
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
	// The restricted client is wrapped from outside, so the metadata refresh and
	// every other outbound request carries the span of the call's context across
	// the hop; internal/egress offers no constructor of its own for it.
	client.Transport = observability.Transport(client.Transport)
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

// wire runs in Init, where every instance exists.
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

// optional takes up a capability whose absence is a legal assembly.
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

// diagnose writes the startup lines: whether the service provider key is
// configured and whether the audit and event capabilities are present.
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

// registerConfig is the part of the enterprise provider register's configuration
// this module reads across namespaces: the switch that admits loopback addresses
// to the restricted egress client.
type registerConfig struct {
	AllowPrivateEgress bool `config:"allow-private-egress"`
}

// readAllowPrivateEgress reads the register's egress relaxation. A namespace
// that cannot be read and a value that is off both leave the client refusing
// loopback addresses, the strict direction.
func readAllowPrivateEgress(reader config.Reader) (bool, error) {
	var rc registerConfig
	if err := reader.Decode("authn.sso", &rc); err != nil {
		return false, fmt.Errorf("%s%s: reading the register's egress switch: %w", diagnosticPrefix, ModuleName, err)
	}
	return rc.AllowPrivateEgress, nil
}
