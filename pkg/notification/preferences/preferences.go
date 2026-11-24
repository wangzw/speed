// Package preferences is the per-type channel preference module: for each
// recipient and type it holds the channels that recipient's dispatches go out
// under, and it consumes the notification type taxonomy.
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

const defaultEndpoint = "api"

// tableName is the preference table. It is written here rather than derived from
// a Go type because the model names it and each dialect's migration names it in
// turn, and the three are kept in step by hand.
const tableName = "notification_preferences"

//go:embed migrations
var migrationRoot embed.FS

//go:embed openapi/preferences.json
var preferencesFragment []byte

var preferencesSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: preferencesFragment,
}

var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive above is what makes this unreachable: the directory
		// is part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

func init() { core.ProcessRegistry.Register(Module()) }

// The product must deliver the seam the descriptor declares.
var _ notification.Preferences = (*product)(nil)

// Module is the descriptor a host registers.
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
			feature.Spec{Name: "notification.preferences"},
		},

		New: func(ctx context.Context, reg *core.Registry) (any, error) {
			return newProduct(ctx, reg)
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.wire(reg)
		},
	}
}

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

// wire runs in Init, where every instance exists.
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

func (p *product) diagnose() {
	p.logger.Info(ModuleName+": the preference routes are mounted", "endpoints", p.cfg.endpoints)
}

type route struct {
	method  string
	path    string
	handler gin.HandlerFunc
}

var (
	pathTypes             = preferencesSpec.ResolvePath(pathTypesFragment)
	pathPreferences       = preferencesSpec.ResolvePath(pathPreferencesFragment)
	pathPreferenceChannel = preferencesSpec.ResolvePath(pathPreferenceChannelFragment)
)

func (p *product) routes() []route {
	return []route{
		{listTypesOperation.Method, pathTypes, listTypesOperation.Handle(p.listTypes)},
		{listPreferencesOperation.Method, pathPreferences, listPreferencesOperation.Handle(p.listPreferences)},
		{updatePreferenceOperation.Method, pathPreferenceChannel, updatePreferenceOperation.Handle(p.setPreference)},
	}
}
