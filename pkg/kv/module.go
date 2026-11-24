package kv

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/literal"
	"github.com/vislake/speed/pkg/log"
)

// Spec is what an implementation subpackage hands this package to become a module.
// It is the whole of what an implementation contributes.
type Spec[C any] struct {
	// ModuleName is this implementation's name in the registry: its identity in the startup
	// diagnostics and in an enablement lookup.
	ModuleName string
	// ConfigNamespace is where this implementation's input items are mounted in the config data.
	ConfigNamespace string
	// Config is a pointer to this implementation's input-item prototype: the struct config fills,
	// carrying the values an absent key takes.
	// The schema reads the accepted keys and their defaults from it.
	Config *C
	// Items describes each declared item, keyed by its path within the namespace.
	Items map[string]config.Item
	// Stance reads the decoded configuration and answers two things: how this implementation's
	// exclusive claim yields to another provider's, and what it cannot start without.
	Stance func(cfg *C) (core.Enablement, error)
	// Open builds this implementation from the decoded configuration.
	Open func(ctx context.Context, cfg *C) (KV, error)
	// Secrets returns the values this implementation holds as configuration that have to be registered
	// as literal redaction rules, so a credential that turns up in any log line of the process is
	// masked.
	Secrets func(cfg *C) []string
}

// NewModule builds an implementation's module descriptor.
func NewModule[C any](spec Spec[C]) core.Module {
	spec.check()
	return core.Module{
		Name:      spec.ModuleName,
		Requires:  []core.Requirement{{Token: (*log.Logger)(nil), Optional: true}},
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
			if err := spec.registerSecrets(reg, cfg); err != nil {
				return nil, err
			}
			return spec.Open(ctx, cfg)
		},
		Close: func(ctx context.Context, _ *core.Registry, instance any) error {
			return release(ctx, instance)
		},
	}
}

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
