package mail

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/literal"
	"github.com/vislake/speed/pkg/log"
)

// Spec is what a carrier subpackage hands this package to become a module.
type Spec[C any] struct {
	// ModuleName is this carrier's name in the registry.
	ModuleName string
	// CarrierName is this carrier's name in the mail.carrier selection item and
	// the name its Sender answers Name with.
	CarrierName string
	// Fallback marks the carrier that runs when no other carrier was explicitly
	// enabled and no configured carrier claimed the capability. The log carrier
	// sets it.
	Fallback bool
	// DeclaresSelection makes this carrier declare the mail.carrier item; one
	// carrier declares it and the rest read it. The log carrier sets it.
	DeclaresSelection bool
	// ConfigNamespace is where this carrier's input items are mounted, and it has
	// to be one of its own.
	ConfigNamespace string
	// Config is a pointer to this carrier's input-item prototype, carrying the
	// values an absent key takes.
	Config *C
	// Items describes each declared item, keyed by its path within the namespace.
	Items map[string]config.Item
	// Stance judges this carrier's own configuration before the selection item is
	// applied: StateEnabled when it can run, StateDisabled when the section is
	// wholly unset, and an error when it is half-configured.
	Stance func(cfg *C) (core.Enablement, error)
	// Secrets returns the values this carrier holds that have to be registered as
	// literal redaction rules. It is optional.
	Secrets func(cfg *C) []string
	// Open builds this carrier from the decoded configuration.
	Open func(ctx context.Context, cfg *C) (Sender, error)
}

// NewModule builds a carrier's module descriptor. A defective Spec panics here,
// because the fault is in this repository's own code.
func NewModule[C any](spec Spec[C]) core.Module {
	spec.check()
	return core.Module{
		Name:      spec.ModuleName,
		Requires:  []core.Requirement{{Token: (*log.Logger)(nil), Optional: true}},
		Provides:  []core.Provision{{Token: (*Sender)(nil), Exclusive: true}},
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
		panic("mail: a Spec carries the module name this carrier registers under")
	case s.CarrierName == "":
		panic(fmt.Sprintf("mail: module %q gives no carrier name, and mail.carrier selects a carrier by "+
			"that name", s.ModuleName))
	case s.ConfigNamespace == "":
		panic(fmt.Sprintf("mail: module %q gives no configuration namespace, and its input items have "+
			"to hang under one of its own: carriers sharing a namespace conflict as soon as a host "+
			"imports both", s.ModuleName))
	case s.Config == nil:
		panic(fmt.Sprintf("mail: module %q gives no configuration prototype, which is what its accepted "+
			"input items and their defaults are read from; a carrier that accepts nothing still mounts "+
			"an empty struct here", s.ModuleName))
	case s.Stance == nil:
		panic(fmt.Sprintf("mail: module %q gives no stance, so nothing states what it cannot start without",
			s.ModuleName))
	case s.Open == nil:
		panic(fmt.Sprintf("mail: module %q gives no constructor, so nothing reaches its carrier", s.ModuleName))
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
