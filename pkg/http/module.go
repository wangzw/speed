package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// Module is the descriptor an implementation subpackage registers. name is the
// module's name in the registry, and newEngine builds the routing engine one
// endpoint's routes are mounted on. That function is the seam between this
// package and an implementation subpackage, and it is what keeps the engine's
// own type off this package's surface; a module that registers routes goes
// through Endpoint and never meets an engine.
//
// The root package registers nothing by itself. A host chooses an entry point
// by importing the subpackage that binds the engine it wants, and importing
// this package alone brings in the registration surface and the request
// helpers without opening a port.
//
// The capability is delivered exclusively and the stance is StateEnabled. The
// pair is deliberate: resolution only stands a provider down when it claims
// exclusivity and states StateAuto, so a second entry point implementation
// fails the startup instead of quietly taking the registrations that were meant
// for this one.
//
// Three packages are depended on. core carries the capability tokens, the
// declared resources and the instance lookups; config is where the endpoints
// are read from, over a reader the host hands the registry before the drive
// begins; log supplies the token of the optional dependency below and the
// context accessors the base layer injects through.
//
// That reader is not delivered by a module. It is in the registry before the
// first module is constructed, so it is taken up with a lookup and declared in
// no Requires: there is nothing to order this module against. Its absence is
// still a startup failure — the endpoints are read through it and there is
// nothing to serve without them.
//
// The dependency on PanicResponder is declared optional too, and its absence
// is the ordinary case rather than a state only a hand-built registry reaches:
// which shape a defect's response takes is the business of whichever module
// owns that API, and most assemblies have none. With no provider the fallback
// writes the status alone.
//
// The dependency on Logger is declared optional, and that declaration is a
// statement of contract rather than a branch a host reaches. This package
// imports pkg/log, which registers itself with the process registry at package
// initialisation, so every host that imports this module transitively carries a
// logging module. The absent case only stands in a registry assembled by hand,
// which is how the tests here reach it: nothing is injected into the request
// context, and the request path's records reach the process default logger's
// destination rather than the configured one.
func Module(name string, newEngine func() Engine) core.Module {
	if newEngine == nil {
		panic(fmt.Sprintf("http: Module(%q, nil) has no engine to build. Pass the function that "+
			"builds one routing engine per endpoint; it is what binds the engine to this "+
			"module without putting its type on the surface", name))
	}
	return core.Module{
		Name: name,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil), Optional: true},
			{Token: (*PanicResponder)(nil), Optional: true},
		},
		Provides:  []core.Provision{{Token: (*Router)(nil), Exclusive: true}},
		Resources: []any{schema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("http: the listening endpoints are read "+
					"from configuration, and no reader was handed to the registry "+
					"before the assembly began: %w", err)
			}
			return prepare(reader, core.Resources[Spec](reg))
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return nil, fmt.Errorf("http: the listening endpoints are read from "+
					"configuration, and no reader was handed to the registry before "+
					"the assembly began: %w", err)
			}
			injected, err := requestLogger(reg, name)
			if err != nil {
				return nil, err
			}
			responder, err := panicResponder(reg)
			if err != nil {
				return nil, err
			}
			return newModule(reader, newEngine, injected, responder)
		},

		// The gate is opened in Migrate and sealed in Start, the two
		// stage slots the design's stage table gives it. The window that
		// leaves open contains the Init stage whole and reaches beyond it at
		// both ends, because a stage advances one module at a time: this
		// module's Migrate callback runs before the assembly enters Init,
		// and its Start callback runs once Init has ended.
		// Neither end could be this module's own Init or Serve callback — a
		// module that never declared a dependency on Router may legally
		// resolve it and register from an Init that runs before this one,
		// and the Start stage together with the Serve callbacks ordered
		// ahead of this module all lie outside Init. This module migrates
		// nothing and has no service of its own to start, so the two slots
		// have no other use.
		//
		// Whether either slot runs is the depth of the drive to decide,
		// and the two are tied by nothing. A drive shallower than Migrate
		// runs neither and reaches no Init: both sides of the window are
		// absent together and the result is consistent. A drive that stops
		// exactly at Migrate opens the gate with nobody to seal it, and it
		// stays open for the rest of the process; that run has no Init, no
		// registration and no traffic. The way round it is for such a
		// command not to take this module in — applying migrations needs no
		// entry point.
		Migrate: func(_ context.Context, _ *core.Registry, instance any) error {
			if r, ok := productOf(instance); ok {
				r.open()
			}
			return nil
		},
		Start: func(_ context.Context, _ *core.Registry, instance any) error {
			if r, ok := productOf(instance); ok {
				r.seal()
			}
			return nil
		},
		Serve: func(_ context.Context, _ *core.Registry, instance any) error {
			r, ok := productOf(instance)
			if !ok {
				return nil
			}
			return r.serve()
		},
		Stop: func(ctx context.Context, _ *core.Registry, instance any) error {
			r, ok := productOf(instance)
			if !ok {
				return nil
			}
			return r.stop(ctx)
		},
		Close: func(ctx context.Context, _ *core.Registry, instance any) error {
			r, ok := productOf(instance)
			if !ok {
				return nil
			}
			return r.close(ctx)
		},
	}
}

// productOf recovers this module's product from what the registry hands back.
// A nil instance, or one of another type, is tolerated rather than asserted:
// a startup that fails part-way rolls back every constructed instance, so the
// later callbacks run on modules whose New never returned a product.
func productOf(instance any) (*router, bool) {
	r, ok := instance.(*router)
	return r, ok && r != nil
}

// prepare reads the configuration, checks the declared fragments and states the
// stance.
//
// A module taken in at run level has to start, so not being able to start is a
// failure and not a stance. With no endpoint configured there is no address to
// serve on, and this returns an error naming the key to write; the alternative
// — standing down — is what the module-level rule forbids, because a host that
// named this entry point would otherwise get a process with no entry point and
// a zero exit status.
//
// The declared fragments are checked first, and over the available set: the
// shape of a fragment is a static fact about the binary, so a malformed one has
// to be reported whatever this run's configuration says. Checking it after the
// endpoint premise would let a host that configured no endpoint hide a fragment
// another configuration of the same binary would reject.
func prepare(reader config.Reader, specs []core.Resource[Spec]) (core.Enablement, error) {
	settings, err := readSettings(reader)
	if err != nil {
		return core.Enablement{}, err
	}
	if err := validateSpecs(specs); err != nil {
		return core.Enablement{}, err
	}
	if len(settings) == 0 {
		return core.Enablement{}, fmt.Errorf("http: no listening endpoint is configured: "+
			"%s.%s is empty, so there is no address to serve on. Declare an endpoint under "+
			"that key to run this entry point", configNamespace, endpointsItem)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// readSettings decodes this module's section and validates it. Prepare and New
// both go through it, so the two see one set of endpoints and one set of
// defaults.
func readSettings(reader config.Reader) ([]endpointSettings, error) {
	var cfg moduleConfig
	if err := reader.Decode(configPath, &cfg); err != nil {
		return nil, err
	}
	return cfg.resolve()
}

// newModule builds the product: one endpoint per configured entry, with the
// gate still shut. Nothing is bound here — the address is bound in Serve.
func newModule(reader config.Reader, newEngine func() Engine, injected *slog.Logger,
	responder PanicResponder,
) (*router, error) {
	settings, err := readSettings(reader)
	if err != nil {
		return nil, err
	}
	r := newRouter(settings, newEngine)
	r.injected = injected
	r.responder = responder
	r.logger = injected
	if r.logger == nil {
		// No Logger capability in this assembly, a state reached only by
		// building the registry by hand: importing this package pulls in
		// pkg/log, which registers itself. This module's own diagnostics
		// still have somewhere to go; what the absence changes is that
		// nothing is injected into the request context.
		r.logger = log.Default()
	}
	return r, nil
}

// requestLogger takes up the optional Logger capability and returns the logger
// to inject into each request's context, or nil when no module delivers it.
//
// Absence is a legal configuration and is told apart from a real failure by the
// sentinel: anything other than "no provider" is a failure of the lookup itself
// and terminates the startup.
func requestLogger(reg *core.Registry, name string) (*slog.Logger, error) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return nil, nil
		}
		return nil, fmt.Errorf("http: taking up the logger to inject into request contexts: %w", err)
	}
	return delivered.Named(name), nil
}

// panicResponder takes up the optional PanicResponder capability and returns
// what decides the fallback's response shape, or nil when no module delivers
// it.
//
// Absence is told apart from a real failure the same way, and for the same
// reason: it is the ordinary case, because most assemblies have no module that
// owns an API's error shape.
func panicResponder(reg *core.Registry) (PanicResponder, error) {
	delivered, err := core.Resolve[PanicResponder](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return nil, nil
		}
		return nil, fmt.Errorf("http: taking up the responder that decides the panic fallback's "+
			"response shape: %w", err)
	}
	return delivered, nil
}

// serve assembles every endpoint's chain and binds its address.
//
// Both are synchronous, so a failure of either terminates the startup rather
// than surfacing later in a goroutine; the accept loops are what runs on their
// own. An endpoint that fails leaves the ones bound before it listening, and
// the rollback that follows the startup failure closes them: Stop and Close run
// over every constructed instance whatever stage it reached.
func (r *router) serve() error {
	for _, name := range r.names {
		e := r.endpoints[name]
		handler, err := e.assemble(r.newEngine(), r.injected, r.logger, r.responder)
		if err != nil {
			return err
		}
		if err := e.socket.start(e.settings, handler, r.logger); err != nil {
			return err
		}
	}
	return nil
}

// stop closes every endpoint's socket, then waits for every endpoint to finish
// draining, and reports the failures together.
//
// It returns only once the drains are over, because the modules the shutdown
// reaches after this callback are ones an in-flight request may still be
// using. Stop runs module by module and Close follows the whole Stop stage, so
// a request left draining behind this callback would find a module stopped
// after this one refusing its work, and any module closed holding nothing it
// can use: the audit trail it writes, the spans it produces, a handle its
// handler reads through. The price is that a module stopped after this one
// keeps taking work from sources other than this entry point for as long as
// the drain lasts.
//
// Every socket is closed before any drain is waited for, so no endpoint goes
// on accepting while another one drains. The endpoints are then waited on one
// after another, which is the same as waiting on them together: each drain has
// been running since that endpoint's socket closed, so one endpoint timing out
// does not shorten the wait of any other.
func (r *router) stop(ctx context.Context) error {
	var failures []error
	for _, name := range r.names {
		if err := r.endpoints[name].socket.stop(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	for _, name := range r.names {
		if err := r.endpoints[name].socket.close(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// close releases whatever Stop left behind, which on the lifecycle's own path
// is nothing: Stop has waited for every drain and reported what came of it. A
// host driving the stages by hand may reach Close without Stop, and then the
// sockets are closed and the drains waited for here.
func (r *router) close(ctx context.Context) error {
	var failures []error
	for _, name := range r.names {
		if err := r.endpoints[name].socket.close(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
