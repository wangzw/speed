// Package registry is the template registry module: it holds the set of SMS
// templates in force, decides which one a message goes out under, and manages
// the ones this process owns.
//
// It carries two sources with two different owners. The configuration source is
// a template set the host writes into this module's own `templates` item; it is
// read once at startup, frozen into an sms.Declaration and read-only for the
// life of the process — the operator who wrote it has already had the vendor
// approve it, so this module neither manages it nor tracks its review state.
// The runtime source is this module's own sms_templates table: templates the
// operator registers, hands to the vendor, enables and removes while the
// process runs, each carrying the vendor's review state.
//
// The capability it delivers, sms.Registry, is separate from the sending seam:
// a Registry knows what is approved and what a message resolves to, reaches the
// vendor only through the sms.Provider seam, and a consumer takes both — Resolve
// from here, then Send from a carrier.
//
// It is deliberately not built on pkg/settings. The assembly-time key set of
// settings fixes which parameters exist at startup, and its container values
// never enter the settings tree at all (ADR-settings-declaration), so
// a set of templates that grows and shrinks at runtime cannot be expressed
// there.
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

// ModuleName is this module's name in the registry. A host writes it into the
// command that manages templates and resolves messages through them.
const ModuleName = "sms.registry"

// ConfigNamespace is where this module's input items are mounted, which is the
// module name.
const ConfigNamespace = ModuleName

// tableName is the runtime template table. It is written here rather than
// derived from a Go type because nothing in this module goes through the model
// path: the statements are native SQL, so the name is stated once for the
// writers and once in each dialect's migration file, and the three are kept in
// step by hand.
const tableName = "sms_templates"

// ChangedEventType is the type name of the change event published after every
// successful write to the runtime set. A replica subscribes to reload its
// snapshot; the ticker is the backstop under a change the event never brings.
const ChangedEventType = "sms.registry.changed"

// ErrNotLoaded reports a read made before this module's Init first loaded the
// runtime snapshot. It says the state is unavailable, never that the set is
// empty or that a template is refused: a caller that acted on an empty answer
// would treat every runtime template as absent until a reload it never saw.
var ErrNotLoaded = errors.New("sms.registry: the runtime template snapshot has not been loaded yet")

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

// init registers this module with the process registry. Importing the package
// is what puts the template registry in a binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// The product must deliver what the descriptor declares.
var _ sms.Registry = (*registryStore)(nil)

// Module is the descriptor a host registers.
//
// The template-management seam and the database are required and hard. A
// registry without a provider can neither offer a template nor read a review
// state, and a runtime set without a table has nowhere to live; a run missing
// either is a resolution failure rather than a module that quietly does
// nothing. The provider is required non-optionally, and whichever carrier
// delivers it delivers it exclusively, so a command names exactly one carrier.
//
// The event bus and the logging capability are taken up in Init and are both
// optional: the ticker converges the replicas without an event, and a host with
// no logging still gets a working registry whose failures reach the process
// default logger. Logging is declared, optionally, so that an assembly that has
// it orders this module after the logging module and the records reach the
// configured logger rather than the process default. The settings store is
// declared optionally and taken up in New: the single sending switch is read on
// every Resolve, and a host with no settings still gets a working registry whose
// switch answers its declared default. The configuration reader is not declared
// here — the host supplies it before the drive begins, the way every module that
// reads its own section takes it up.
//
// The capability is delivered exclusively: the set in force is one
// process-wide surface, and two registries would be two answers to which
// template a message goes out under, so a second provider fails the startup.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*sms.Provider)(nil)},
			{Token: (*db.Database)(nil)},
			// Optional, because a host that assembles no settings still gets a
			// working registry whose sending switch answers its declared default;
			// declared, because the switch has to be read without a restart, and a
			// declared requirement orders this module after the settings module.
			{Token: (*settings.Store)(nil), Optional: true},
			// Optional, because a host that assembles no logging still gets a working
			// registry; declared, because a declared requirement orders this module after
			// the logging module, so an assembly that has one builds this module after it
			// and its records reach the configured logger instead of the process default.
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

		// New builds the product and reads no table of its own: this stage runs
		// before the migrations are applied, so on a first start the runtime
		// table is not there yet. The table is first read in Init. The configured
		// templates are validated against the mounted carrier's CarrierModel here,
		// which is the first stage the provider is reachable and the last before a
		// message could be sent under them.
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

		// Init subscribes, adopts any legacy rows, loads and starts the poller. The
		// subscription belongs to this stage: a bus freezes its subscription table
		// when it starts, so a subscriber that arrived later would miss every event
		// published before it. The load is here rather than in New because the
		// migrations have run by now, and the poller starts here because its first
		// pass needs the table and the loaded snapshot. The adoption runs before the
		// load, because it is what gives the legacy rows the carrier the load reads
		// them by.
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

		// Nothing to release. The handle belongs to the database module, which
		// closes it, and the subscription ends with the bus.
		Close: func(_ context.Context, _ *core.Registry, _ any) error { return nil },
	}
}

// productOf narrows an instance to this module's product. An instance of
// another shape is one the host supplied for this module, such as a stand-in
// built for a test, and the callbacks leave it alone.
func productOf(instance any) (*registryStore, bool) {
	s, ok := instance.(*registryStore)
	return s, ok && s != nil
}

// readerOf takes up the configuration reader.
//
// A reader that is not there means the host never supplied one as the instance
// of the config module, which is the host's omission rather than something to
// read as "nothing was configured".
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	return reader, nil
}

// newStore takes up what the descriptor requires and assembles the product. It
// reads no table: the runtime set is first read in Init, after the migrations.
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

// optionalSettings takes up the settings store if the assembly has one. A
// capability that is not there is a legal assembly and leaves the sending switch
// answering its declared default; a capability that is there and cannot be taken
// up is not an absence and ends the start.
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

// validateConfigured holds the configuration template set to the mounted
// carrier's CarrierModel. It runs in New, where the provider is reachable and
// before any message could be sent under a template this carrier cannot carry.
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

// takeLogger picks up the logging capability if the assembly has one.
//
// It is optional on purpose: this module's records are its own diagnostics —
// a reload that failed, a vendor call that failed — and a host that never
// assembled logging still gets a working registry whose records reach the
// process default logger.
func (s *registryStore) takeLogger(reg *core.Registry) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		return
	}
	s.logger = delivered.Named(ModuleName)
	s.tuned.logger = s.logger
}

// takeBus picks up the event bus if the assembly has one. Without it the
// replicas converge through the poller's ticker alone.
func (s *registryStore) takeBus(reg *core.Registry) {
	delivered, err := core.Resolve[event.Bus](reg)
	if err != nil {
		return
	}
	s.bus = delivered
}
