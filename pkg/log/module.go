package log

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// ModuleName is this module's name in the registry, and the word a command takes this module in
// by.
const ModuleName = "log"

var bootstrapLevel = new(slog.LevelVar)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not inherit the
// process-level registrations.
// It delivers Logger exclusively and states StateEnabled.
func Module() core.Module {
	return core.Module{
		Name:      ModuleName,
		Provides:  []core.Provision{{Token: (*Logger)(nil), Exclusive: true}},
		Resources: []any{schema()},
		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf(
					"log: the level is read from configuration, and the reader a host supplies "+
						"to the registry before assembly is not in it: %w", err)
			}
			return prepare(reader)
		},
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return nil, fmt.Errorf(
					"log: the handler chain is assembled from configuration, and the reader a host "+
						"supplies to the registry before assembly is not in it: %w", err)
			}
			return newLogger(reader)
		},
		Close: func(_ context.Context, _ *core.Registry, instance any) error {
			return closeLogger(instance)
		},
	}
}

func prepare(reader config.Reader) (core.Enablement, error) {
	var cfg Config
	if err := reader.Decode(configPath, &cfg); err != nil {
		return core.Enablement{}, err
	}
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return core.Enablement{}, err
	}
	bootstrapLevel.Set(level)
	return core.Enablement{State: core.StateEnabled}, nil
}

// ModuleAttrKey is the key of the attribute Named binds.
// It identifies which module wrote a record, which is what the key says; it is not the logger's
// own name.
const ModuleAttrKey = "module"

type logger struct {
	chain   slog.Handler
	release func()
}

var _ Logger = (*logger)(nil)

func (l *logger) Named(name string) *slog.Logger {
	return slog.New(l.chain).With(ModuleAttrKey, name)
}

func (l *logger) Redaction() Redaction { return processRedaction }

func newLogger(reader config.Reader) (*logger, error) {
	var cfg Config
	if err := reader.Decode(configPath, &cfg); err != nil {
		return nil, err
	}
	resolved, err := cfg.resolve()
	if err != nil {
		return nil, err
	}
	assembled, err := newChain(resolved)
	if err != nil {
		return nil, err
	}
	if len(resolved.outputs) == 0 {
		report("the configuration gives an empty output list, so no log records are written anywhere; " +
			"remove the outputs key to get the default output to standard output")
	}
	return &logger{chain: assembled.handler, release: assembled.release}, nil
}

func closeLogger(instance any) error {
	product, ok := instance.(*logger)
	if !ok || product == nil {
		return nil
	}
	product.release()
	return nil
}
