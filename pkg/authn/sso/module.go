// Package sso is the enterprise single sign-on module of the authentication
// capability: the register of the identity providers (IdPs) of tenants, the
// ownership proof of their email domains and the domain discovery route. It
// holds the three tables authn_idps, authn_idp_domains and authn_saml_assertions
// and delivers authn.SSOProviders exclusively.
//
// An IdP is data and not a module: it appears at run time, its number is not
// fixed and it carries a secret, so it lives in a table rather than in the
// settings or the configuration. The client secret of an OIDC provider sits in
// an encrypted column and is handed out only through Secret; it reaches no read,
// no log, no event and no metric. An email domain an IdP discovers logins by is
// proven by a DNS TXT record before it takes part, and a domain another provider
// has already proven is refused.
//
// The module sweeps its own tables — the expired SAML assertion identifiers and
// the domain claims left unverified — in a bounded goroutine started in Start
// and stopped in Stop. It writes no management route: the write surface is
// exposed as HTTP only through the administration module, after a Decider has
// allowed the action authn.idp.manage.
package sso

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/authn/internal/model"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command whose tenants keep identity providers.
const ModuleName = "authn.sso"

// ConfigNamespace is where this module's input items are mounted, which is the
// module name. The settings this module declares hang from the same namespace.
const ConfigNamespace = ModuleName

// The tables this module owns and sweeps.
const (
	idpTable       = "authn_idps"
	domainTable    = "authn_idp_domains"
	assertionTable = "authn_saml_assertions"
)

// admissionModule is the module that enforces a tenant's requirement of SSO. It
// is named here so that a deployment without it is told, once, that the
// requirement is executed by nobody.
const admissionModule = "tenant.admission"

// diagnosticPrefix starts every line this module writes to the startup
// diagnostics, which have several writers.
const diagnosticPrefix = "authn: "

// defaultEndpoint is the endpoint this module's fragment is declared on: the
// account endpoint under the gateway's base path, which is where the reference
// assembly mounts the authentication capability's own routes. A resource is a
// static declaration fixed before configuration is read, so the field carries
// this default; the merged document takes it as the endpoint of these
// operations. A deployment that mounts the routes elsewhere is drift, which the
// fragment is allowed to have and which produces no failure signal (see pkg/ops).
const defaultEndpoint = "account"

//go:embed openapi/sso.json
var ssoFragment []byte

// ssoSpec is the API root this module's fragment and routes hang off. Its
// prefix and version are the common ones; the fragment writes the capability's
// own segment, so a route's declared path begins with the part of the mounter's
// base that lies below this root.
var ssoSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: ssoFragment,
}

// dnsTimeout bounds one TXT lookup. It is the deadline a domain verification and
// a re-verification each take.
const dnsTimeout = 5 * time.Second

//go:embed migrations
var migrationRoot embed.FS

// migrationsFS is migrationRoot with the wrapping directory removed, so that the
// tree the database module sees has one directory per dialect at its root.
var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive above makes this unreachable: the directory is
		// part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

// init registers this module with the process registry. Importing the package is
// what puts the capability into a binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// The product must deliver what the descriptor declares.
var _ authn.SSOProviders = (*product)(nil)

// Module is the descriptor a host registers.
//
// The database and the mounter are required, and each is delivered exclusively
// by another module, so a command that names this one without them fails in
// resolution. The settings store is required as optional only so that it is
// initialised before this one and its snapshot is loaded when Init subscribes to
// the settings change event; the event bus, the audit capability, the
// login-method switch and the logging capability are taken up in Init, each with
// a stated behaviour for its absence, which is a legal assembly.
//
// SSOProviders is delivered exclusively and the stance is StateEnabled and never
// StateAuto: an exclusive provider that states StateAuto is the cell resolution
// disables.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*mount.Mounter)(nil)},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authn.SSOProviders)(nil), Exclusive: true},
		},
		Resources: []any{
			configSchema(),
			settingsSchema(),
			db.Migrations{FS: migrationsFS},
			ssoSpec,
			operations,
		},

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

		// New takes up what the descriptor requires and reads no table of its
		// own: this stage runs before the migrations are applied, so on a first
		// start the tables are not there yet.
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

		Start: func(ctx context.Context, reg *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.start(ctx, reg)
		},

		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			if p, ok := productOf(instance); ok {
				return p.stop()
			}
			return nil
		},

		// The handle belongs to the database module, which closes it.
		Close: func(context.Context, *core.Registry, any) error { return nil },
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

// readerOf takes up the configuration reader. Its absence is the host's omission
// and is carried out as it stands, not read as "nothing was configured".
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: the developer egress relaxation is read from configuration, and no "+
			"reader was handed to the registry before the assembly began: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}

// product is the module's instance. It delivers the SSO provider register.
type product struct {
	database db.Database
	handle   *gorm.DB

	register *register
	mounter  mount.Mounter

	// reader reads the configuration at write time: the SAML service provider's
	// key lives in the authn.sso.saml namespace, and whether it is configured
	// decides whether a provider may ask for signed requests.
	reader config.Reader

	// What Init takes up. They are read-only after Init.
	store  settings.Store
	bus    event.Bus
	audit  audit.Audit
	logger *slog.Logger

	// lookup resolves the TXT records of a name. It defaults to the system
	// resolver and is replaced by a test.
	lookup func(ctx context.Context, name string) ([]string, error)

	// delivered is the set of enterprise protocols this run delivers, taken up
	// in Init from the login methods the registry holds. A protocol absent here
	// has no discoverable provider: the answer layer lists a provider only when
	// its protocol is in this set, and an empty set lists none.
	delivered map[string]bool

	// loginSwitch is the run-time switch of the login methods, taken up in Init
	// when the login engine delivers it. It is nil in a process without that
	// engine, where no method can start a flow either, so the discovery filters
	// nothing.
	loginSwitch authn.LoginMethodSwitch

	// allowPrivateEgress is the developer relaxation of the restricted egress
	// client. It is read here only so that the startup diagnostics can name it;
	// the egress client itself is built by the protocol modules.
	allowPrivateEgress bool

	clock clock.Source

	sweeper         *sweeper
	claimsEvery     time.Duration
	assertionsEvery time.Duration
	stopBound       time.Duration
}

// newProduct takes up what the descriptor requires and assembles the product.
func newProduct(reg *core.Registry) (*product, error) {
	reader, err := readerOf(reg)
	if err != nil {
		return nil, err
	}
	cfg, err := readConfig(reader)
	if err != nil {
		return nil, err
	}
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the database: %w", diagnosticPrefix, ModuleName, err)
	}
	mounter, err := core.Resolve[mount.Mounter](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the route mounter: %w", diagnosticPrefix, ModuleName, err)
	}
	return build(database, mounter, cfg, reader), nil
}

// build assembles a product from its parts. The optional parts start absent and
// the logger starts as the process's, so that a product that has not been through
// Init is still one that does not panic. The TXT lookup goes through the
// configured resolver when there is one, and through the system resolver when
// there is none.
func build(database db.Database, mounter mount.Mounter, cfg resolved, reader config.Reader) *product {
	logger := log.Default()
	lookup := systemLookup
	if cfg.DNSResolver != "" {
		lookup = resolverLookup(cfg.DNSResolver)
	}
	return &product{
		database:           database,
		handle:             database.DB(),
		register:           &register{handle: database.DB(), dialect: database.Dialect()},
		mounter:            mounter,
		reader:             reader,
		logger:             logger,
		lookup:             lookup,
		allowPrivateEgress: cfg.AllowPrivateEgress,
		claimsEvery:        claimsEvery,
		assertionsEvery:    assertionsEvery,
		stopBound:          stopWait,
	}
}

// systemLookup resolves the TXT records of a name through the system resolver,
// under a deadline of dnsTimeout.
func systemLookup(ctx context.Context, name string) ([]string, error) {
	return lookupTXT(ctx, net.DefaultResolver, name)
}

// resolverLookup resolves the TXT records of a name through the resolver at
// address (host:port), under a deadline of dnsTimeout. The resolver is a Go
// resolver whose dial always reaches address, whatever resolver address the
// system configuration would otherwise name.
func resolverLookup(address string) func(context.Context, string) ([]string, error) {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, address)
		},
	}
	return func(ctx context.Context, name string) ([]string, error) {
		return lookupTXT(ctx, resolver, name)
	}
}

// lookupTXT is the one TXT lookup the ownership proof makes, through whichever
// resolver it was handed.
func lookupTXT(ctx context.Context, resolver *net.Resolver, name string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()
	return resolver.LookupTXT(ctx, name)
}

// wire runs in Init, where every instance exists: it takes up the optional
// capabilities, subscribes to the settings change event when both the event bus
// and the settings store are present, writes the startup diagnostics and
// registers the discovery route.
func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
	}
	var err error
	if p.store, err = optional[settings.Store](reg, "the settings store"); err != nil {
		return err
	}
	if p.bus, err = optional[event.Bus](reg, "the event bus"); err != nil {
		return err
	}
	if p.audit, err = optional[audit.Audit](reg, "the audit capability"); err != nil {
		return err
	}
	// The run-time switch is optional: a process that does not run the login
	// engine delivers none, and then no method can start a flow either, so the
	// discovery filters nothing.
	if p.loginSwitch, err = optional[authn.LoginMethodSwitch](reg, "the login-method switch"); err != nil {
		return err
	}
	p.subscribe()

	// The discovery names only a provider whose protocol this run delivers. The
	// login methods are parallel, so the set comes from ResolveAll and not from
	// Requires; a protocol whose method stood down is absent here, and its
	// providers drop out of the answer.
	p.delivered = deliveredProtocols(core.ResolveAll[authn.LoginMethod](reg))

	p.diagnose()
	p.mountRoutes()
	return nil
}

// deliveredProtocols is the set of enterprise protocols this run delivers, from
// the login methods the registry holds. A method's name is the protocol its
// providers record, and only oidc and saml name a provider the register can
// hold; every other method is ignored, so a social or password method never
// makes a provider discoverable. A method the run did not construct is absent,
// which is how a protocol whose enable switch is off leaves the set.
func deliveredProtocols(methods []authn.LoginMethod) map[string]bool {
	out := make(map[string]bool, len(methods))
	for _, method := range methods {
		switch name := method.Name(); name {
		case model.SSOProtocolOIDC, model.SSOProtocolSAML:
			out[name] = true
		}
	}
	return out
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

// diagnose writes the startup lines: one Warn for each protection given up, and
// one Info naming the route the module mounts.
func (p *product) diagnose() {
	if p.allowPrivateEgress {
		p.logger.Warn(diagnosticPrefix+ModuleName+": the restricted egress client admits connections to "+
			"loopback addresses, which is a developer relaxation and not a deployment one",
			slog.String("item", ConfigNamespace+"."+allowPrivateEgressItem))
	}
}
