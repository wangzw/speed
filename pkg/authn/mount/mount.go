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
	base string
	// publicURL is the origin of the configured public URL in the form a browser
	// sends it, and empty when none is configured.
	publicURL string
	// configured is the endpoint the configuration names, empty when it names
	// none.
	configured string
	// csrf is the cross-origin check every registration passes through, built
	// from the public URL and the trusted origins and carrying the bypass
	// patterns of the callbacks.
	csrf *http.CrossOriginProtection

	// wired is what Init settles: the endpoint the routes are mounted on and
	// the two registration surfaces they are written to. It is nil before Init
	// and read-only afterwards.
	wired atomic.Pointer[wiring]
}

// wiring is the part of the module that exists once Init has run.
type wiring struct {
	// name is the endpoint the routes are mounted on.
	name     string
	endpoint speedhttp.Endpoint
	gate     authn.Gate
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
		Name:     ModuleName,
		Requires: []core.Requirement{{Token: (*authn.Gate)(nil)}},
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
	return &product{base: set.Mount.BasePath, publicURL: origin, configured: set.Mount.Endpoint, csrf: csrf}, nil
}

// wire runs in Init: it settles the endpoint the routes go on, takes up the two
// registration surfaces and writes the startup diagnostic.
//
// The endpoint is judged for the one thing only this module can say, that there
// is one: the key empty and the entry point declaring anything but exactly one
// endpoint leaves nothing to pick (ErrRoutesUnconfigured), which can only be
// told here because the entry point's endpoints are not known before its
// instance exists. That it is guarded, that the admission layer does not cover
// it and that tenant resolution does not resolve on it are the gateway's to
// judge in Start, on the same name.
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
	name, err := mount.Endpoint(p.configured, router.Endpoints(), key(endpointItem))
	if err != nil {
		return err
	}
	endpoint, err := router.Endpoint(name)
	if err != nil {
		if errors.Is(err, speedhttp.ErrUnknownEndpoint) {
			return fmt.Errorf("%w: %s names the endpoint %q, which the entry point does not have. "+
				"Correct the name, or declare that endpoint in the entry point's configuration",
				authn.ErrUnknownEndpoint, key(endpointItem), name)
		}
		return fmt.Errorf("%staking up the endpoint %q the routes are mounted on: %w", diagnosticPrefix, name, err)
	}
	gate, err := core.Resolve[authn.Gate](reg)
	if err != nil {
		return fmt.Errorf("%staking up the gateway to register the routes' access levels on: %w",
			diagnosticPrefix, err)
	}
	p.wired.Store(&wiring{name: name, endpoint: endpoint, gate: gate})

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
		slog.String("endpoint", name), slog.String("base_path", p.base))
	return nil
}
