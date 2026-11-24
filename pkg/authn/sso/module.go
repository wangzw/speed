// Package sso is the enterprise single sign-on module: the register of tenant identity providers, the ownership proof of their email domains and the domain discovery route. It holds authn_idps, authn_idp_domains and authn_saml_assertions and delivers authn.SSOProviders exclusively.
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

const (
	idpTable       = "authn_idps"
	domainTable    = "authn_idp_domains"
	assertionTable = "authn_saml_assertions"
)

const admissionModule = "tenant.admission"

const diagnosticPrefix = "authn: "

const defaultEndpoint = "account"

//go:embed openapi/sso.json
var ssoFragment []byte

var ssoSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: ssoFragment,
}

const dnsTimeout = 5 * time.Second

//go:embed migrations
var migrationRoot embed.FS

var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

func init() { core.ProcessRegistry.Register(Module()) }

var _ authn.SSOProviders = (*product)(nil)

// Module is the descriptor a host registers. It requires the database and the mounter, takes the settings store as optional so it is initialised first, and takes the bus, audit, login-method switch and logger up in Init. It claims authn.SSOProviders exclusively and states the enabled stance.
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

		Close: func(context.Context, *core.Registry, any) error { return nil },
	}
}

func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: the developer egress relaxation is read from configuration, and no "+
			"reader was handed to the registry before the assembly began: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}

type product struct {
	database db.Database
	handle   *gorm.DB

	register *register
	mounter  mount.Mounter

	reader config.Reader

	store  settings.Store
	bus    event.Bus
	audit  audit.Audit
	logger *slog.Logger

	lookup func(ctx context.Context, name string) ([]string, error)

	delivered map[string]bool

	loginSwitch authn.LoginMethodSwitch

	allowPrivateEgress bool

	clock clock.Source

	sweeper         *sweeper
	claimsEvery     time.Duration
	assertionsEvery time.Duration
	stopBound       time.Duration
}

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

func systemLookup(ctx context.Context, name string) ([]string, error) {
	return lookupTXT(ctx, net.DefaultResolver, name)
}

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

func lookupTXT(ctx context.Context, resolver *net.Resolver, name string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()
	return resolver.LookupTXT(ctx, name)
}

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
	if p.loginSwitch, err = optional[authn.LoginMethodSwitch](reg, "the login-method switch"); err != nil {
		return err
	}
	p.subscribe()

	p.delivered = deliveredProtocols(core.ResolveAll[authn.LoginMethod](reg))

	p.diagnose()
	p.mountRoutes()
	return nil
}

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
	if p.allowPrivateEgress {
		p.logger.Warn(diagnosticPrefix+ModuleName+": the restricted egress client admits connections to "+
			"loopback addresses, which is a developer relaxation and not a deployment one",
			slog.String("item", ConfigNamespace+"."+allowPrivateEgressItem))
	}
}
