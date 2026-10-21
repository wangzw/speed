// Package metadata is the object store's metadata and lifecycle module: it
// holds the objects table, carries an object through declaration, upload and
// completion, serves reads, lists and deletes, sweeps expired rows, and mounts
// the HTTP surface under /api/v1/objects.
//
// It is the module that delivers the objectstore.Objects seam, exclusively. The
// bytes do not live here: they move through the objectstore.Store seam, which a
// backend module (objectstore.fs or objectstore.s3) delivers exclusively in
// turn. This module holds the metadata row, generates the internal key the bytes
// are addressed by, and reaches the backend only through that seam. It imports
// no object-storage SDK, so a host compiles no backend but the one it deploys
// on.
//
// An object is written in three steps — declaration, upload, completion — and
// each is a call on Objects. A declaration validates what the caller states,
// lands a pending row and opens an upload window; an upload stores the bytes
// under the internal key and records the observed size and SHA-256; a completion
// makes the object readable. Deletion tombstones the row and removes the bytes.
//
// The tenant comes from the context on every call. The objects table embeds the
// tenant convention, so the roster module's filter scopes, fills and checks
// every statement this module issues through the model path. The sweep is the
// one pass that crosses tenants: it is the database's own housekeeping, not a
// tenant's request, and it runs under a cross-tenant context.
//
// The internal key never leaves the capability. It is not in any API response,
// not in any error text this module writes and not in any log line; changing its
// syntax is an internal change no caller can observe.
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

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
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

// defaultEndpoint is the listening endpoint this module declares its OpenAPI
// fragment for. The endpoint's name belongs to the host's HTTP configuration, and
// a resource is a static declaration written before any configuration is read, so
// the fragment carries this conventional name rather than an endpoint a run
// happens to configure; the routes a run actually mounts follow the endpoints the
// module is told to (objectstore.metadata.endpoints).
const defaultEndpoint = "api"

//go:embed migrations
var migrationRoot embed.FS

//go:embed openapi/metadata.json
var metadataFragment []byte

// metadataSpec is the API root this module's fragment and routes hang off. The
// fragment is generated from operations; the routes are mounted from the same
// declarations and resolved against this root.
var metadataSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: metadataFragment,
}

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
// The HTTP entry point, the logging capability, the message localizer and the
// settings store are optional. With no entry point the routes are not mounted:
// the module still delivers Objects, which a command with no gateway reaches
// through Resolve, and the endpoint list, which says where the routes go, is not
// read. With no settings store the upload window and the object lifetime read as
// their declared defaults. The capability is delivered exclusively: one process
// has exactly one metadata surface, so a second provider fails the startup.
//
// The descriptor also declares this module's own http.Spec, carrying the OpenAPI
// fragment that describes the /api/v1/objects surface; the operations module
// merges it into the document a process serves.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*objectstore.Store)(nil)},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*speedhttp.Router)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{{Token: (*objectstore.Objects)(nil), Exclusive: true}},
		Resources: []any{
			schema(),
			settingsSchema(),
			db.Migrations{FS: migrationsFS},
			permissionSpec(),
			translationSpec(),
			messageCatalog,
			metadataSpec,
			operations,
			// The object surface: its routes are mounted whenever the module is
			// in force and the process has an entry point, so the feature is
			// static. Which byte backend is assembled does not change it.
			feature.Spec{Name: "objectstore.objects"},
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

		// Init wires the optional surfaces up and mounts the routes. It reads no
		// table: the routes are registered, not served, and the sweep has not
		// started.
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

// product is the module's instance: the seam it delivers, the storage it reads
// and writes, and the optional surfaces it wires up in Init.
type product struct {
	handle  *gorm.DB
	dialect db.Dialect
	stmts   statements
	bytes   objectstore.Store

	// tuned reads the upload window and the object lifetime from the settings
	// store at each call; with no store assembled, both read as their declared
	// defaults.
	tuned         *settingsReader
	sweepInterval time.Duration
	endpoints     []string

	logger    *slog.Logger
	localizer i18n.Localizer
	gate      authz.Gate

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
	stmts, err := statementsFor(database.Dialect())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ModuleName, err)
	}
	store, _, err := optional[settings.Store](reg, "the settings store")
	if err != nil {
		return nil, err
	}
	return &product{
		handle:        database.DB(),
		dialect:       database.Dialect(),
		stmts:         stmts,
		bytes:         bytes,
		tuned:         newSettingsReader(store),
		sweepInterval: cfg.sweepInterval,
		endpoints:     cfg.endpoints,
		logger:        log.Default(),
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
// logging capability, the message localizer and the route-declaration surface,
// mounts the routes on the entry point when one is present, declares every
// route's permission, and writes the start diagnostic.
//
// The entry point is optional: a command that resolves the object store and has
// no gateway still gets the metadata surface through Resolve, and its routes
// simply are not mounted. The endpoint list is judged here and only here: a run
// that assembles an entry point has to name the endpoints its routes are mounted
// on, while a run that assembles none mounts nothing and reads the list nowhere.
// Every endpoint in the list is looked up on the entry point before any route is
// mounted; a name the entry point does not declare fails the startup, because a
// route on an endpoint nobody serves is unreachable.
func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
	}
	if localizer, found, err := optional[i18n.Localizer](reg, "the message localizer"); err != nil {
		return err
	} else if found {
		p.localizer = localizer
	}
	if gate, found, err := optional[authz.Gate](reg, "the route-declaration surface"); err != nil {
		return err
	} else if found {
		p.gate = gate
	}

	router, found, err := optional[speedhttp.Router](reg, "the HTTP entry point")
	if err != nil {
		return err
	}
	if found {
		if err := validateEndpoints(p.endpoints); err != nil {
			return err
		}
		if err := p.mount(router); err != nil {
			return err
		}
		p.declare()
		p.diagnose()
	}
	return nil
}

// diagnose writes the start diagnostic: the endpoints the routes were mounted on.
// The line is the module's own, so a reader finds what the object store mounted
// without scanning a chain listing.
func (p *product) diagnose() {
	p.logger.Info(ModuleName+": the object store routes are mounted", "endpoints", p.endpoints)
}
