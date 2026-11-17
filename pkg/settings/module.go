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

// migrationsFS is migrationRoot with the wrapping directory removed, so the
// tree the database module sees has one directory per dialect at its root,
// which is the shape it reads.
var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive above is what makes this unreachable: the
		// directory is part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

// The module registers itself. Importing this package is what puts settings in
// a binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The capability is delivered exclusively: settings are one process-wide
// surface, and two providers of it would be two snapshots a read could land
// in with nothing to choose between them, so a second one fails the startup
// instead.
//
// Both construction-time dependencies are hard. The table lives in the
// database, and the change event is the module's own way of telling a replica
// that its snapshot is stale — a settings module without a bus cannot
// propagate a change at all. A run missing either is a resolution failure
// rather than a module that quietly does nothing.
//
// Logging is not declared. It is taken up in Init, is optional, and falls back
// to the process default logger, because this module's records are about its
// own refresh failures rather than about anything a caller asked for.
//
// There is no stand-down: the one input item has a default, so nothing can be
// missing, and a command that does not want settings simply does not take this
// module in.
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

		// New builds the key table and nothing else. The table's rows are
		// read in Init: this stage runs before migrations are applied, so the
		// settings table need not exist yet.
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

		// Init subscribes and loads. The subscription belongs to this stage
		// and to no other: a bus freezes its subscription table when it
		// starts, so a subscriber that arrived later would miss every event
		// published before it and nothing would report the gap.
		Init: func(ctx context.Context, reg *core.Registry, instance any) error {
			s, ok := productOf(instance)
			if !ok {
				return nil
			}
			s.takeLogger(reg)
			s.subscribe()
			return s.load(ctx)
		},

		// Start loads the snapshot a second time, then starts the refresh.
		//
		// A transport bus creates its consumers at its own Start and hands
		// them only what is published from then on, while the first load ran
		// in Init, before any module started. A change another replica
		// published in between is delivered to nobody here, and with the
		// refresh off this replica would serve the value from before it until
		// a restart. This module requires the bus and therefore starts after
		// it, so a load now covers every change written before the consumers
		// existed, and the consumers cover every change after. Its failure
		// stops the process for the reason load gives.
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

		// Nothing to release. The handle belongs to the database module, which
		// closes it, and the subscription ends with the bus.
		Close: func(_ context.Context, _ *core.Registry, _ any) error { return nil },
	}
}

// newSettingsStore builds the product.
func newSettingsStore(keys *keyTable, database db.Database, bus event.Bus, cfg moduleSettings) *settingsStore {
	s := &settingsStore{
		keys:          keys,
		handle:        database.DB(),
		bus:           bus,
		config:        cfg,
		logger:        log.Default(),
		connectWithin: connectionWait,
	}
	// The snapshot stays unset until the first load: a store that has read
	// nothing from the table refuses a read rather than answering from the
	// declared defaults.
	return s
}

// takeLogger picks up the logging capability if the assembly has one.
//
// It is optional on purpose: this module's records are its own diagnostics,
// and a host that never assembled logging still gets a working settings
// surface whose refresh failures reach the process default logger.
func (s *settingsStore) takeLogger(reg *core.Registry) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		return
	}
	s.logger = delivered.Named(ModuleName)
}

// subscribe registers the reload handler for this module's own change events.
//
// The handler runs wherever the bus calls it, which on some backends is the
// publisher's own goroutine, so it does one thing and returns: replace the
// snapshot. That is safe to do whenever it runs, because a replica's own
// events come back to it and the replacement is idempotent.
func (s *settingsStore) subscribe() {
	s.bus.Subscribe(ChangedEventType, ModuleName, func(ctx context.Context, _ event.Event) error {
		s.reload(ctx)
		return nil
	})
}

// load replaces the snapshot and reports why it could not.
//
// The two startup loads, in Init and in Start, are the ones whose failure
// stops the process. A snapshot that could not be read at startup means every
// read would answer from defaults, or from a state that may have missed a
// change no event will bring, while the database holds something else, and a
// module serving values it cannot vouch for is worse than one that refuses to
// start. Later loads — a write, a delivered event, the ticker — keep the
// previous snapshot instead.
func (s *settingsStore) load(ctx context.Context) error {
	_, err := s.swap(ctx)
	return err
}

// readerOf takes up the configuration reader.
//
// A reader that is not there means the host never supplied one as the instance
// of the config module, which is the host's omission rather than something to
// read as "nothing was configured". The failure names this module, since a run
// can carry several modules each wanting a reader. The name stands in for the
// capability directory here as well: this one registers from its own root
// package, so the two are the same string and writing both would say it twice.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	return reader, nil
}

// productOf narrows an instance to this module's product. An instance of
// another shape is one the host supplied for this module, such as a stand-in
// store built for a test, and the callbacks leave it alone: nothing is
// subscribed, loaded, started or stopped for it, and what it holds is released
// by whoever supplied it.
func productOf(instance any) (*settingsStore, bool) {
	s, ok := instance.(*settingsStore)
	return s, ok
}
