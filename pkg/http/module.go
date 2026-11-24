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

// ModuleName is this module's name in the registry, and the name a host writes to take this module into a command of its tree. It is exported for that: a command takes a module in by name, and a literal spread over a host's commands is one a rename leaves behind.
const ModuleName = "http"

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not inherit
// the process-level registrations.
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

		// The gate is opened in Migrate and sealed in Start.
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

// productOf recovers this module's product from what the registry hands back. A nil instance, or one of another type, is tolerated rather than asserted: a startup that fails part-way rolls back every constructed instance, and a host-supplied instance is any type that delivers Router.
func productOf(instance any) (*router, bool) {
	r, ok := instance.(*router)
	return r, ok && r != nil
}

func prepare(reader config.Reader, specs []core.Resource[Spec]) (core.Enablement, error) {
	listeners, _, err := readSettings(reader)
	if err != nil {
		return core.Enablement{}, err
	}
	if err := validateSpecs(specs); err != nil {
		return core.Enablement{}, err
	}
	if len(listeners) == 0 {
		return core.Enablement{}, fmt.Errorf("%w: no listener is configured: "+
			"%s.%s is empty, so there is no address to serve on. Declare a listener under "+
			"that key to run this entry point", ErrInvalidConfig, configNamespace, listenersItem)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

func readSettings(reader config.Reader) ([]listenerSettings, []endpointSettings, error) {
	var cfg moduleConfig
	if err := reader.Decode(configPath, &cfg); err != nil {
		return nil, nil, err
	}
	return cfg.resolve()
}

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
		// No Logger capability in this assembly, a state reached only by building the registry by hand: importing this package pulls in pkg/log, which registers itself.
		r.logger = log.Default()
	}
	return r, nil
}

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
		// The dispatch sits outside every endpoint's chain, so a request the dispatch itself
		// answers — a 404 with no fallback — carries the injected logger, ErrorResponder and
		// Resolver only if the context is built here.
		handler := injectContext(dispatch, r.injected, r.responder, r.resolver)
		if err := l.start(l.settings, handler, r.logger); err != nil {
			return err
		}
	}
	return nil
}

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

func (r *router) close(ctx context.Context) error {
	var failures []error
	for _, name := range r.listenerNames {
		if err := r.listeners[name].close(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
