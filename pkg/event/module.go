package event

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
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
	// Stance reads the decoded configuration and states whether this
	// implementation runs.
	//
	// It is where an implementation answers two questions at once: whether the
	// host pointed it at anything, and whether the deployment it found itself in
	// suits it. A backend that needs an address and was given none states
	// StateDisabled with a reason; an implementation that delivers within one
	// process states StateDisabled when the host declared several replicas.
	//
	// A zero-configuration implementation states StateAuto, so that it yields to
	// an explicitly enabled backend rather than colliding with it. An
	// implementation that has to be pointed at a backend states StateEnabled, so
	// that two of them being configured at once fails the startup.
	Stance func(cfg *C) core.Enablement
	// Open builds this implementation from the decoded configuration.
	Open func(ctx context.Context, cfg *C) (Bus, error)
	// Start is called when the drive reaches the start stage, and is where an
	// implementation freezes what has to be fixed before the first publish.
	//
	// The subscription table is the thing that has to be: every module
	// subscribes in its own Init, which the drive calls one module at a time, so
	// the table is not complete until the stage after them. Freezing it there is
	// what makes a subscription registered later a defect at the call site
	// rather than a subscriber that silently receives nothing published before
	// it.
	//
	// A nil one leaves this implementation without that stage.
	Start func(ctx context.Context, bus Bus) error
}

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
			return spec.Stance(cfg), nil
		},
		New: func(ctx context.Context, reg *core.Registry) (any, error) {
			cfg, err := spec.read(reg)
			if err != nil {
				return nil, err
			}
			return spec.Open(ctx, cfg)
		},
		Close: func(ctx context.Context, _ *core.Registry, instance any) error {
			return release(ctx, instance)
		},
	}
	if spec.Start != nil {
		m.Start = func(ctx context.Context, _ *core.Registry, instance any) error {
			bus, ok := instance.(Bus)
			if !ok {
				return fmt.Errorf("event: module %q reached the start stage carrying %T, and every "+
					"constructed module reaches this stage with the capability it delivers",
					spec.ModuleName, instance)
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
		panic(fmt.Sprintf("event: module %q gives no stance, so nothing decides whether it runs", s.ModuleName))
	case s.Open == nil:
		panic(fmt.Sprintf("event: module %q gives no constructor, so nothing reaches its backend", s.ModuleName))
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
func release(ctx context.Context, instance any) error {
	c, ok := instance.(closer)
	if !ok {
		return nil
	}
	return c.Close(ctx)
}
