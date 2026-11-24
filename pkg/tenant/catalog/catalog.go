// Package catalog carries the roster: the table of tenants that exist, the
// snapshot of it that requests are resolved against, and the statement filter
// that every scoped model passes through.
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
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

// The module registers itself on import; a binary that never imports this
// package carries no tenant concept.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It is delivered exclusively and
// requires the database capability at dependency resolution; it has no
// stand-down, so a command that takes it in always gets a roster.
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

		// The table is read here and not in New: migrations are applied after every
		// product is constructed.
		Init: func(ctx context.Context, reg *core.Registry, instance any) error {
			r, ok := productOf(instance)
			if !ok {
				return nil
			}
			r.takeLogger(reg)
			return r.load(ctx)
		},

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
