// Package mount carries the module that mounts the identity system's own routes:
// the one that delivers the route-mounting capability the login, session,
// multi-factor, SSO, token, impersonation and administration modules register
// their routes through.
//
// A route and the access level the gateway applies to it are written in one
// call. The module registers the route on the entry point and the access level
// on the gateway's public registration surface (Exempt, Optional,
// Impersonable), so there is no route that was mounted and left guarded or
// exempted, and no exemption that names a path with a typo in it. It does that
// through authn.Gate alone, so a host that supplies a gateway of its own can
// still take this module in.
//
// Every registration goes through one wrapper (pkg/authn/internal/mount.Wrap):
// the response carries Cache-Control: no-store, and a request with an unsafe
// method that is not a callback is put to the module's own cross-origin check
// before the handler runs. The check trusts the origin of the public URL and the
// trusted origins, which the gateway builds its own check from by the same rule
// (internal/mount.CrossOrigin), so the two cannot come to trust different sets
// from one configuration.
//
// The module declares no input item. The keys it reads, authn.gate.mount.* and
// authn.gate.trusted-origins, are the gateway's.
package mount

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// ModuleName is this module's name in the registry. A host writes it into the
// commands that mount the capability's routes.
const ModuleName = "authn.mount"

// diagnosticPrefix starts every line this module writes to the startup
// diagnostics, which have several writers; it is the gateway's, so that a reader
// scanning for what authentication did finds both.
const diagnosticPrefix = "authn: "

// product is this module's product. It implements mount.Mounter.
type product struct {
	// bases is the base path of each scope the capability's routes span.
	bases map[speedhttp.Scope]string
	// endpoints is the configured endpoint of each scope. A scope left empty
	// keeps the empty value; the default scope settles it in Init, the system
	// scope falls back to the default one, and the tenant scope has none.
	endpoints map[speedhttp.Scope]string
	// publicURL is the origin of the configured public URL in the form a browser
	// sends it, and empty when none is configured.
	publicURL string
	// csrf is the cross-origin check every registration passes through, built
	// from the public URL and the trusted origins and carrying the bypass
	// patterns of the callbacks.
	csrf *http.CrossOriginProtection

	// wired is what Init settles: the endpoint each scope's routes are mounted
	// on and the two registration surfaces they are written to. It is nil before
	// Init and read-only afterwards.
	wired atomic.Pointer[wiring]
}

// wiring is the part of the module that exists once Init has run.
type wiring struct {
	// defaultName is the endpoint the ordinary scope is mounted on. It is the
	// realm a handler's own 401 names, the protection space being the ordinary
	// authentication surface.
	defaultName string
	// endpoints is the endpoint of each scope that may be mounted, by scope.
	endpoints map[speedhttp.Scope]wiredEndpoint
	gate      authn.Gate
}

// wiredEndpoint is one scope's mount point: the name the gateway is told and
// the entry point's own surface the route is written to.
type wiredEndpoint struct {
	name     string
	endpoint speedhttp.Endpoint
}

// product must satisfy the capability it delivers.
var _ mount.Mounter = (*product)(nil)

// init registers this module with the process registry.
//
// Registration travels with the import, which is what makes "does this command
// mount the capability's routes" a question the host answers by importing this
// package and writing ModuleName into the command.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// Requires holds the gateway, which is delivered exclusively and so is not
// pulled in by the dependency closure: a command that names this module and
// leaves the gateway out fails before any instance is built, with the registry's
// missing-provider error. The entry point is taken up in Init, where every
// instance exists. The capability is delivered exclusively and the stance is
// StateEnabled: whether routes are mounted is a decision of the command tree and
// not a conclusion a configuration can draw.
//
// Prepare does not run for an instance the host supplied, and every later
// callback hands this module that instance; one that is not this module's
// product is left alone, and what it holds is released by whoever supplied it.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*authn.Gate)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{{Token: (*mount.Mounter)(nil), Exclusive: true}},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if _, err := readSettings(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return nil, err
			}
			set, err := readSettings(reader)
			if err != nil {
				return nil, err
			}
			return newProduct(set)
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			p, ok := instance.(*product)
			if !ok || p == nil {
				return nil
			}
			return p.wire(reg)
		},
	}
}

// readerOf takes up the configuration reader. Its absence is core's own failure
// and is carried out as it stands: reading it as "nothing was configured" would
// mount the routes on a configuration nobody read.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%sthe keys the routes are mounted by are read from configuration, and "+
			"no reader was handed to the registry before the assembly began: %w", diagnosticPrefix, err)
	}
	return reader, nil
}

// newProduct builds the product from settings that passed check. It takes up no
// module's instance.
func newProduct(set settings) (*product, error) {
	csrf, err := mount.CrossOrigin(set.Mount.PublicURL, set.TrustedOrigins)
	if err != nil {
		return nil, fmt.Errorf("%w: %s or %s does not give the cross-origin check an origin set it "+
			"can trust: %w", authn.ErrInvalidConfig, key(publicURLItem), key(trustedOriginsItem), err)
	}
	origin, err := mount.Origin(set.Mount.PublicURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is not an origin: %w", authn.ErrInvalidConfig, key(publicURLItem), err)
	}
	return &product{
		bases: map[speedhttp.Scope]string{
			"":                     set.Mount.Bases.Default,
			speedhttp.ScopeTenants: set.Mount.Bases.Tenants,
			speedhttp.ScopeSystem:  set.Mount.Bases.System,
		},
		endpoints: map[speedhttp.Scope]string{
			"":                     set.Mount.Endpoints.Default,
			speedhttp.ScopeTenants: set.Mount.Endpoints.Tenants,
			speedhttp.ScopeSystem:  set.Mount.Endpoints.System,
		},
		publicURL: origin, csrf: csrf,
	}, nil
}

// wire runs in Init: it settles the endpoint of each scope, takes up the two
// registration surfaces and writes the startup diagnostic.
//
// The default endpoint is judged for the one thing only this module can say,
// that there is one: the key empty and the entry point declaring anything but
// exactly one endpoint leaves nothing to pick (ErrRoutesUnconfigured), which can
// only be told here because the entry point's endpoints are not known before its
// instance exists. The system scope shares the default endpoint unless it names
// one of its own. The tenant scope has no fallback: an unwritten key leaves it
// unmountable, and a tenant-scoped registration is refused at its call site
// rather than landing on an endpoint no tenant layer guards. That each endpoint
// is guarded, and that the tenant layers cover the tenant scope's endpoint and
// no other, are the gateway's to judge in Start, on the same names.
func (p *product) wire(reg *core.Registry) error {
	router, err := core.Resolve[speedhttp.Router](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return fmt.Errorf("%w: this command mounts the authentication routes but includes no HTTP "+
				"entry point to mount them on. Write the entry implementation subpackage's module "+
				"name into this command", authn.ErrNoRouter)
		}
		return fmt.Errorf("%staking up the HTTP entry point to mount routes on: %w", diagnosticPrefix, err)
	}
	defaultName, err := mount.Endpoint(p.endpoints[""], router.Endpoints(), key(defaultMountItem))
	if err != nil {
		return err
	}
	names := map[speedhttp.Scope]string{"": defaultName}
	names[speedhttp.ScopeSystem] = p.endpoints[speedhttp.ScopeSystem]
	if names[speedhttp.ScopeSystem] == "" {
		names[speedhttp.ScopeSystem] = defaultName
	}
	if tenants := p.endpoints[speedhttp.ScopeTenants]; tenants != "" {
		names[speedhttp.ScopeTenants] = tenants
	}
	wired := make(map[speedhttp.Scope]wiredEndpoint, len(names))
	// The scopes are visited in a fixed order. The map would otherwise decide
	// which key a failure names: an unset system scope aliases the default's
	// name, so an invalid default fails twice and the reader would be pointed at
	// whichever scope the iteration reached first.
	for _, scope := range []speedhttp.Scope{"", speedhttp.ScopeSystem, speedhttp.ScopeTenants} {
		name, configured := names[scope]
		if !configured {
			continue
		}
		endpoint, lookupErr := router.Endpoint(name)
		if lookupErr != nil {
			if errors.Is(lookupErr, speedhttp.ErrUnknownEndpoint) {
				return fmt.Errorf("%w: %s names the endpoint %q, which the entry point does not have. "+
					"Correct the name, or declare that endpoint in the entry point's configuration",
					authn.ErrUnknownEndpoint, key(mountItemFor(scope)), name)
			}
			return fmt.Errorf("%staking up the endpoint %q the routes are mounted on: %w", diagnosticPrefix, name, lookupErr)
		}
		wired[scope] = wiredEndpoint{name: name, endpoint: endpoint}
	}
	gate, err := core.Resolve[authn.Gate](reg)
	if err != nil {
		return fmt.Errorf("%staking up the gateway to register the routes' access levels on: %w",
			diagnosticPrefix, err)
	}
	p.wired.Store(&wiring{defaultName: defaultName, endpoints: wired, gate: gate})

	logger := log.Default()
	delivered, err := core.Resolve[log.Logger](reg)
	switch {
	case err == nil:
		logger = delivered.Named(ModuleName)
	case errors.Is(err, core.ErrMissingProvider):
		// No logging module in this assembly: the diagnostic goes to the process
		// default logger.
	default:
		return fmt.Errorf("%staking up the logging capability for the startup diagnostic: %w", diagnosticPrefix, err)
	}
	logger.Info(diagnosticPrefix+"the authentication routes are mounted",
		slog.String("endpoint", defaultName),
		slog.String("tenants_endpoint", wired[speedhttp.ScopeTenants].name),
		slog.String("system_endpoint", wired[speedhttp.ScopeSystem].name),
		slog.String("default_base_path", p.bases[""]),
		slog.String("tenants_base_path", p.bases[speedhttp.ScopeTenants]),
		slog.String("system_base_path", p.bases[speedhttp.ScopeSystem]))
	return nil
}

// mountItemFor is the configuration item that names one scope's endpoint,
// relative to the gateway's mount section.
func mountItemFor(scope speedhttp.Scope) string {
	switch scope {
	case speedhttp.ScopeTenants:
		return tenantsMountItem
	case speedhttp.ScopeSystem:
		return systemMountItem
	default:
		return defaultMountItem
	}
}
