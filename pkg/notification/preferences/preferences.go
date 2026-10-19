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
// The routes are mounted under /api/v1/notifications on the listening endpoints
// the module is told to (notification.preferences.endpoints), and on no others:
// a frontend bundle covers the whole path space of the endpoint it is given
// with a catch-all route, so an endpoint that serves a frontend cannot also
// carry these. The recipient a route acts for is not resolved here — this module
// holds no identity — but is read from the request context, where the host's
// identity and authorization side put it with WithRecipient.
package preferences

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"gorm.io/gorm"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry. It is also the configuration
// namespace its input items hang from. A host writes it into the command that
// assembles the notification capability.
const ModuleName = "notification.preferences"

// ConfigNamespace is where this module's input items are mounted, which is the
// module name.
const ConfigNamespace = ModuleName

// defaultEndpoint is the listening endpoint this module declares its OpenAPI
// fragment for. The endpoint's name belongs to the host's HTTP configuration, and
// a resource is a static declaration written before any configuration is read, so
// the fragment carries this conventional name rather than an endpoint a run
// happens to configure; the routes a run actually mounts follow the endpoints the
// module is told to (notification.preferences.endpoints).
const defaultEndpoint = "api"

// tableName is the preference table. It is written here rather than derived from
// a Go type because the model names it and each dialect's migration names it in
// turn, and the three are kept in step by hand.
const tableName = "notification_preferences"

//go:embed migrations
var migrationRoot embed.FS

//go:embed openapi/preferences.json
var preferencesFragment []byte

// preferencesSpec is the API root this module's fragment and routes hang off.
var preferencesSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: preferencesFragment,
}

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
// mounted and the endpoint list is not read, and a host without logging still
// gets a working matrix whose failures reach the process default logger.
//
// The capability is delivered exclusively: one process has exactly one
// preference matrix, so a second provider fails the startup.
//
// The descriptor also declares this module's own http.Spec, carrying the OpenAPI
// fragment that describes the preference surface under /api/v1/notifications; the
// operations module merges it into the document a process serves.
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
			schema(),
			db.Migrations{FS: migrationsFS},
			messageCatalog,
			preferencesSpec,
			// The preference surface: its routes are mounted whenever the
			// module is in force and the process has an entry point, so the
			// feature is static.
			feature.Spec{Name: "notification.preferences"},
		},

		// New builds the product, takes up the database and the configuration
		// reader, reads the endpoint list, and collects and validates the taxonomy.
		// It reads no table of its own: this stage runs before the migrations are
		// applied, so on a first start the preference table is not there yet, and
		// the taxonomy comes from the module descriptors rather than from any
		// table.
		New: func(ctx context.Context, reg *core.Registry) (any, error) {
			return newProduct(ctx, reg)
		},

		// Init mounts the routes, when an entry point is present, on the endpoints
		// the module was told to. The entries are registered, not served: no
		// request reaches them until the entry point serves.
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
// it collected once in New, the endpoint list its routes are mounted by, and the
// optional surfaces it wires up in Init.
type product struct {
	handle   *gorm.DB
	taxonomy taxonomy
	cfg      resolved

	logger    *slog.Logger
	localizer i18n.Localizer
}

// readerOf takes up the configuration reader. A reader that is not there means
// the host never supplied one as the instance of the config module, which is the
// host's omission rather than something to read as "nothing was configured".
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	return reader, nil
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
	reader, err := readerOf(reg)
	if err != nil {
		return nil, err
	}
	cfg, err := readConfig(reader)
	if err != nil {
		return nil, err
	}
	tax, err := buildTaxonomy(core.Resources[notification.TypeSpec](reg))
	if err != nil {
		return nil, err
	}
	return &product{
		handle:   database.DB(),
		taxonomy: tax,
		cfg:      cfg,
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
// routes on the endpoints the module is told to.
//
// The entry point is optional: a command that resolves the preference matrix and
// has no gateway still delivers it, and its routes are simply not mounted. When
// the entry point is present the endpoint list is required — a list that mounted
// the routes nowhere would silently drop them — and every name in it is looked up
// before any route is registered, so a name the entry point does not declare
// fails the startup naming that endpoint.
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
		// The endpoint list is not read when the route capability is absent.
		return nil
	}
	if len(p.cfg.endpoints) == 0 {
		return fmt.Errorf("%s: %w: %s is empty and the HTTP entry point is present, so the routes would "+
			"be mounted on nothing; list the endpoints that should carry them, or leave the entry point "+
			"out of the command", ModuleName, notification.ErrInvalidConfig, endpointsKey)
	}
	if err := validateEndpoints(p.cfg.endpoints); err != nil {
		return fmt.Errorf("%s: %w", ModuleName, err)
	}
	if err := p.mount(router); err != nil {
		return err
	}
	p.diagnose()
	return nil
}

// mount registers the routes on the endpoints the module is told to. Every name
// is looked up before any route is registered, so a name the entry point does not
// declare fails the startup naming that endpoint rather than leaving a partial
// mounting behind.
func (p *product) mount(router speedhttp.Router) error {
	type target struct {
		name     string
		endpoint speedhttp.Endpoint
	}
	targets := make([]target, 0, len(p.cfg.endpoints))
	for _, name := range p.cfg.endpoints {
		endpoint, err := router.Endpoint(name)
		if err != nil {
			return fmt.Errorf("%s: an undeclared endpoint in %s: %q: %w", ModuleName, endpointsKey, name, err)
		}
		targets = append(targets, target{name: name, endpoint: endpoint})
	}
	for _, tgt := range targets {
		for _, r := range p.routes() {
			tgt.endpoint.Route(r.method, r.path, r.handler)
		}
	}
	return nil
}

// diagnose writes the start diagnostic: the endpoints the preference routes were
// mounted on. The line is the module's own, so a reader finds what the module
// mounted without scanning a chain listing.
func (p *product) diagnose() {
	p.logger.Info(ModuleName+": the preference routes are mounted", "endpoints", p.cfg.endpoints)
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

// The absolute paths the routes are mounted on: the paths the fragment declares,
// resolved against the API root the module's Spec declares. They are the
// module's whole route surface.
var (
	pathTypes             = preferencesSpec.ResolvePath(pathTypesFragment)
	pathPreferences       = preferencesSpec.ResolvePath(pathPreferencesFragment)
	pathPreferenceChannel = preferencesSpec.ResolvePath(pathPreferenceChannelFragment)
)

// routes is the closed set of routes this module mounts. Each route is bound
// from the operation declared for it, so the handler receives the declared input
// and writes the declared answer.
func (p *product) routes() []route {
	return []route{
		{listTypesOperation.Method, pathTypes, listTypesOperation.Handle(p.listTypes)},
		{listPreferencesOperation.Method, pathPreferences, listPreferencesOperation.Handle(p.listPreferences)},
		{updatePreferenceOperation.Method, pathPreferenceChannel, updatePreferenceOperation.Handle(p.setPreference)},
	}
}
