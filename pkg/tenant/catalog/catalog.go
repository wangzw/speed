// Package catalog carries the roster: the table of tenants that exist, the
// snapshot of it that requests are resolved against, and the statement filter
// that every scoped model passes through.
//
// It is one module rather than two because the table and the filter are the
// same capability directory's presence on the database side, and neither is
// without the other: a filter with no roster filters to a tenant nothing can
// enumerate, and a roster with no filter is a table of names. Splitting them
// would put a second name on the command tree whose omission is silent in
// exactly the same way the first one's is.
//
// A host takes this module in to have tenants at all. A command that runs and
// never listens still takes it in directly — its tables are built either way,
// but only a module that runs has its plugin installed, and a filter is what
// makes a statement scoped. A command whose work is applying migrations reaches
// it for its declarations alone, which is what collects its migration set
// without constructing it.
package catalog

import (
	"context"
	"embed"
	"io/fs"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry. A host writes it into the
// commands that carry tenants.
const ModuleName = "tenant.catalog"

// ConfigNamespace is where this module's input items are mounted. It is this
// module's own name, the way a registering module's namespace always is.
const ConfigNamespace = ModuleName

// diagnosticPrefix starts every line this module writes to the startup
// diagnostics, which have several writers. It names the capability directory
// rather than the module, and the module follows it in the line itself.
const diagnosticPrefix = "tenant: "

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

// The module registers itself. Importing this package is what puts the roster
// in a binary's available set: a host that never imports it carries no tenant
// concept at all, no table and no filter, which is the whole of what
// single-tenant means here.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The capability is delivered exclusively. Two rosters in one assembly are two
// accounts of which tenants exist, and a tenant one of them serves and the
// other does not is answered differently depending on which one a request
// reached — a difference nothing could resolve, because there is no fact of the
// matter to resolve it against.
//
// The database capability is a construction-time dependency, so a command that
// takes this module in without an engine fails at dependency resolution rather
// than at any callback here. That is why no sentinel in this capability
// directory names it: a run that cannot resolve one never reaches code that
// could report it.
//
// There is no stand-down. The configuration has no item that could be missing —
// the refresh period has a default — so a command that takes this module in
// gets a roster, and a command that does not want one leaves it out.
func Module() core.Module {
	return core.Module{
		Name:      ModuleName,
		Requires:  []core.Requirement{{Token: (*db.Database)(nil)}},
		Provides:  []core.Provision{{Token: (*tenant.Catalog)(nil), Exclusive: true}},
		Resources: []any{schema(), db.Migrations{FS: migrationsFS}, db.Plugin{Plugin: newFilter()}},

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
			return newRoster(set, database), nil
		},

		// The table is read here and not in New. Migrations are applied in
		// Migrate, which runs after every product is constructed, so the
		// table does not exist until this stage. Reading it earlier would
		// mean reading a schema the run has not built yet.
		Init: func(ctx context.Context, reg *core.Registry, instance any) error {
			r, ok := productOf(instance)
			if !ok {
				return nil
			}
			r.takeLogger(reg)
			return r.load(ctx)
		},

		// The refresher starts after every module is wired, so nothing it
		// does can race a module still being set up.
		Start: func(_ context.Context, _ *core.Registry, instance any) error {
			r, ok := productOf(instance)
			if !ok {
				return nil
			}
			r.start()
			return nil
		},

		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			if r, ok := productOf(instance); ok {
				r.stop()
			}
			return nil
		},
	}
}

// readerOf takes up the configuration reader. A reader that is not there means
// the host never supplied one as the instance of the config module, which is the
// host's omission and is carried out as it stands rather than read as "nothing
// was configured".
func readerOf(reg *core.Registry) (config.Reader, error) {
	return core.Resolve[config.Reader](reg)
}

// productOf narrows an instance to this module's product. An instance of another
// shape is one the host supplied for this module, and the callbacks leave it
// alone: no snapshot is loaded for it and no refresher is started or stopped,
// and what it holds is released by whoever supplied it.
func productOf(instance any) (*roster, bool) {
	r, ok := instance.(*roster)
	return r, ok
}
