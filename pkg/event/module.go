package event

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/literal"
	"github.com/vislake/speed/pkg/log"
)

// Spec is what an implementation subpackage hands this package to become a
// module. The type parameter is the implementation's own configuration carrier.
type Spec[C any] struct {
	// ModuleName is this implementation's name in the registry: its identity in
	// the startup diagnostics and in an enablement lookup.
	ModuleName string
	// ConfigNamespace is where this implementation's input items are mounted. It
	// has to be one of its own: two implementations declaring items on one path
	// conflict as soon as a host imports both.
	ConfigNamespace string
	// Config is a pointer to this implementation's input-item prototype: the
	// struct config fills, carrying the values an absent key takes.
	Config *C
	// Items describes each declared item, keyed by its path within the
	// namespace.
	Items map[string]config.Item
	// Stance reads the decoded configuration and states whether this
	// implementation can start and how its exclusive claim gives way. A module
	// taken in at run level must start, so StateDisabled is resolution's alone.
	Stance func(cfg *C) (core.Enablement, error)
	// Trace takes up the optional tracing capability, adapting the telemetry
	// module's provider into this package's TracerProvider. An assembly with no
	// telemetry hands back one that exports nothing.
	Trace func(reg *core.Registry) (TracerProvider, error)
	// Open builds this implementation from the decoded configuration. logger is
	// where it reports what has no caller to return to, and tracer is what Trace
	// resolved, handed to dispatch.New together with the propagator.
	Open func(ctx context.Context, cfg *C, logger *slog.Logger, tracer TracerProvider) (Bus, error)
	// Start is called at the start stage, where an implementation freezes what
	// must be fixed before the first publish and opens itself to publishing. It
	// takes up its own product, refuses any other bus, and nil leaves the stage out.
	Start func(ctx context.Context, bus Bus) error
	// Secrets returns the configuration values to register as literal redaction
	// rules, so a credential in any log line is masked. It is optional, and an
	// empty value contributes nothing.
	Secrets func(cfg *C) []string
}

// TracerProvider is the smallest face this package takes up from the tracing
// capability: starting one span. It is defined here so a module that only
// publishes events carries no telemetry closure.
type TracerProvider interface {
	// Start begins a span as a child of whatever span ctx carries, named name and
	// carrying attrs, and returns a context with it and the span itself. The face
	// carries no span kind.
	Start(ctx context.Context, name string, attrs map[string]string) (context.Context, Span)
}

// Span is one span this package started.
type Span interface {
	// End closes the span, marking it failed when err is non-nil.
	End(err error)
}

// NewModule builds an implementation's module descriptor. A defective Spec
// panics here.
func NewModule[C any](spec Spec[C]) core.Module {
	spec.check()
	m := core.Module{
		Name: spec.ModuleName,
		// Optional, because a bus has to work in an assembly that never took
		// logging in; declared, because an assembly that did has to have it
		// constructed first.
		Requires: []core.Requirement{{Token: (*log.Logger)(nil), Optional: true}},
		// Exclusive: two backends behind one capability would leave a
		// publisher's subscribers on the other one never seeing its events.
		Provides:  []core.Provision{{Token: (*Bus)(nil), Exclusive: true}},
		Resources: []any{spec.schema()},
		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			cfg, err := spec.read(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			return spec.Stance(cfg)
		},
		New: func(ctx context.Context, reg *core.Registry) (any, error) {
			cfg, err := spec.read(reg)
			if err != nil {
				return nil, err
			}
			// The credentials are registered before the logger is taken up:
			// Named binds the module attribute, and a rule only governs what is
			// bound after it is registered.
			if err = spec.registerSecrets(reg, cfg); err != nil {
				return nil, err
			}
			logger, err := spec.logger(reg)
			if err != nil {
				return nil, err
			}
			tracer, err := spec.Trace(reg)
			if err != nil {
				return nil, err
			}
			return spec.Open(ctx, cfg, logger, tracer)
		},
		Stop: func(ctx context.Context, _ *core.Registry, instance any) error {
			return stop(ctx, instance)
		},
		Close: func(ctx context.Context, _ *core.Registry, instance any) error {
			return release(ctx, instance)
		},
	}
	if spec.Start != nil {
		m.Start = func(ctx context.Context, _ *core.Registry, instance any) error {
			bus, ok := instance.(Bus)
			if !ok {
				return fmt.Errorf("event: module %q reached the start stage carrying %T, which is not "+
					"a Bus, and the registry asserts the capability of every instance, constructed "+
					"or supplied, before this stage runs", spec.ModuleName, instance)
			}
			return spec.Start(ctx, bus)
		}
	}
	return m
}

// check refuses a Spec that cannot produce a working module. Each condition is
// one this package would otherwise meet much later and much further from its
// cause.
func (s Spec[C]) check() {
	switch {
	case s.ModuleName == "":
		panic("event: a Spec carries the module name this implementation registers under")
	case s.ConfigNamespace == "":
		panic(fmt.Sprintf("event: module %q gives no configuration namespace, and its input items "+
			"have to hang under one of its own: implementations sharing a namespace conflict as soon "+
			"as a host imports both", s.ModuleName))
	case s.Config == nil:
		panic(fmt.Sprintf("event: module %q gives no configuration prototype, which is what its "+
			"accepted input items and their defaults are read from; a backend that accepts nothing "+
			"still mounts an empty struct here", s.ModuleName))
	case s.Stance == nil:
		panic(fmt.Sprintf("event: module %q gives no stance, so nothing decides whether it can start "+
			"or how its exclusive claim gives way", s.ModuleName))
	case s.Open == nil:
		panic(fmt.Sprintf("event: module %q gives no constructor, so nothing reaches its backend", s.ModuleName))
	case s.Trace == nil:
		panic(fmt.Sprintf("event: module %q gives no tracer resolution, and Open has to be handed the "+
			"narrow provider it returns: an implementation with no telemetry of its own still hands "+
			"back the one that exports nothing", s.ModuleName))
	}
}

func (s Spec[C]) schema() config.Schema {
	return config.Schema{
		Namespace: s.ConfigNamespace,
		Mounts:    []config.Mount{{Value: s.Config}},
		Items:     s.Items,
	}
}

func (s Spec[C]) read(reg *core.Registry) (*C, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: this implementation's input items are read from configuration, and "+
			"the host handed no reading face into the registry before assembly began: %w",
			s.ModuleName, err)
	}
	cfg := new(C)
	*cfg = *s.Config
	if err := reader.Decode(s.ConfigNamespace, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (s Spec[C]) registerSecrets(reg *core.Registry, cfg *C) error {
	if s.Secrets == nil {
		return nil
	}
	values := s.Secrets(cfg)
	if len(values) == 0 {
		return nil
	}
	delivered, err := core.Resolve[log.Logger](reg)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
		return nil
	case err != nil:
		return fmt.Errorf("%s: taking up the logging capability to register its credentials: %w",
			s.ModuleName, err)
	}
	literal.Register(delivered.Redaction(), s.ModuleName+".credentials", values...)
	return nil
}

func (s Spec[C]) logger(reg *core.Registry) (*slog.Logger, error) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return log.Default(), nil
		}
		return nil, fmt.Errorf("%s: taking up the logger this implementation reports through: %w",
			s.ModuleName, err)
	}
	return delivered.Named(s.ModuleName), nil
}

// stopper is what an implementation's product implements to end its service at
// the stop stage: publishing is refused, delivery ends, and running subscribers
// get a bounded time.
type stopper interface {
	Stop(ctx context.Context) error
}

func stop(ctx context.Context, instance any) error {
	s, ok := instance.(stopper)
	if !ok {
		return nil
	}
	return s.Stop(ctx)
}

type closer interface {
	Close(ctx context.Context) error
}

func release(ctx context.Context, instance any) error {
	c, ok := instance.(closer)
	if !ok {
		return nil
	}
	return c.Close(ctx)
}
