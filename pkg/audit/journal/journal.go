// Package journal is the audit module: the capture paths that reach facts, the
// table they are written into, and the seam other modules hand entries to.
//
// It is one module rather than several because the capture paths share a record
// shape and a table. Split into one module per path, the table and the writer
// would either be delivered by one of them and depended on by the rest — an
// internal relationship turned into a public contract — or owned several times
// over.
//
// The interface package registers nothing, and this one depends on the database
// module, the log module, the event bus, the entry point, authentication and
// tenancy. That split is what lets a module that only places a layer against
// the audit anchor name the capability without pulling this package's
// dependencies in — for the cost the anchor record states.
package journal

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sync/atomic"

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

// diagnosticPrefix starts every line this module writes to the startup
// diagnostics, which have several writers. It names the release unit rather
// than the module, and the module follows it in the line itself.
const diagnosticPrefix = "audit: "

// middlewareOrder places the capture layer where the constraints leave a
// choice. It is a preference and not a constraint: where the layer has to sit
// is decided by pointing at authentication and tenant resolution, and this
// value only decides where it lands when neither is in the assembly at all.
const middlewareOrder = 95

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

// The module registers itself. Importing this package is what puts audit in a
// binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The capability is delivered exclusively. Two journals in one assembly are two
// tables an entry could go to, and which one a capture path reached is not a
// fact anything could resolve — so the pair is deliberately unrepresentable and
// a second one fails the startup instead.
//
// Both construction-time dependencies are hard, and neither is decoration. The
// table is built on the handle the database module delivers, and the detail
// masking rule set is the log module's. A run missing either cannot have an
// audit trail at all, and that is a resolution failure rather than a path that
// quietly does nothing.
//
// The event bus and the entry point are taken up in Init instead of being
// declared. They exist already by then, so there is no order to settle, and
// declaring them would drag their providers into commands a host never listed
// them on.
//
// There is no stand-down. No input item can be missing — all three default to
// empty — so a command that takes this module in gets a journal, and a command
// that wants none leaves it out. That is also why the stance is never
// StateAuto: standing down means yielding to something explicitly enabled, and
// what enables this module is the host writing it into a command tree, not
// anything configuration could infer.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*log.Logger)(nil)},
		},
		Provides:  []core.Provision{{Token: (*audit.Audit)(nil), Exclusive: true}},
		Resources: []any{schema(), db.Migrations{FS: migrationsFS}, db.Plugin{Plugin: newCapture()}},

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
			delivered, err := core.Resolve[log.Logger](reg)
			if err != nil {
				return nil, err
			}
			// Registered before the logger is taken, because taking one binds
			// an attribute and a binding is judged as it is made.
			delivered.Redaction().AddKeys(maskedKeys()...)
			return newJournal(set, database.DB(), delivered.Named(ModuleName)), nil
		},

		// The capture paths are wired here and not in New. The entry point and
		// the bus are resolved at this stage, and both are taken up rather than
		// declared so that a host which never lists them is not made to carry
		// them.
		Init: func(ctx context.Context, reg *core.Registry, instance any) error {
			j, ok := productOf(instance)
			if !ok {
				return nil
			}
			return j.wire(ctx, reg)
		},

		// The three paths this module drives begin accepting here. The fourth —
		// statement capture — is not gated by this: its switch is whether the
		// plugin was installed, which happened while the database module was
		// constructing.
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

		// Nothing to release. The handle belongs to the database module, which
		// closes it; this module holds no connection of its own.
		Close: func(_ context.Context, _ *core.Registry, _ any) error { return nil },
	}
}

// journal is the module's product. It delivers the audit capability.
type journal struct {
	settings  settings
	handle    *gorm.DB
	logger    *slog.Logger
	signal    *signal
	matcher   *matcher
	accepting atomic.Bool
}

// newJournal builds the product. It holds configuration and nothing derived
// from it: the matcher and the capture layer are both built in Init, once the
// capabilities they need have been taken up.
func newJournal(set settings, handle *gorm.DB, logger *slog.Logger) *journal {
	return &journal{
		settings: set,
		handle:   handle,
		logger:   logger,
		signal:   newSignal(),
	}
}

// wire registers the capture layer on every guarded endpoint and subscribes to
// every named event type.
//
// Both capabilities are taken up rather than declared, which is what makes them
// optional in practice: a host that names an endpoint but never brings an entry
// point fails here, and the failure names the input item that asked for it. A
// host that names nothing gets neither path and a journal that still captures
// statements.
func (j *journal) wire(_ context.Context, reg *core.Registry) error {
	if len(j.settings.requests) > 0 {
		m, err := newMatcher(j.settings.requests)
		if err != nil {
			return fmt.Errorf("%s%s: the %s input item: %w",
				diagnosticPrefix, ModuleName, requestsItem, err)
		}
		j.matcher = m
	}

	if len(j.settings.endpoints) > 0 {
		router, err := core.Resolve[speedhttp.Router](reg)
		if err != nil {
			return fmt.Errorf("%s%s: the %s input item names endpoints, but %w",
				diagnosticPrefix, ModuleName, endpointsItem, err)
		}
		for _, name := range unique(j.settings.endpoints) {
			endpoint, err := router.Endpoint(name)
			if err != nil {
				return fmt.Errorf("%s%s: %s lists the endpoint %q: %w",
					diagnosticPrefix, ModuleName, endpointsItem, name, err)
			}
			endpoint.Use(j.middlewareFor(name))
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

// readerOf takes up the configuration reader.
//
// A reader that is not there means the host never supplied one, which is the
// host's omission and not something to read as "nothing was configured". The
// failure names this module, because a run can carry several modules each
// wanting a reader and the sentinel alone does not say which one was asking.
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the configuration reader: %w",
			diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}

// productOf narrows an instance to this module's product. A product of another
// shape means the registry handed this callback something it did not build,
// which is not something a host can cause.
func productOf(instance any) (*journal, bool) {
	j, ok := instance.(*journal)
	return j, ok
}

// unique drops repeated names, keeping the order they were first written in.
//
// A name written twice is the same request written twice, and passing it
// through is not harmless: a second layer on one endpoint records every request
// twice and stands for the audit capability a second time, and a second
// subscription to one event type delivers it twice. The matcher does the same
// for its patterns, for the same reason and one more — the router it is built
// on panics on a repeated registration.
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
