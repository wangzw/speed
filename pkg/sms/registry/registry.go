// Package registry is the template registry module: it holds the set of SMS
// templates in force, decides which one a message goes out under, and manages the
// ones this process owns.
package registry

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/sms"
)

// ModuleName is this module's name in the registry.
const ModuleName = "sms.registry"

// ConfigNamespace is where this module's input items are mounted, which is the module name.
const ConfigNamespace = ModuleName

// tableName is the runtime template table, stated here and in each dialect
// migration file; the three are kept in step by hand.
const tableName = "sms_templates"

// ChangedEventType is the type name of the change event published after every
// successful write to the runtime set.
const ChangedEventType = "sms.registry.changed"

// ErrNotLoaded reports a read made before Init first loaded the runtime snapshot:
// the state is unavailable, never that the set is empty.
var ErrNotLoaded = errors.New("sms.registry: the runtime template snapshot has not been loaded yet")

//go:embed migrations
var migrationRoot embed.FS

var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

func init() { core.ProcessRegistry.Register(Module()) }

var _ sms.Registry = (*registryStore)(nil)

// Module is the descriptor a host registers. It requires the template-management
// seam and the database, takes the event bus, logging and settings store
// optionally, and delivers sms.Registry exclusively.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*sms.Provider)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides:  []core.Provision{{Token: (*sms.Registry)(nil), Exclusive: true}},
		Resources: []any{schema(), settingsSchema(), db.Migrations{FS: migrationsFS}},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			off, reason, err := sms.OffBySelection(reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("%s: reading the SMS carrier selection: %w", ModuleName, err)
			}
			if off {
				return core.Enablement{State: core.StateDisabled, Reason: reason}, nil
			}
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if _, err := readSettings(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			s, err := newStore(reg)
			if err != nil {
				return nil, err
			}
			if err := s.validateConfigured(); err != nil {
				return nil, err
			}
			return s, nil
		},

		Init: func(ctx context.Context, reg *core.Registry, instance any) error {
			s, ok := productOf(instance)
			if !ok {
				return nil
			}
			s.takeLogger(reg)
			s.takeBus(reg)
			s.subscribe()
			if err := s.adopt(ctx); err != nil {
				return err
			}
			if err := s.load(ctx); err != nil {
				return err
			}
			s.startPoll(ctx)
			return nil
		},

		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			if s, ok := productOf(instance); ok {
				s.stopPoll()
			}
			return nil
		},

		Close: func(_ context.Context, _ *core.Registry, _ any) error { return nil },
	}
}

func productOf(instance any) (*registryStore, bool) {
	s, ok := instance.(*registryStore)
	return s, ok && s != nil
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	return reader, nil
}

func newStore(reg *core.Registry) (*registryStore, error) {
	reader, err := readerOf(reg)
	if err != nil {
		return nil, err
	}
	settings, err := readSettings(reader)
	if err != nil {
		return nil, err
	}
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the database: %w", ModuleName, err)
	}
	provider, err := core.Resolve[sms.Provider](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the template-management seam: %w", ModuleName, err)
	}
	store, err := optionalSettings(reg)
	if err != nil {
		return nil, err
	}
	return &registryStore{
		handle:   database.DB(),
		provider: provider,
		config:   settings,
		tuned:    newSettingsReader(store, nil),
		logger:   log.Default(),
	}, nil
}

func optionalSettings(reg *core.Registry) (settings.Store, error) {
	store, err := core.Resolve[settings.Store](reg)
	switch {
	case err == nil:
		return store, nil
	case errors.Is(err, core.ErrMissingProvider):
		return nil, nil
	default:
		return nil, fmt.Errorf("%s: taking up the settings store: %w", ModuleName, err)
	}
}

func (s *registryStore) validateConfigured() error {
	if len(s.config.specs) == 0 {
		return nil
	}
	model := s.provider.Model()
	names := make([]string, 0, len(s.config.specs))
	for name := range s.config.specs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := validateAgainstModel(name, s.config.specs[name], model, model.Review.PerTemplate); err != nil {
			return fmt.Errorf("%s.%s: %w", ConfigNamespace, templatesItem, err)
		}
	}
	return nil
}

func (s *registryStore) takeLogger(reg *core.Registry) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		return
	}
	s.logger = delivered.Named(ModuleName)
	s.tuned.logger = s.logger
}

func (s *registryStore) takeBus(reg *core.Registry) {
	delivered, err := core.Resolve[event.Bus](reg)
	if err != nil {
		return
	}
	s.bus = delivered
}
