package objectstore

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

// Spec is what a backend subpackage hands this package to become a module.
// It is the whole of what a backend contributes.
type Spec[C any] struct {
	// ModuleName is this backend's name in the registry: its identity in the startup diagnostics and
	// in an enablement lookup.
	ModuleName string
	// ConfigNamespace is where this backend's input items are mounted in the config data.
	ConfigNamespace string
	// Config is a pointer to this backend's input-item prototype: the struct config fills, carrying
	// the values an absent key takes.
	// The schema reads the accepted keys and their defaults from it.
	Config *C
	// Items describes each declared item, keyed by its path within the namespace.
	Items map[string]config.Item
	// Stance reads the decoded configuration and states what this backend cannot start without.
	// A backend taken in at run level has to start, so the state it returns is StateEnabled: two
	// backends enabled at once fail the startup instead of one silently winning the byte seam.
	Stance func(cfg *C) (core.Enablement, error)
	// Secrets returns the values this backend holds that have to be registered as literal redaction
	// rules, so a credential that turns up in any log line of the process is masked.
	Secrets func(cfg *C) []string
	// Direct says the value Open returns also implements DirectURL.
	// The module then declares that capability beside the byte seam, so an assembly can resolve it and
	// the factory's delivery check covers it.
	Direct bool
	// Open builds this backend from the decoded configuration.
	// It returns the one value that implements Store, and DirectURL as well when Direct is set.
	Open func(ctx context.Context, cfg *C, logger *slog.Logger) (Store, error)
}

// NewModule builds a backend's module descriptor.
func NewModule[C any](spec Spec[C]) core.Module {
	spec.check()
	provides := []core.Provision{{Token: (*Store)(nil), Exclusive: true}}
	if spec.Direct {
		provides = append(provides, core.Provision{Token: (*DirectURL)(nil), Exclusive: true})
	}
	return core.Module{
		Name:      spec.ModuleName,
		Requires:  []core.Requirement{{Token: (*log.Logger)(nil), Optional: true}},
		Provides:  provides,
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
			if err = spec.registerSecrets(reg, cfg); err != nil {
				return nil, err
			}
			logger, err := spec.logger(reg)
			if err != nil {
				return nil, err
			}
			return spec.Open(ctx, cfg, logger)
		},
	}
}

func (s Spec[C]) check() {
	switch {
	case s.ModuleName == "":
		panic("objectstore: a Spec carries the module name this backend registers under")
	case s.ConfigNamespace == "":
		panic(fmt.Sprintf("objectstore: module %q gives no configuration namespace, and its input items "+
			"have to hang under one of its own: backends sharing a namespace conflict as soon as a host "+
			"imports both", s.ModuleName))
	case s.Config == nil:
		panic(fmt.Sprintf("objectstore: module %q gives no configuration prototype, which is what its "+
			"accepted input items and their defaults are read from; a backend that accepts nothing still "+
			"mounts an empty struct here", s.ModuleName))
	case s.Stance == nil:
		panic(fmt.Sprintf("objectstore: module %q gives no stance, so nothing states what it cannot "+
			"start without", s.ModuleName))
	case s.Open == nil:
		panic(fmt.Sprintf("objectstore: module %q gives no constructor, so nothing reaches its backend",
			s.ModuleName))
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
		return nil, fmt.Errorf("%s: this backend's input items are read from configuration, and the host "+
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

func (s Spec[C]) logger(reg *core.Registry) (*slog.Logger, error) {
	delivered, err := core.Resolve[log.Logger](reg)
	switch {
	case err == nil:
		return delivered.Named(s.ModuleName), nil
	case errors.Is(err, core.ErrMissingProvider):
		return log.Default(), nil
	default:
		return nil, fmt.Errorf("%s: taking up the logger its calls are reported through: %w",
			s.ModuleName, err)
	}
}
