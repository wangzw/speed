// Package ops is the process's operations surface: it mounts the probe routes
// and the merged API document on the listening endpoint the configuration
// names.
//
// It delivers no capability. It becomes a module through its descriptor and its
// lifecycle callbacks and through the http.Spec resource it declares for
// itself, and it serves three routes on that endpoint: GET /health answers that
// the process is alive, GET /ready answers whether it can serve, and
// GET /openapi.json answers which API this binary provides.
//
// The routes sit on an endpoint the host assembles unguarded: a probe carries
// no credential and names no tenant, so nothing here consults an identity, a
// tenant or a permission and no handler decides one. The endpoint is
// ops.endpoint, defaulting to "ops".
//
// Merging the fragments every module declares into one document is the module's
// work, and the available set is what it merges — every declared fragment,
// whether or not its module runs in this command, because the document answers
// which API this binary provides rather than which it is currently serving. The
// merge itself is openapi.Merge, a pure function this module and a build tool
// share; a conflict between two fragments fails the startup here rather than
// surfacing as a 500 on the first probe.
package ops

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/ops/openapi"
)

// ModuleName is this module's name in the registry. A host writes it into a
// command to mount the operations surface, and the configuration namespace is
// the same word.
const ModuleName = "ops"

// ConfigNamespace is where this module's input items are mounted in the
// configuration data.
const ConfigNamespace = "ops"

// defaultEndpoint is the endpoint this module mounts its routes on when the
// configuration names none.
const defaultEndpoint = "ops"

// The paths of the three routes this module serves, on the configured endpoint.
const (
	healthPath   = "/health"
	readyPath    = "/ready"
	documentPath = "/openapi.json"
)

// The input items this module accepts, relative to its namespace.
const (
	endpointItem    = "endpoint"
	titleItem       = "title"
	versionItem     = "version"
	descriptionItem = "description"
	serversItem     = "servers"
)

// ErrForeignInstance reports a lifecycle callback handed an instance this
// module's New did not construct.
var ErrForeignInstance = errors.New("ops: the instance is not this module's")

//go:embed openapi/ops.json
var opsFragment []byte

// init registers this module with the process registry, so a host that imports
// this package can take it into a command by name. Importing it alone mounts
// nothing: a registered module only takes part in a run a command names it in.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
//
// The HTTP entry point is required: this module mounts its routes through the
// Router capability and reads the endpoint names from it, so a command that
// takes this module in also takes the entry point in. The logger and the
// database are optional: with a database the readiness probe checks it once
// beside the endpoints, and with neither the module still serves, its release
// records going unwritten.
//
// The module delivers no capability. The only thing it adds to the outward
// declaration is its own http.Spec resource, which carries the OpenAPI fragment
// describing its three routes.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*speedhttp.Router)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
			{Token: (*db.Database)(nil), Optional: true},
		},
		Resources: []any{
			schema(),
			speedhttp.Spec{Endpoint: defaultEndpoint, Document: opsFragment},
			operations,
		},

		New:   newProduct,
		Init:  initProduct,
		Stop:  stopProduct,
		Close: closeProduct,
	}
}

// moduleConfig is the set of input items this module accepts, under its
// namespace.
type moduleConfig struct {
	// Endpoint is the endpoint the routes are mounted on and the one written
	// into the module's own fragment. An undeclared name fails the startup.
	Endpoint string `config:"endpoint"`
	// Title is the merged document's info.title.
	Title string `config:"title"`
	// Version is the merged document's info.version.
	Version string `config:"version"`
	// Description is the merged document's info.description. Empty leaves the
	// member out.
	Description string `config:"description"`
	// Servers are the origins the API is reached at, as the merged document's
	// servers list. Empty leaves the member out.
	Servers []string `config:"servers"`
}

// schema declares this module's input items, mounted under its own namespace.
// The endpoint defaults to defaultEndpoint through the prototype.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &moduleConfig{Endpoint: defaultEndpoint}}},
		Items: map[string]config.Item{
			endpointItem:    {Origins: config.OriginPrimary},
			titleItem:       {Origins: config.OriginPrimary},
			versionItem:     {Origins: config.OriginPrimary},
			descriptionItem: {Origins: config.OriginPrimary},
			serversItem:     {Origins: config.OriginPrimary},
		},
	}
}

// newProduct reads this module's configuration and takes up the optional
// logger and database, building the product Init and the handlers work from.
func newProduct(_ context.Context, reg *core.Registry) (any, error) {
	cfg, err := readConfig(reg)
	if err != nil {
		return nil, err
	}
	p := &product{config: cfg}

	logger, err := log.NamedFor(reg, ModuleName)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
		// Logging is optional; with none the release records go unwritten.
	case err != nil:
		return nil, fmt.Errorf("%s: taking up the logging capability: %w", ModuleName, err)
	default:
		p.logger = logger
	}

	database, err := core.Resolve[db.Database](reg)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
		// No database: the readiness probe checks the endpoints alone.
	case err != nil:
		return nil, fmt.Errorf("%s: taking up the database capability: %w", ModuleName, err)
	default:
		p.database = database
	}
	return p, nil
}

// readConfig decodes this module's section into a fresh prototype copy, so the
// defaults reach the result.
func readConfig(reg *core.Registry) (moduleConfig, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return moduleConfig{}, fmt.Errorf("%s: this module's input items are read from "+
			"configuration, and no reader was handed to the registry before the assembly "+
			"began: %w", ModuleName, err)
	}
	cfg := moduleConfig{Endpoint: defaultEndpoint}
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return moduleConfig{}, fmt.Errorf("%s: reading %s.%s: %w", ModuleName, ConfigNamespace,
			endpointItem, err)
	}
	return cfg, nil
}

// initProduct resolves the router, merges the document and mounts the three
// routes.
//
// The merge happens here rather than per request: the fragments are
// declarations fixed for the life of the binary, so a conflict between two of
// them is a startup failure, and merging once leaves the request path reading
// an immutable value. The registration lands inside the http module's gate
// window, which stays open across the whole Init stage.
func initProduct(_ context.Context, reg *core.Registry, instance any) error {
	p, ok := instance.(*product)
	if !ok {
		return fmt.Errorf("%w: the %s module's Init was handed a %T, which its own New did "+
			"not construct", ErrForeignInstance, ModuleName, instance)
	}
	router, err := core.Resolve[speedhttp.Router](reg)
	if err != nil {
		return err
	}
	endpoint, err := router.Endpoint(p.config.Endpoint)
	if err != nil {
		return err
	}

	specs := core.Resources[speedhttp.Spec](reg)
	// The module's own fragment describes the routes it serves, and those are
	// on the configured endpoint. Its declared Spec carries the default,
	// because a resource is a static declaration fixed before configuration is
	// read; the endpoint it is actually served on is written here, so the
	// merged document tags its operations with the endpoint that answers them.
	for i := range specs {
		if specs[i].Module == ModuleName {
			specs[i].Value.Endpoint = p.config.Endpoint
		}
	}
	document, err := openapi.Merge(specs, openapi.Options{
		Title:       p.config.Title,
		Version:     p.config.Version,
		Description: p.config.Description,
		Servers:     p.config.Servers,
	})
	if err != nil {
		return err
	}

	p.router = router
	p.document = document
	endpoint.Route(http.MethodGet, healthPath, healthOperation.Handle(reportHealth))
	endpoint.Route(http.MethodGet, readyPath, readinessOperation.Handle(p.reportReady))
	endpoint.Route(http.MethodGet, documentPath, documentOperation.Handle(p.serveDocument))
	return nil
}

// stopProduct writes the module's release record. It holds nothing of its own
// to release, and it tolerates an instance it did not construct so a startup
// that never reached the module still stops cleanly.
func stopProduct(_ context.Context, _ *core.Registry, instance any) error {
	if p, ok := instance.(*product); ok && p.logger != nil {
		p.logger.Info("stop")
	}
	return nil
}

// closeProduct writes the module's release record, with the same tolerance of a
// foreign or absent instance as stopProduct.
func closeProduct(_ context.Context, _ *core.Registry, instance any) error {
	if p, ok := instance.(*product); ok && p.logger != nil {
		p.logger.Info("close")
	}
	return nil
}
