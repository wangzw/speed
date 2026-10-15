// Package preferences is the per-type channel preference module: it holds, for
// each recipient and each notification type, the channels that recipient's
// dispatches of that type go out under, and it is one of the two consumption
// points of the notification type taxonomy.
//
// The types are not declared here. A type is the declaring business module's:
// a business module places a notification.TypeSpec in its own Resources, and
// this module collects every declaration in New through
// core.Resources[notification.TypeSpec](reg). The collected declarations are
// the taxonomy: the module validates each one, refuses a startup over one that
// is not self-consistent or over two modules claiming one key, and then holds
// the set read-only for the life of the process. A preference write names a
// type and is refused unless it is in the taxonomy.
//
// The source domain is the available set, not a participation set: the resource
// query walks every registered module, so a type a module declares is in the
// taxonomy whether or not that module is taken in by the run. A type is a piece
// of vocabulary a preference write is checked against, and a word does not leave
// the language because one command did not load it.
//
// This module delivers notification.Preferences exclusively. It answers which
// channels a recipient's dispatches of a type would go out under — the channel
// set the recipient stored for the type, or the type's default channels when the
// recipient stored none — and it replaces the channel set a recipient chose for
// a type. It does not deliver, render or write an inbox, and it decides no
// authorization: who may read or change a recipient's preferences is the host's,
// so its routes carry no permission gate of their own.
//
// The routes are mounted under /api/v1/notifications. The recipient a route acts
// for is not resolved here — this module holds no identity — but is read from
// the request context, where the host's identity and authorization side put it
// with WithRecipient.
package preferences

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"

	"gorm.io/gorm"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command that assembles the notification capability.
const ModuleName = "notification.preferences"

// tableName is the preference table. It is written here rather than derived from
// a Go type because the model names it and each dialect's migration names it in
// turn, and the three are kept in step by hand.
const tableName = "notification_preferences"

//go:embed migrations
var migrationRoot embed.FS

// migrationsFS is migrationRoot with the wrapping directory removed, so the tree
// the database module sees has one directory per dialect at its root, which is
// the shape it reads.
var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive above is what makes this unreachable: the directory
		// is part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

// init registers this module with the process registry. Importing the package is
// what puts the preference matrix in a binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// The product must deliver the seam the descriptor declares.
var _ notification.Preferences = (*product)(nil)

// Module is the descriptor a host registers.
//
// The database and the tenant roster are required and hard: a preference matrix
// with no table has nowhere to hold a row, and one with no roster carries no
// filter to scope its statements by tenant. The roster is required for the order
// it imposes, not for a value this module reads: the roster module installs the
// tenant filter on the handle every scoped statement runs through, and declaring
// the dependency is what runs that module first. The HTTP entry point and the
// logging capability are optional: with no entry point the routes are simply not
// mounted, and a host without logging still gets a working matrix whose failures
// reach the process default logger.
//
// The capability is delivered exclusively: one process has exactly one
// preference matrix, so a second provider fails the startup.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*speedhttp.Router)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{{Token: (*notification.Preferences)(nil), Exclusive: true}},
		Resources: []any{
			db.Migrations{FS: migrationsFS},
			messageCatalog,
		},

		// New builds the product, takes up the database and collects and
		// validates the taxonomy. It reads no table of its own: this stage runs
		// before the migrations are applied, so on a first start the preference
		// table is not there yet, and the taxonomy comes from the module
		// descriptors rather than from any table.
		New: func(ctx context.Context, reg *core.Registry) (any, error) {
			return newProduct(ctx, reg)
		},

		// Init mounts the routes, when an entry point is present. The entries are
		// registered, not served: no request reaches them until the entry point
		// serves.
		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.wire(reg)
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

// product is the module's instance: the table it reads and writes, the taxonomy
// it collected once in New, and the optional surfaces it wires up in Init.
type product struct {
	handle   *gorm.DB
	taxonomy taxonomy

	logger    *slog.Logger
	localizer i18n.Localizer
}

// newProduct takes up what the descriptor requires and collects the taxonomy.
//
// The taxonomy is collected here and validated here: a declaration that is not
// self-consistent, or two modules that claim one key, is a startup failure. The
// collection reads the module descriptors, not a table, so it is available
// before the migrations have run.
func newProduct(_ context.Context, reg *core.Registry) (*product, error) {
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the database: %w", ModuleName, err)
	}
	tax, err := buildTaxonomy(core.Resources[notification.TypeSpec](reg))
	if err != nil {
		return nil, err
	}
	return &product{
		handle:   database.DB(),
		taxonomy: tax,
		logger:   log.Default(),
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
// logging capability, the message localizer and the entry point, and mounts the
// routes on every endpoint the entry point declares.
//
// The entry point is optional: a command that resolves the preference matrix and
// has no gateway still delivers it, and its routes are simply not mounted. Every
// endpoint is mounted because this module declares no endpoint list: the
// notification routes are a product API, and they are served wherever the entry
// point listens.
func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
	}
	if localizer, found, err := optional[i18n.Localizer](reg, "the message localizer"); err != nil {
		return err
	} else if found {
		p.localizer = localizer
	}
	router, found, err := optional[speedhttp.Router](reg, "the HTTP entry point")
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if err := p.mount(router); err != nil {
		return err
	}
	p.diagnose(router)
	return nil
}

// mount registers the routes on every endpoint the entry point declares. A name
// the endpoint lookup refuses fails the startup, though the name came from the
// entry point's own listing: an endpoint it declares but cannot hand out is a
// defect in that entry point rather than a route left silently unmounted.
func (p *product) mount(router speedhttp.Router) error {
	for _, name := range router.Endpoints() {
		endpoint, err := router.Endpoint(name)
		if err != nil {
			return fmt.Errorf("%s: the endpoint %q is declared but not accessible: %w", ModuleName, name, err)
		}
		for _, r := range p.routes() {
			endpoint.Route(r.method, r.path, r.handler)
		}
	}
	return nil
}

// diagnose writes the start diagnostic: the endpoints the preference routes were
// mounted on. The line is the module's own, so a reader finds what the module
// mounted without scanning a chain listing.
func (p *product) diagnose(router speedhttp.Router) {
	p.logger.Info(ModuleName+": the preference routes are mounted", "endpoints", router.Endpoints())
}

// route is one route: the method and path it answers and the handler that serves
// it. No route carries a permission: this module declares none, so the routes
// authenticate as usual and decide nothing about who may read or change a
// recipient's preferences.
type route struct {
	method  string
	path    string
	handler gin.HandlerFunc
}

// The routes, under the fixed base path, in the order they are mounted. The set
// is closed.
const (
	basePath              = "/api/v1/notifications"
	pathTypes             = basePath + "/types"
	pathPreferences       = basePath + "/preferences"
	pathPreferenceChannel = basePath + "/preferences/:typeKey/:channel"
)

// routes is the closed set of routes this module mounts.
func (p *product) routes() []route {
	return []route{
		{http.MethodGet, pathTypes, p.handleTypes},
		{http.MethodGet, pathPreferences, p.handlePreferences},
		{http.MethodPut, pathPreferenceChannel, p.handleSetPreference},
	}
}
