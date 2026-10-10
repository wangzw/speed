package kv

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
// every implementation. That split is what keeps implementations from drifting
// apart in the behaviour a host sees.
//
// The type parameter is the implementation's own configuration carrier. Each
// backend accepts different items — an address, a pool size, a replica count —
// so there is no carrier to share here as there is in a capability whose
// implementations all connect the same way. The parameter is what keeps the
// stance and the constructor typed against that carrier instead of asserting on
// an any.
type Spec[C any] struct {
	// ModuleName is this implementation's name in the registry: its identity in
	// the startup diagnostics and in an enablement lookup.
	ModuleName string
	// ConfigNamespace is where this implementation's input items are mounted in
	// the config data.
	//
	// It has to be one of its own, and that is load-bearing rather than tidy:
	// the configuration manifest is collected from every registered module, this
	// run's disabled ones included, so two implementations declaring input items
	// on one path conflict as soon as a host imports both — and that conflict is
	// raised while the manifest is being collected, before exclusivity is
	// resolved, so the resolution that would have stood one of them down never
	// runs. Importing several implementations and configuring one is the
	// intended shape, and separate namespaces are what make it work.
	//
	// Implementations give it the module name, so a diagnostic naming the module
	// and an error naming a key read as the same thing.
	ConfigNamespace string
	// Config is a pointer to this implementation's input-item prototype: the
	// struct config fills, carrying the values an absent key takes. The schema
	// reads the accepted keys and their defaults from it.
	//
	// It must be a pointer to a struct whose fields are values or collections
	// Decode replaces wholesale. This package copies it before decoding, so a
	// field Decode appends to rather than replaces would leak between runs.
	Config *C
	// Items describes each declared item, keyed by its path within the
	// namespace.
	Items map[string]config.Item
	// Stance reads the decoded configuration and answers two things: how this
	// implementation's exclusive claim yields to another provider's, and what it
	// cannot start without.
	//
	// A module taken in at run level has to start, so the state it returns says
	// nothing about whether it runs. It is one of StateAuto or StateEnabled:
	// StateAuto is for the capability's zero-configuration implementation, which
	// yields the moment another provider is explicitly enabled, and StateEnabled
	// is for an implementation that has to be pointed at a backend, so that two
	// of them pointed at once fail the startup instead of one silently winning.
	//
	// Everything this implementation cannot start without — a setting it was not
	// given, a deployment premise its backend cannot serve — is an error naming
	// that input, and the startup fails with it. StateDisabled is not a stance a
	// module may take: only resolution gives it, to a provider that stood down
	// over an exclusive claim.
	Stance func(cfg *C) (core.Enablement, error)
	// Open builds this implementation from the decoded configuration.
	Open func(ctx context.Context, cfg *C) (KV, error)
}

// NewModule builds an implementation's module descriptor.
//
// A defective Spec panics here rather than being reported: a subpackage fills it
// in at init, so the fault is in this repository's own code, shows up
// deterministically on the first run of any host that imports it, and leaves a
// host nothing to handle. It is the treatment core gives an illegal token, for
// the same reason.
func NewModule[C any](spec Spec[C]) core.Module {
	spec.check()
	return core.Module{
		Name: spec.ModuleName,
		// Exclusive, because two backends behind one capability is not a
		// configuration this module could serve: dependants would each take up
		// whichever one resolution happened to leave standing, and their data
		// would be split across two stores with nobody told. Two configured
		// implementations therefore fail the startup instead.
		Provides:  []core.Provision{{Token: (*KV)(nil), Exclusive: true}},
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
			return spec.Open(ctx, cfg)
		},
		Close: func(ctx context.Context, _ *core.Registry, instance any) error {
			return release(ctx, instance)
		},
	}
}

// check refuses a Spec that cannot produce a working module. Each condition is
// one this package would otherwise meet much later and much further from its
// cause: a missing namespace as a manifest conflict naming a path nobody wrote,
// a missing stance as a nil call inside Prepare.
func (s Spec[C]) check() {
	switch {
	case s.ModuleName == "":
		panic("kv: a Spec carries the module name this implementation registers under")
	case s.ConfigNamespace == "":
		panic(fmt.Sprintf("kv: module %q gives no configuration namespace, and its input items have "+
			"to hang under one of its own: implementations sharing a namespace conflict as soon as a "+
			"host imports both", s.ModuleName))
	case s.Config == nil:
		panic(fmt.Sprintf("kv: module %q gives no configuration prototype, which is what its accepted "+
			"input items and their defaults are read from; a backend that accepts nothing still mounts "+
			"an empty struct here", s.ModuleName))
	case s.Stance == nil:
		panic(fmt.Sprintf("kv: module %q gives no stance, so nothing states how its exclusive claim "+
			"yields and nothing reports what it cannot start without", s.ModuleName))
	case s.Open == nil:
		panic(fmt.Sprintf("kv: module %q gives no constructor, so nothing reaches its backend", s.ModuleName))
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

// closer is what an implementation's instance implements to release what it
// opened. An implementation whose backend holds nothing open — the in-memory one
// — does not implement it, and release leaves it alone.
type closer interface {
	Close(ctx context.Context) error
}

// release closes an implementation's backend, when its instance has one to
// close.
//
// Every shape the shutdown can hand it is tolerated. A startup that failed
// part-way closes every module whatever stage it reached, so this runs on a
// module whose New never returned an instance, and reporting an error on that
// would bury the failure that started the rollback under one about the cleanup.
// It also runs on an instance the host supplied for the module, which Open did
// not build and which can be any type that delivers KV: one that implements
// closer is closed here, and what the others hold, or what a Close leaves held,
// is the supplying host's to release, since nothing in this package knows what
// it is. An unguarded type assertion would panic on either shape, and on the
// first lose the failure that started the rollback outright, which is why the
// assertion is the checked form.
func release(ctx context.Context, instance any) error {
	c, ok := instance.(closer)
	if !ok {
		return nil
	}
	return c.Close(ctx)
}
