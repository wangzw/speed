package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
)

// ModuleName is this module's name in the registry. It is also the name a host
// writes to take this module into a command of its tree, and that is why it is
// exported: a command takes a module in by name, and a literal spread over a
// host's commands is one a rename leaves behind with nothing to catch it.
//
// The configuration namespace is the same word, "http", and a different thing:
// input items hang from the namespace a schema declares and the module name
// takes no part in those paths.
const ModuleName = "http"

// init registers this module with the process registry, the way pkg/log does,
// so a host that imports this package can take it into a command by name.
// Importing it alone opens no port: a registered module is available, and only
// the ones a command names take part in a run.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
//
// The capability is delivered exclusively and the stance is StateEnabled. The
// pair is deliberate: resolution only stands a provider down when it claims
// exclusivity and states StateAuto, so a second entry point module fails the
// startup instead of quietly taking the registrations that were meant for this
// one.
//
// Four packages are depended on. core carries the capability tokens, the
// declared resources and the instance lookups; config is where the listeners and
// the endpoints are read from, through the reader its module delivers; log
// supplies the
// token of the optional dependency below and the context accessors the base
// layer injects through; i18n supplies the token of the localizer that renders
// this module's own refusals, its root package being standard-library only so
// that reading it adds no third-party dependency.
//
// That reader is the instance of the config module, which the host supplies
// before the drive begins. It exists from that call on, so Prepare and New take
// it up with a lookup and this module declares it in no Requires: there is
// nothing to order this module against, and neither Stop nor Close reads it. Its
// absence is still a startup failure — the listeners and endpoints are read
// through it and there is nothing to serve without them.
//
// A host can supply an instance for this module too, to stand where its New
// would have put the router. Prepare and New do not run for it, so the checks
// Prepare makes are not made: the listener premise, and the shape of the
// fragments the modules declared. Only the router this module builds is acted
// on. An instance of any other type, one the host wrote or one it wrapped around
// a router, is left alone by every callback from Migrate to Close: the gate
// neither opens nor seals, Serve assembles no chain and binds no address, and
// Stop and Close drain nothing and release nothing. How it serves and when what
// it holds is released are its owner's to answer, and the requests it takes in
// are outside the drain this module waits for.
//
// The dependency on ErrorResponder is declared optional too, and its absence
// is the ordinary case rather than a state only a hand-built registry reaches:
// which shape an API's error responses take is the business of whichever module
// owns that API, and most assemblies have none. With no provider the responses
// take the default shape, a problem details object. A provider decides the
// shape alone: it carries no language, and the text of a refusal is finished
// here before it is handed over.
//
// The dependency on Logger is declared optional, and that declaration is a
// statement of contract rather than a branch a host reaches. This package
// imports pkg/log, which registers itself with the process registry at package
// initialisation, so every host that imports this module transitively carries a
// logging module. The absent case only stands in a registry assembled by hand,
// which is how the tests here reach it: nothing is injected into the request
// context, and the request path's records reach the process default logger's
// destination rather than the configured one.
//
// The dependency on i18n.Localizer is declared optional, and it is what renders
// the text of the four refusals this module writes itself. Absence is the
// ordinary case — an assembly with no i18n engine — and it is no failure: the
// refusals fall back to net/http's standard text for their status, the text an
// assembly without this capability has always written. The engine that delivers
// the localizer is where this module's own catalog is merged, so the
// //go:embed catalog in Resources is read through it and not here.
//
// The dependency on i18n.Resolver is declared optional too, and it is what names
// the language of the default problem shape in a Content-Language header: the
// locale it resolves for the request, which is the request's own when one was
// settled and the run's default otherwise, so a client answered in a language
// other than the one it asked for is told which it got. Absence is the ordinary
// case and is no failure: the four refusals are still written, with no such
// header, which is the state an assembly with no i18n engine has always been in.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil), Optional: true},
			{Token: (*ErrorResponder)(nil), Optional: true},
			{Token: (*i18n.Localizer)(nil), Optional: true},
			{Token: (*i18n.Resolver)(nil), Optional: true},
		},
		Provides:  []core.Provision{{Token: (*Router)(nil), Exclusive: true}},
		Resources: []any{schema(), messageCatalog},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("http: the listeners and endpoints are read "+
					"from configuration, and no reader was handed to the registry "+
					"before the assembly began: %w", err)
			}
			return prepare(reader, core.Resources[Spec](reg))
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return nil, fmt.Errorf("http: the listeners and endpoints are read from "+
					"configuration, and no reader was handed to the registry before "+
					"the assembly began: %w", err)
			}
			injected, err := requestLogger(reg, ModuleName)
			if err != nil {
				return nil, err
			}
			responder, err := errorResponder(reg)
			if err != nil {
				return nil, err
			}
			localizer, err := localizerFor(reg)
			if err != nil {
				return nil, err
			}
			resolver, err := resolverFor(reg)
			if err != nil {
				return nil, err
			}
			return newModule(reader, injected, responder, localizer, resolver)
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
// later callbacks run on modules whose New never returned a product, and the
// instance a host supplied for this module is any type that delivers Router.
// What is not this module's product is left alone.
func productOf(instance any) (*router, bool) {
	r, ok := instance.(*router)
	return r, ok && r != nil
}

// prepare reads the configuration, checks the declared fragments and states the
// stance.
//
// A module taken in at run level has to start, so not being able to start is a
// failure and not a stance. With no listener configured there is no address to
// serve on, and this returns an error naming the key to write; the alternative
// — standing down — is what the module-level rule forbids, because a host that
// named this entry point would otherwise get a process with no entry point and
// a zero exit status.
//
// The declared fragments are checked first, and over the available set: the
// shape of a fragment is a static fact about the binary, so a malformed one has
// to be reported whatever this run's configuration says. Checking it after the
// listener premise would let a host that configured no listener hide a fragment
// another configuration of the same binary would reject.
func prepare(reader config.Reader, specs []core.Resource[Spec]) (core.Enablement, error) {
	listeners, _, err := readSettings(reader)
	if err != nil {
		return core.Enablement{}, err
	}
	if err := validateSpecs(specs); err != nil {
		return core.Enablement{}, err
	}
	if len(listeners) == 0 {
		return core.Enablement{}, fmt.Errorf("http: no listener is configured: "+
			"%s.%s is empty, so there is no address to serve on. Declare a listener under "+
			"that key to run this entry point", configNamespace, listenersItem)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// readSettings decodes this module's section and validates it. Prepare and New
// both go through it, so the two see one set of listeners and one set of
// endpoints, and one set of defaults.
func readSettings(reader config.Reader) ([]listenerSettings, []endpointSettings, error) {
	var cfg moduleConfig
	if err := reader.Decode(configPath, &cfg); err != nil {
		return nil, nil, err
	}
	return cfg.resolve()
}

// newModule builds the product: one listener per configured entry and one
// endpoint per configured entry, with the gate still shut. Nothing is bound
// here — the addresses are bound in Serve.
func newModule(reader config.Reader, injected *slog.Logger, responder ErrorResponder, localizer i18n.Localizer, resolver i18n.Resolver) (*router, error) {
	listeners, endpoints, err := readSettings(reader)
	if err != nil {
		return nil, err
	}
	r := newRouter(listeners, endpoints)
	r.injected = injected
	r.responder = responder
	r.localizer = localizer
	r.resolver = resolver
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

// errorResponder takes up the optional ErrorResponder capability and returns
// what decides the shape of the error responses, or nil when no module
// delivers it.
//
// Absence is told apart from a real failure the same way, and for the same
// reason: it is the ordinary case, because most assemblies have no module that
// owns an API's error shape. More than one provider is a failure of the lookup
// itself, so the startup ends rather than one of them being picked.
func errorResponder(reg *core.Registry) (ErrorResponder, error) {
	delivered, err := core.Resolve[ErrorResponder](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return nil, nil
		}
		return nil, fmt.Errorf("http: taking up the responder that decides the shape of error "+
			"responses: %w", err)
	}
	return delivered, nil
}

// localizerFor takes up the optional Localizer capability and returns what
// renders this module's own refusals, or nil when no module delivers it.
//
// Absence is told apart from a real failure the same way, and for the same
// reason: an assembly with no i18n engine is the ordinary case, and the
// refusals then fall back to net/http's standard text. More than one provider
// is a failure of the lookup itself, so the startup ends rather than one of them
// being picked.
func localizerFor(reg *core.Registry) (i18n.Localizer, error) {
	delivered, err := core.Resolve[i18n.Localizer](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return nil, nil
		}
		return nil, fmt.Errorf("http: taking up the localizer that renders the entry point's own "+
			"refusals: %w", err)
	}
	return delivered, nil
}

// resolverFor takes up the optional Resolver capability and returns what names
// the language of the default problem shape, or nil when no module delivers it.
//
// Absence is told apart from a real failure the same way, and for the same
// reason: an assembly with no i18n engine is the ordinary case, and the default
// shape then names no language. More than one provider is a failure of the
// lookup itself, so the startup ends rather than one of them being picked.
func resolverFor(reg *core.Registry) (i18n.Resolver, error) {
	delivered, err := core.Resolve[i18n.Resolver](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return nil, nil
		}
		return nil, fmt.Errorf("http: taking up the resolver that names the language of an error "+
			"response: %w", err)
	}
	return delivered, nil
}

// serve assembles every endpoint's chain and binds every listener's address.
//
// Both are synchronous, so a failure of either terminates the startup rather
// than surfacing later in a goroutine; the accept loops are what runs on their
// own. An endpoint's chain is assembled first, because the listener's dispatch
// handler routes to the assembled chains: it mounts each endpoint's routes and
// hands every request it matches to that endpoint's chain. A failure leaves the
// listeners bound before it listening, and the rollback that follows the startup
// failure closes them: Stop and Close run over every constructed instance
// whatever stage it reached.
func (r *router) serve() error {
	for _, name := range r.names {
		e := r.endpoints[name]
		handler, err := e.assemble(r.injected, r.logger, r.responder, r.localizer, r.resolver)
		if err != nil {
			return err
		}
		e.handler = handler
	}
	for _, name := range r.listenerNames {
		l := r.listeners[name]
		dispatch, err := buildDispatch(name, l.endpoints, r.localizer)
		if err != nil {
			return err
		}
		// The dispatch sits outside every endpoint's chain, so a request the
		// dispatch itself answers — a 404 with no fallback — carries the injected
		// logger, ErrorResponder and Resolver only if the context is built here.
		// A request the dispatch hands to an endpoint, a method-not-allowed one
		// among them, reaches that endpoint's base, which builds the same context
		// again; the values are the same, so injecting twice is injecting once.
		handler := injectContext(dispatch, r.injected, r.responder, r.resolver)
		if err := l.start(l.settings, handler, r.logger); err != nil {
			return err
		}
	}
	return nil
}

// stop closes every listener's socket, then waits for every listener to finish
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
// Every socket is closed before any drain is waited for, so no listener goes
// on accepting while another one drains. The listeners are then waited on one
// after another, which is the same as waiting on them together: each drain has
// been running since that listener's socket closed, so one listener timing out
// does not shorten the wait of any other.
func (r *router) stop(ctx context.Context) error {
	var failures []error
	for _, name := range r.listenerNames {
		if err := r.listeners[name].stop(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	for _, name := range r.listenerNames {
		if err := r.listeners[name].close(ctx); err != nil {
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
	for _, name := range r.listenerNames {
		if err := r.listeners[name].close(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
