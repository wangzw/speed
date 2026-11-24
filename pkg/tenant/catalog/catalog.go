// Package catalog carries the roster: the table of tenants that exist, the
// snapshot requests resolve against, and the statement filter every scoped
// model passes through. A host takes this module in to have tenants at all.
package catalog

import (
	"context"
	"embed"
	"io/fs"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry.
const ModuleName = "tenant.catalog"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

var systemTenant = tenant.ID(authz.PlatformDomain)

const diagnosticPrefix = "tenant: "

//go:embed migrations
var migrationRoot embed.FS

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

// Module is the descriptor a host registers. The capability is delivered
// exclusively, and it requires the database at construction time. There is no
// stand-down: the configuration has no item that could be missing.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*tenant.Catalog)(nil), Exclusive: true},
			{Token: (*TxCatalog)(nil), Exclusive: true},
			{Token: (*tenant.NameResolver)(nil), Exclusive: true},
			{Token: (*authn.TenantNameResolver)(nil), Exclusive: true},
			{Token: (*authn.TenantDirectory)(nil), Exclusive: true},
			{Token: (*authn.TenantStateResolver)(nil), Exclusive: true},
			{Token: (*authn.TenantDescriptionResolver)(nil), Exclusive: true},
			{Token: (*authz.TenantNameResolver)(nil), Exclusive: true},
		},
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

		// The table is read here and not in New: migrations are applied in
		// Migrate, after every product is constructed.
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

func readerOf(reg *core.Registry) (config.Reader, error) {
	return core.Resolve[config.Reader](reg)
}

func productOf(instance any) (*roster, bool) {
	r, ok := instance.(*roster)
	return r, ok
}
