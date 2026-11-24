// Package metadata is the object store's metadata and lifecycle module: it holds the objects and grants tables and delivers the objectstore.Objects seam exclusively.
package metadata

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/objectstore"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry.
// A host writes it into the command that assembles an object store, and the configuration
// namespace its input items hang from is the same string.
const ModuleName = "objectstore.metadata"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

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

var _ objectstore.Objects = (*product)(nil)

// Module is the descriptor a host registers.
// The database, the tenant roster and the byte seam are required and hard.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*objectstore.Store)(nil)},
			{Token: (*objectstore.DirectURL)(nil), Optional: true},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*speedhttp.Router)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{{Token: (*objectstore.Objects)(nil), Exclusive: true}},
		Resources: []any{
			schema(),
			settingsSchema(),
			db.Migrations{FS: migrationsFS},
		},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if _, err := readConfig(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newProduct(reg)
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.wire(reg)
		},

		Start: func(ctx context.Context, _ *core.Registry, instance any) error {
			if p, ok := productOf(instance); ok {
				p.startSweep(ctx)
			}
			return nil
		},

		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			if p, ok := productOf(instance); ok {
				p.stopSweep()
			}
			return nil
		},
	}
}

func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

type product struct {
	handle  *gorm.DB
	stmts   statements
	bytes   objectstore.Store
	catalog tenant.Catalog
	direct  objectstore.DirectURL

	tuned            *settingsReader
	sweepInterval    time.Duration
	contentEndpoints []string
	publicURL        string
	contentMounted   bool

	logger *slog.Logger

	cancelSweep context.CancelFunc
	sweepDone   chan struct{}
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	return reader, nil
}

func newProduct(reg *core.Registry) (*product, error) {
	reader, err := readerOf(reg)
	if err != nil {
		return nil, err
	}
	cfg, err := readConfig(reader)
	if err != nil {
		return nil, err
	}
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the database: %w", ModuleName, err)
	}
	bytes, err := core.Resolve[objectstore.Store](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the byte seam: %w", ModuleName, err)
	}
	roster, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant roster: %w", ModuleName, err)
	}
	stmts, err := statementsFor(database.Dialect())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ModuleName, err)
	}
	direct, _, err := optional[objectstore.DirectURL](reg, "the direct-URL capability")
	if err != nil {
		return nil, err
	}
	store, _, err := optional[settings.Store](reg, "the settings store")
	if err != nil {
		return nil, err
	}
	return &product{
		handle:           database.DB(),
		stmts:            stmts,
		bytes:            bytes,
		catalog:          roster,
		direct:           direct,
		tuned:            newSettingsReader(store),
		sweepInterval:    cfg.sweepInterval,
		contentEndpoints: cfg.contentEndpoints,
		publicURL:        cfg.publicURL,
		logger:           log.Default(),
	}, nil
}

func optional[T any](reg *core.Registry, what string) (T, bool, error) {
	var zero T
	v, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return v, true, nil
	case errors.Is(err, core.ErrMissingProvider):
		return zero, false, nil
	default:
		return zero, false, fmt.Errorf("%s: taking up %s: %w", ModuleName, what, err)
	}
}

func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
	}

	if p.direct != nil {
		p.logger.Info(ModuleName + ": the byte backend mints direct addresses, so the content route is " +
			"not mounted")
		return nil
	}

	router, found, err := optional[speedhttp.Router](reg, "the HTTP entry point")
	if err != nil {
		return err
	}
	if !found {
		p.logger.Info(ModuleName + ": no byte backend mints direct addresses and no HTTP entry point is " +
			"assembled, so no URL can be minted")
		return nil
	}
	if err := validateContentEndpoints(p.contentEndpoints); err != nil {
		return err
	}
	if err := p.mount(router); err != nil {
		return err
	}
	p.logger.Info(ModuleName+": the content route is mounted", "endpoints", p.contentEndpoints)
	return nil
}
