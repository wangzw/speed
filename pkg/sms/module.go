package sms

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/literal"
	"github.com/vislake/speed/pkg/log"
)

// Product is what a carrier module delivers: the two seams a carrier answers
// through, over one vendor client.
//
// The sending seam and the template-management seam are one value and not two
// because they reach the same vendor account through the same client, and the
// module constructs its instance once. A carrier module delivers both,
// exclusively, so a command still names exactly one carrier. Product is Sender
// plus Provider, so it carries Provider.Model transitively: a caller holding a
// Product can read the carrier's capability declaration as well as send through
// it.
type Product interface {
	Sender
	Provider
}

// Spec is what a carrier subpackage hands this package to become a module.
//
// It is the whole of what a carrier contributes. Everything else — the exclusive
// delivery of both seams, mounting the input-item declaration under a namespace
// of the carrier's own, resolving the optional logging capability, registering
// the carrier's credentials as literal redaction rules and releasing nothing,
// since a carrier holds only an in-process dedupe table — is this package's,
// shared by every carrier.
//
// The type parameter is the carrier's own configuration carrier. Each carrier
// accepts different items — an endpoint, a signature, an application identity —
// so there is no shared carrier to hand here.
type Spec[C any] struct {
	// ModuleName is this carrier's name in the registry: its identity in the
	// startup diagnostics and in an enablement lookup.
	ModuleName string
	// ConfigNamespace is where this carrier's input items are mounted in the
	// config data. It has to be one of its own: the configuration manifest is
	// collected from every registered module, a run's disabled ones included, so
	// two carriers declaring input items on one path conflict as soon as a host
	// imports both.
	ConfigNamespace string
	// Config is a pointer to this carrier's input-item prototype: the struct
	// config fills, carrying the values an absent key takes. The schema reads the
	// accepted keys and their defaults from it.
	Config *C
	// Items describes each declared item, keyed by its path within the namespace.
	Items map[string]config.Item
	// Stance reads the decoded configuration and states what this carrier cannot
	// start without. A carrier taken in at run level has to start, so the state
	// it returns is StateEnabled: two carriers enabled at once fail the startup
	// instead of one silently winning. Everything it cannot start without — a
	// value the loader's required-item declaration does not already enforce — is
	// an error naming what is wrong, and the startup fails with it ahead of any
	// message.
	Stance func(cfg *C) (core.Enablement, error)
	// Secrets returns the values this carrier holds that have to be registered as
	// literal redaction rules, so a credential that turns up in any log line of
	// the process is masked. It is the carrier's way to name a credential without
	// importing the literal package: this package registers what it returns.
	//
	// It is optional; a carrier with no credential of its own leaves it nil.
	Secrets func(cfg *C) []string
	// Open builds this carrier from the decoded configuration. It returns the
	// one product that answers both seams, so a carrier returns whatever type
	// implements them; the factory declares the seams from the product's
	// capabilities.
	Open func(ctx context.Context, cfg *C) (Product, error)
}

// NewModule builds a carrier's module descriptor.
//
// A defective Spec panics here rather than being reported: a subpackage fills it
// in at init, so the fault is in this repository's own code, shows up
// deterministically on the first run of any host that imports it, and leaves a
// host nothing to handle.
func NewModule[C any](spec Spec[C]) core.Module {
	spec.check()
	return core.Module{
		Name: spec.ModuleName,
		// Optional, because a sender has to work in an assembly that never took
		// logging in; declared, because a carrier's credential is registered as a
		// literal redaction rule and an assembly with logging has to have it built
		// first.
		Requires: []core.Requirement{{Token: (*log.Logger)(nil), Optional: true}},
		// Both seams are delivered, both exclusively: a consumer would otherwise
		// hand its messages to whichever carrier resolution happened to leave
		// standing, and two at once therefore fail the startup. One instance
		// answers both, so the carrier a command sends through is the one whose
		// management seam that command reaches.
		Provides: []core.Provision{
			{Token: (*Sender)(nil), Exclusive: true},
			{Token: (*Provider)(nil), Exclusive: true},
		},
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
			if err := spec.registerSecrets(reg, cfg); err != nil {
				return nil, err
			}
			return spec.Open(ctx, cfg)
		},
	}
}

// check refuses a Spec that cannot produce a working module. Each condition is
// one this package would otherwise meet much later and much further from its
// cause.
func (s Spec[C]) check() {
	switch {
	case s.ModuleName == "":
		panic("sms: a Spec carries the module name this carrier registers under")
	case s.ConfigNamespace == "":
		panic(fmt.Sprintf("sms: module %q gives no configuration namespace, and its input items have "+
			"to hang under one of its own: carriers sharing a namespace conflict as soon as a host "+
			"imports both", s.ModuleName))
	case s.Config == nil:
		panic(fmt.Sprintf("sms: module %q gives no configuration prototype, which is what its accepted "+
			"input items and their defaults are read from; a carrier that accepts nothing still mounts "+
			"an empty struct here", s.ModuleName))
	case s.Stance == nil:
		panic(fmt.Sprintf("sms: module %q gives no stance, so nothing states what it cannot start without",
			s.ModuleName))
	case s.Open == nil:
		panic(fmt.Sprintf("sms: module %q gives no constructor, so nothing reaches its carrier", s.ModuleName))
	}
}

// schema is this carrier's input-item declaration, mounted under its own
// namespace.
func (s Spec[C]) schema() config.Schema {
	return config.Schema{
		Namespace: s.ConfigNamespace,
		Mounts:    []config.Mount{{Value: s.Config}},
		Items:     s.Items,
	}
}

// read hands back a fresh copy of the prototype with the carrier's own section
// decoded into it.
//
// The copy matters because Prepare and New each read, and a shared carrier would
// have the second read writing over the first one's values. Decode leaves a
// field no source gave a value for as it found it, so the copy is what carries
// the defaults into the result.
func (s Spec[C]) read(reg *core.Registry) (*C, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: this carrier's input items are read from configuration, and the host "+
			"handed no reading face into the registry before assembly began: %w", s.ModuleName, err)
	}
	cfg := new(C)
	*cfg = *s.Config
	if err := reader.Decode(s.ConfigNamespace, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// registerSecrets registers the carrier's credentials as literal redaction rules
// with the logging capability, when it names any and the assembly carries one.
//
// A credential has no shape to match, so the only thing known about it is the
// value; without a rule it would be masked only where the code remembered to,
// and a request that failed would carry it into a log line. The registration
// runs in New, before any Named logger binds an attribute, which is the timing a
// redaction rule needs. With no logging capability there is no log to protect,
// and the absence is told apart from a failed lookup by the sentinel.
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
