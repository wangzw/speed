package settings

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/log"
)

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

// Module is the descriptor a host registers. It delivers the settings surface
// exclusively, requires the database and the event bus, and takes logging
// optionally in Init.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*event.Bus)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides:  []core.Provision{{Token: (*Store)(nil), Exclusive: true}},
		Resources: []any{schema(), db.Migrations{FS: migrationsFS}},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
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
			reader, err := readerOf(reg)
			if err != nil {
				return nil, err
			}
			set, err := readSettings(reader)
			if err != nil {
				return nil, err
			}
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return nil, err
			}
			keys, err := buildKeyTable(reg)
			if err != nil {
				return nil, err
			}
			return newSettingsStore(keys, database, bus, set), nil
		},

		Init: func(ctx context.Context, reg *core.Registry, instance any) error {
			s, ok := productOf(instance)
			if !ok {
				return nil
			}
			s.takeLogger(reg)
			s.subscribe()
			return s.load(ctx)
		},

		Start: func(ctx context.Context, _ *core.Registry, instance any) error {
			s, ok := productOf(instance)
			if !ok {
				return nil
			}
			if err := s.load(ctx); err != nil {
				return err
			}
			s.startRefresh(ctx)
			return nil
		},

		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			if s, ok := productOf(instance); ok {
				s.stopRefresh()
			}
			return nil
		},

		Close: func(_ context.Context, _ *core.Registry, _ any) error { return nil },
	}
}

func newSettingsStore(keys *keyTable, database db.Database, bus event.Bus, cfg moduleSettings) *settingsStore {
	s := &settingsStore{
		keys:          keys,
		handle:        database.DB(),
		bus:           bus,
		config:        cfg,
		logger:        log.Default(),
		connectWithin: connectionWait,
	}
	return s
}

func (s *settingsStore) takeLogger(reg *core.Registry) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		return
	}
	s.logger = delivered.Named(ModuleName)
}

func (s *settingsStore) subscribe() {
	s.bus.Subscribe(ChangedEventType, ModuleName, func(ctx context.Context, _ event.Event) error {
		s.reload(ctx)
		return nil
	})
}

func (s *settingsStore) load(ctx context.Context) error {
	_, err := s.swap(ctx)
	return err
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	return reader, nil
}

func productOf(instance any) (*settingsStore, bool) {
	s, ok := instance.(*settingsStore)
	return s, ok
}
