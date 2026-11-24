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
// through, over one vendor client. It is Sender plus Provider.
type Product interface {
	Sender
	Provider
}

// Spec is what a carrier subpackage hands this package to become a module: its
// module and carrier names, the selection and fallback flags, its config
// prototype, items, stance, secrets and open function.
type Spec[C any] struct {
	// ModuleName is this carrier's name in the registry: its identity in the
	// startup diagnostics and in an enablement lookup.
	ModuleName string
	// CarrierName is this carrier's name in the sms.carrier selection item and the
	// name its Sender and Provider answer Name with.
	CarrierName string
	// Fallback marks the carrier that runs when no other carrier was explicitly
	// enabled; the log carrier sets it.
	Fallback bool
	// DeclaresSelection makes this carrier declare the sms.carrier item; the factory
	// refuses a second declaration.
	DeclaresSelection bool
	// ConfigNamespace is where this carrier's input items are mounted; it has to be
	// one of its own so two carriers never declare items on one path.
	ConfigNamespace string
	// Config is a pointer to this carrier's input-item prototype, carrying the
	// defaults an absent key takes.
	Config *C
	// Items describes each declared item, keyed by its path within the namespace.
	Items map[string]config.Item
	// Stance judges this carrier's own configuration before the selection is
	// applied: StateEnabled when it can run, StateDisabled with a reason when unset,
	// and an error when half-configured.
	Stance func(cfg *C) (core.Enablement, error)
	// Secrets returns the values to register as literal redaction rules; optional,
	// nil when the carrier has no credential of its own.
	Secrets func(cfg *C) []string
	// Open builds this carrier from the decoded configuration and returns the one
	// product that answers both seams.
	Open func(ctx context.Context, cfg *C) (Product, error)
}

// NewModule builds a carrier's module descriptor. A defective Spec panics, since
// the subpackage fills it in at init and the fault is this repository's own.
func NewModule[C any](spec Spec[C]) core.Module {
	spec.check()
	return core.Module{
		Name:     spec.ModuleName,
		Requires: []core.Requirement{{Token: (*log.Logger)(nil), Optional: true}},
		Provides: []core.Provision{
			{Token: (*Sender)(nil), Exclusive: true},
			{Token: (*Provider)(nil), Exclusive: true},
		},
		Resources: spec.resources(),
		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			return spec.stanceFor(reg)
		},
		New: func(ctx context.Context, reg *core.Registry) (any, error) {
			cfg, err := spec.read(reg)
			if err != nil {
				return nil, err
			}
			if err := spec.registerSecrets(reg, cfg); err != nil {
				return nil, err
			}
			if delivered, err := core.Resolve[log.Logger](reg); err == nil {
				ctx = log.WithLogger(ctx, delivered.Named(spec.ModuleName))
			}
			return spec.Open(ctx, cfg)
		},
	}
}

func (s Spec[C]) check() {
	switch {
	case s.ModuleName == "":
		panic("sms: a Spec carries the module name this carrier registers under")
	case s.CarrierName == "":
		panic(fmt.Sprintf("sms: module %q gives no carrier name, and sms.carrier selects a carrier by "+
			"that name", s.ModuleName))
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

func (s Spec[C]) resources() []any {
	declared := []any{s.schema(), CarrierName{Name: s.CarrierName}}
	if s.DeclaresSelection {
		declared = append(declared, SelectionSchema())
	}
	return declared
}

func (s Spec[C]) schema() config.Schema {
	return config.Schema{
		Namespace: s.ConfigNamespace,
		Mounts:    []config.Mount{{Value: s.Config}},
		Items:     s.Items,
	}
}

// A fresh copy of the prototype, so Prepare and New do not overwrite each other.
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
// before any Named logger binds an attribute.
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
