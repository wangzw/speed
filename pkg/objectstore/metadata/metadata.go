// Package metadata is the object store's metadata and lifecycle module: it
// holds the objects table and the grants table, carries an object through
// declaration, upload and completion, mints the addresses a browser uses to
// read or write its bytes, sweeps expired rows and grants, and reads its
// runtime tunables.
//
// It is the module that delivers the objectstore.Objects seam, exclusively. The
// bytes do not live here: they move through the objectstore.Store seam, which a
// backend module (objectstore.fs or objectstore.s3) delivers exclusively in
// turn. This module holds the metadata row, generates the opaque internal key
// the bytes are addressed by, and reaches the backend only through that seam. It
// imports no object-storage SDK, so a host compiles no backend but the one it
// deploys on.
//
// An object is written in three steps — declaration, upload, completion — and
// each is a call on Objects. A declaration validates what the caller states,
// lands a pending row and opens an upload window; an upload stores the bytes
// under the internal key and records the observed size and SHA-256; a completion
// observes the bytes the backend holds and makes the object readable. Deletion
// tombstones the row and removes the bytes.
//
// A URL is a bearer capability. A backend that can be reached directly mints a
// pre-signed address; otherwise this module serves GET, HEAD and PUT on
// /content, and an address carries an opaque token the grants table records. A
// grant is read under a cross-tenant context, because the request arrives with
// no tenant and the token names the one it was minted for, and the object is
// then read under that tenant's ordinary scoped statement. Either way the object
// is re-read on every use, so deleting it makes every address to it stop
// working; this module keeps no record of who an address was minted for and
// offers no revocation beyond expiry.
//
// The internal key never leaves the capability. It carries no tenant and no
// object identifier — it is a random shard and name — so a backend address that
// embeds it (an S3 pre-signed URL does) reveals nothing internal. It is not in
// any API response, not in any error text this module writes and not in any log
// line; changing its syntax is an internal change no caller can observe.
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

// ModuleName is this module's name in the registry. A host writes it into the
// command that assembles an object store, and the configuration namespace its
// input items hang from is the same string.
const ModuleName = "objectstore.metadata"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

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
// is what puts the metadata and lifecycle surface in a binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// The product must deliver the seam the descriptor declares.
var _ objectstore.Objects = (*product)(nil)

// Module is the descriptor a host registers.
//
// The database, the tenant roster and the byte seam are required and hard. A
// metadata surface with no table has nowhere to hold a row, one with no roster
// carries no filter to scope its statements by tenant, and one with no store
// has nowhere for bytes to land — the seam is the whole point of the module, so
// an assembly that installs no backend fails resolution rather than at the first
// upload. The byte seam is delivered exclusively by whichever backend the host
// names, so a command installs exactly one.
//
// The direct-URL capability is optional: a backend that delivers it mints
// pre-signed addresses, and this module then mounts no content route. Absent, as
// the local-directory backend is, this module serves the content route itself.
//
// The HTTP entry point, the logging capability and the settings store are
// optional. With no entry point, and with a backend that mints no direct
// address, no address can be minted at all: URL answers ErrURLUnavailable rather
// than handing out an address nobody serves. With no settings store the upload
// window, the object lifetime and the URL lifetime read as their declared
// defaults. The capability is delivered exclusively: one process has exactly one
// metadata surface, so a second provider fails the startup.
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

		// New builds the product and reads no table of its own: this stage runs
		// before the migrations are applied, so on a first start the table is not
		// there yet. The first read of the table is the sweep in Start, or the
		// first request when the sweep is turned off.
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newProduct(reg)
		},

		// Init wires the optional surfaces up and mounts the content route when
		// there is one to mount. It reads no table: the routes are registered,
		// not served, and the sweep has not started.
		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.wire(reg)
		},

		// Start begins the sweep. Its first pass reads the objects table, which is
		// the module's first read of it: the table exists from Migrate on, and
		// nothing before this stage touched it.
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

// productOf narrows an instance to this module's product. An instance of another
// shape is one the host supplied for this module, such as a stand-in built for a
// test, and the callbacks leave it alone.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// product is the module's instance: the seams it reads and delivers, the storage
// it reads and writes, and the optional surfaces it wires up in Init.
type product struct {
	handle  *gorm.DB
	stmts   statements
	bytes   objectstore.Store
	catalog tenant.Catalog
	// direct is the backend's direct-URL capability when one is assembled. When
	// it is, URL mints pre-signed addresses and no content route is mounted.
	direct objectstore.DirectURL

	// tuned reads the upload window, the object lifetime and the URL lifetime
	// from the settings store at each call; with no store assembled, all three
	// read as their declared defaults.
	tuned            *settingsReader
	sweepInterval    time.Duration
	contentEndpoints []string
	publicURL        string
	// contentMounted reports whether this process serves the content route. It
	// is set once, in Init, and read by URL.
	contentMounted bool

	logger *slog.Logger

	cancelSweep context.CancelFunc
	sweepDone   chan struct{}
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

// newProduct takes up what the descriptor requires and assembles the product. It
// reads no table: the first read is the sweep in Start.
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

// optional takes up a capability whose absence is a legal assembly: the zero
// value says it is absent, and any other failure to take it up ends the start,
// because a capability that is there and cannot be taken up is not an absence.
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

// wire runs in Init, where every instance exists. It takes up the optional
// logging capability and decides what to mount, then writes the start
// diagnostic.
//
// A backend that delivers DirectURL answers URL itself, so no content route is
// mounted and the endpoint list is not read. Otherwise the entry point is what
// can mount the content route: a run that assembles none mounts nothing and
// reads the list nowhere, and its URL calls answer ErrURLUnavailable rather than
// an address nobody serves. A run that does mount has to name the endpoints, and
// every name is looked up on the entry point before any route is registered; a
// name the entry point does not declare fails the startup, because a route on an
// endpoint nobody serves is unreachable.
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
