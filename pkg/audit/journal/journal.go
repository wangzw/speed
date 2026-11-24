// Package journal is the audit module: the capture paths that reach
// facts, the table they are written into, and the seam other modules hand
// entries to.
package journal

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// ModuleName is this module's name in the registry. A host writes it into the
// commands whose work should leave an audit trail.
const ModuleName = "audit.journal"

// ConfigNamespace is where this module's input items are mounted. It is this
// module's own name, the way a registering module's namespace always is.
const ConfigNamespace = ModuleName

const diagnosticPrefix = "audit: "

//go:embed migrations
var migrationRoot embed.FS

// migrationsFS is migrationRoot with the wrapping directory removed, so that
// the tree the database module sees has one directory per dialect at its root,
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

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*log.Logger)(nil)},
		},
		Provides: []core.Provision{
			{Token: (*audit.Audit)(nil), Exclusive: true},
			{Token: (*Reader)(nil), Exclusive: true},
		},
		Resources: []any{schema(), db.Migrations{FS: migrationsFS}, db.Plugin{Plugin: newCapture()}},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				// A stance handed back beside a failure is not read, so the failing
				// paths return the zero Enablement with their error.
				return core.Enablement{}, err
			}
			if _, err := readSettings(reader); err != nil {
				return core.Enablement{}, err
			}
			return stance(), nil
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
			delivered, err := core.Resolve[log.Logger](reg)
			if err != nil {
				return nil, err
			}
			// Registered before the logger is taken, because taking one binds
			// an attribute and a binding is judged as it is made.
			delivered.Redaction().AddKeys(maskedKeys()...)
			return newJournal(set, database.DB(), delivered.Named(ModuleName)), nil
		},

		// The capture paths are wired here and not in New.
		Init: func(ctx context.Context, reg *core.Registry, instance any) error {
			j, ok := productOf(instance)
			if !ok {
				return nil
			}
			return j.wire(ctx, reg)
		},

		// The three paths this module drives begin accepting here; statement
		// capture is gated by whether the plugin was installed instead.
		Start: func(_ context.Context, _ *core.Registry, instance any) error {
			if j, ok := productOf(instance); ok {
				j.accepting.Store(true)
			}
			return nil
		},

		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			if j, ok := productOf(instance); ok {
				j.accepting.Store(false)
			}
			return nil
		},

		// Nothing to release: the handle belongs to the database module, which
		// closes it.
		Close: func(_ context.Context, _ *core.Registry, _ any) error { return nil },
	}
}

// stance is what this module states in Prepare: StateEnabled, and never
// StateAuto.
func stance() core.Enablement { return core.Enablement{State: core.StateEnabled} }

type journal struct {
	settings      settings
	handle        *gorm.DB
	logger        *slog.Logger
	signal        *signal
	accepting     atomic.Bool
	connectWithin time.Duration
}

// newJournal builds the product. It holds configuration and nothing derived
// from it: the capture layer is built in Init, once the capabilities it needs
// have been taken up.
func newJournal(set settings, handle *gorm.DB, logger *slog.Logger) *journal {
	return &journal{
		settings:      set,
		handle:        handle,
		logger:        logger,
		signal:        newSignal(),
		connectWithin: connectionWait,
	}
}

func (j *journal) wire(_ context.Context, reg *core.Registry) error {
	if len(j.settings.endpoints) > 0 {
		router, err := core.Resolve[speedhttp.Router](reg)
		switch {
		case errors.Is(err, core.ErrMissingProvider):
			j.logger.Info(diagnosticPrefix+ModuleName+": the run has no HTTP entry point, so no request-level layer is registered",
				"endpoints", j.settings.endpoints)
		case err != nil:
			return fmt.Errorf("%s%s: the %s input item names endpoints, but %w",
				diagnosticPrefix, ModuleName, endpointsItem, err)
		default:
			if err := j.registerRequestCapture(router); err != nil {
				return err
			}
		}
	}

	if len(j.settings.events) > 0 {
		bus, err := core.Resolve[event.Bus](reg)
		if err != nil {
			return fmt.Errorf("%s%s: the %s input item names event types, but %w",
				diagnosticPrefix, ModuleName, eventsItem, err)
		}
		j.subscribe(bus)
	}

	j.logger.Info(diagnosticPrefix+ModuleName+": capture in force",
		"endpoints", j.settings.endpoints,
		"requests", len(j.settings.requests),
		"events", len(j.settings.events))
	return nil
}

func (j *journal) registerRequestCapture(router speedhttp.Router) error {
	for _, name := range unique(j.settings.endpoints) {
		endpoint, err := router.Endpoint(name)
		if err != nil {
			return fmt.Errorf("%s%s: %s lists the endpoint %q: %w",
				diagnosticPrefix, ModuleName, endpointsItem, name, err)
		}
		endpoint.Use(j.middlewareFor(name))
	}
	return nil
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the configuration reader: %w",
			diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}

func productOf(instance any) (*journal, bool) {
	j, ok := instance.(*journal)
	return j, ok
}

func unique(names []string) []string {
	if len(names) < 2 {
		return names
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}
