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
// module.
//
// It is the whole of what an implementation contributes. Everything else — the
// exclusive delivery of the capability, mounting the input-item declaration
// under a namespace of the implementation's own, taking up the reading face,
// and releasing what the implementation opened — is this package's, shared by
// every implementation.
//
// The type parameter is the implementation's own configuration carrier. Each
// backend accepts different items — an address, a replica name, a consumer
// group — so there is no carrier to share here.
type Spec[C any] struct {
	// ModuleName is this implementation's name in the registry: its identity in
	// the startup diagnostics and in an enablement lookup.
	ModuleName string
	// ConfigNamespace is where this implementation's input items are mounted in
	// the config data.
	//
	// It has to be one of its own: the configuration manifest is collected from
	// every registered module, this run's disabled ones included, so two
	// implementations declaring input items on one path conflict as soon as a
	// host imports both — and that conflict is raised before exclusivity is
	// resolved, so the resolution that would have stood one of them down never
	// runs.
	ConfigNamespace string
	// Config is a pointer to this implementation's input-item prototype: the
	// struct config fills, carrying the values an absent key takes.
	Config *C
	// Items describes each declared item, keyed by its path within the
	// namespace.
	Items map[string]config.Item
	// Stance reads the decoded configuration and states what this implementation
	// cannot start without, and how its exclusive claim gives way.
	//
	// It is where an implementation answers two questions at once: whether the
	// host pointed it at anything, and whether the deployment it found itself in
	// suits it. Both are answered the same way: an implementation that cannot
	// run returns an error naming the input that would have let it, and the
	// startup fails with that text. A module the host took in at run level has
	// to start, so "this run does not want it" is not one of its answers; the
	// only source of StateDisabled is resolution.
	//
	// What is left is the pair of states that say how this implementation's
	// exclusive claim gives way. A zero-configuration implementation states
	// StateAuto, so that it runs until another provider of the capability is
	// explicitly enabled and then stands down. An implementation that has to be
	// pointed at a backend states StateEnabled, so that two of them being
	// pointed at once fails the startup.
	Stance func(cfg *C) (core.Enablement, error)
	// Trace takes up the optional tracing capability: the implementation adapts
	// the telemetry module's tracer provider into this package's TracerProvider
	// here, and hands back the implementation that exports nothing when the
	// assembly carries no telemetry.
	//
	// It is what keeps the telemetry interface package out of the root
	// package's dependency face: the root package defines the narrow face below
	// and never names the telemetry module's type. The provider it returns is
	// what Open is handed, and what the dispatch layer records a delivery's
	// span on.
	//
	// An implementation also appends the optional tracer requirement to the
	// descriptor NewModule hands back, since the root package cannot name
	// (*observability.TracerProvider)(nil) without importing that interface
	// package. That edge is what orders the assembly ahead of the bus; without
	// it, this function finds no provider and returns one that exports nothing,
	// and the bus runs with no span exported and nothing to say so.
	Trace func(reg *core.Registry) (TracerProvider, error)
	// Open builds this implementation from the decoded configuration.
	//
	// logger is where the implementation writes what it has to report on its
	// own — a subscriber's failure, a backend it could not read — since none of
	// that has a caller to return to. It is the assembly's logger named after
	// this module when the assembly delivers the logging capability, and the
	// logging module's process default otherwise, so the records obey the
	// configured destinations, format and redaction whenever there are any.
	//
	// tracer is what Trace resolved, and is handed to dispatch.New together with
	// the propagator so the subscription table holds what it records a
	// delivery's span with. See the package documentation for the narrow face it
	// satisfies.
	Open func(ctx context.Context, cfg *C, logger *slog.Logger, tracer TracerProvider) (Bus, error)
	// Start is called when the drive reaches the start stage, and is where an
	// implementation freezes what has to be fixed before the first publish and
	// opens itself to publishing.
	//
	// The subscription table is the thing that has to be: every module
	// subscribes in its own Init, which the drive calls one module at a time, so
	// the table is not complete until the stage after them. Freezing it there is
	// what makes a subscription registered later a defect at the call site
	// rather than a subscriber that silently receives nothing published before
	// it.
	//
	// bus is whatever instance the drive holds for the module: the one Open
	// built, or one the host supplied for the module, for which Open never ran.
	// An implementation takes up its own product and returns an error for any
	// other bus, since what it freezes and starts is the state of the product
	// Open builds, and a bus built elsewhere has none of it. A drive that
	// carries a supplied bus to this stage therefore fails here, and a drive
	// that stops short of it does not.
	//
	// A nil one leaves this implementation without that stage.
	Start func(ctx context.Context, bus Bus) error
	// Secrets returns the values this implementation holds as configuration
	// that have to be registered as literal redaction rules, so a credential
	// that turns up in any log line of the process is masked. It is how an
	// implementation names a credential without importing the literal package:
	// the factory registers what it returns.
	//
	// It is optional; an implementation with no credential of its own leaves it
	// nil. A value that is empty contributes nothing and is not registered.
	Secrets func(cfg *C) []string
}

// TracerProvider is the smallest face this package takes up from the tracing
// capability: starting one span.
//
// It is defined here rather than aliased from the telemetry module, so that a
// module which only publishes events does not carry the telemetry interface
// package's upstream closure — the tracing API, the metrics API and the two
// SDK exporter packages — in its dependency face, whether or not the assembly
// runs telemetry. An implementation adapts the telemetry provider into this
// face in its own Trace.
type TracerProvider interface {
	// Start begins a span as a child of whatever span ctx carries, named name
	// and carrying attrs, and hands back a context with the span in it and the
	// span itself. The face carries no span kind: a delivery is neither a
	// client nor a server span.
	Start(ctx context.Context, name string, attrs map[string]string) (context.Context, Span)
}

// Span is one span this package started. End closes the span, marking it
// failed when err is non-nil.
type Span interface{ End(err error) }

// NewModule builds an implementation's module descriptor.
//
// A defective Spec panics here rather than being reported: a subpackage fills it
// in at init, so the fault is in this repository's own code, shows up
// deterministically on the first run of any host that imports it, and leaves a
// host nothing to handle.
func NewModule[C any](spec Spec[C]) core.Module {
	spec.check()
	m := core.Module{
		Name: spec.ModuleName,
		// Optional, because a bus has to work in an assembly that never took
		// logging in; declared, because an assembly that did has to have it
		// constructed first, or the bus would be built before there was a
		// logger to hand it.
		Requires: []core.Requirement{{Token: (*log.Logger)(nil), Optional: true}},
		// Exclusive, because two backends behind one capability is not a
		// configuration this module could serve: a publisher would hand its
		// events to whichever one resolution happened to leave standing, and its
		// subscribers, registered on the other, would never see them.
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

// schema is this implementation's input-item declaration, mounted under its own
// namespace.
func (s Spec[C]) schema() config.Schema {
	return config.Schema{
		Namespace: s.ConfigNamespace,
		Mounts:    []config.Mount{{Value: s.Config}},
		Items:     s.Items,
	}
}

// read hands back a fresh copy of the prototype with the implementation's own
// section decoded into it.
//
// The copy matters because Prepare and New each read, and a shared carrier would
// have the second read writing over the first one's values. Decode leaves a
// field no source gave a value for as it found it, so the copy is what carries
// the defaults into the result.
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

// registerSecrets registers the values this implementation names as literal
// redaction rules with the logging capability, when it names any and the
// assembly carries one.
//
// It runs in New, before the logger is taken up so that a rule governs the
// module attribute Named binds. A credential has no shape to match, so the only
// thing known about it is the value; without a rule it would be masked only
// where the code remembered to, and a failure would carry it into a log line.
// With no logging capability there is no log to protect, and the absence is
// told apart from a failed lookup by the sentinel.
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

// logger takes up the logging capability when the assembly delivers one.
//
// Its absence is a legal assembly and is told apart from a failed lookup by the
// sentinel: anything other than "no provider" is the lookup itself failing,
// and that terminates the startup rather than quietly sending this module's
// records somewhere the host did not configure.
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

// stopper is what an implementation's product implements to end its service
// at the stop stage: publishing is refused from then on, delivery ends, and the
// subscribers already running are given a bounded time to finish.
//
// The stage is the implementation's rather than Close's, because the drive
// runs every module's Stop before any module's Close. Delivery that lasted
// until Close would run on for as long as every other module took to release
// its resources, handing events to subscribers that had already stopped.
type stopper interface {
	Stop(ctx context.Context) error
}

// stop ends an implementation's service, when it has one to end. It tolerates
// every shape a rollback hands it, on the same terms as release.
func stop(ctx context.Context, instance any) error {
	s, ok := instance.(stopper)
	if !ok {
		return nil
	}
	return s.Stop(ctx)
}

// closer is what an implementation's product implements to release what it
// opened. An implementation that holds nothing open does not implement it, and
// release leaves it alone.
type closer interface {
	Close(ctx context.Context) error
}

// release closes an implementation's backend, when it has one to close.
//
// Every shape a rollback can hand it is tolerated: a startup that failed
// part-way closes every module whatever stage it reached, so this runs on a
// module whose New never returned a product. Reporting an error there would
// bury the failure that started the rollback under one about the cleanup, and
// an unguarded type assertion would panic and lose it outright.
//
// The instance may also be a bus the host supplied for the module, of whatever
// type. It is closed through the Close it has and left alone when it has none;
// what it holds that its Close does not release is the host's to release.
func release(ctx context.Context, instance any) error {
	c, ok := instance.(closer)
	if !ok {
		return nil
	}
	return c.Close(ctx)
}
