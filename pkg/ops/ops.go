// Package ops is the process's operations surface: it mounts the probe routes
// and the merged API document on the endpoint the configuration names. It
// delivers no capability.
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

// ModuleName is this module's name in the registry; the configuration namespace
// is the same word.
const ModuleName = "ops"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = "ops"

const defaultEndpoint = "ops"

const (
	healthPath   = "/health"
	readyPath    = "/ready"
	documentPath = "/openapi.json"
)

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

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor. The HTTP entry point is required; the
// logger and database are optional, and it delivers no capability.
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

type moduleConfig struct {
	Endpoint    string   `config:"endpoint"`
	Title       string   `config:"title"`
	Version     string   `config:"version"`
	Description string   `config:"description"`
	Servers     []string `config:"servers"`
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &moduleConfig{Endpoint: defaultEndpoint}}},
		Items: map[string]config.Item{
			endpointItem:    {Origins: config.OriginPrimary},
			titleItem:       {Origins: config.OriginPrimary},
			versionItem:     {Origins: config.OriginPrimary},
			descriptionItem: {Origins: config.OriginPrimary},
			serversItem:     {Origins: config.OriginPrimary | config.OriginEnv},
		},
	}
}

func newProduct(_ context.Context, reg *core.Registry) (any, error) {
	cfg, err := readConfig(reg)
	if err != nil {
		return nil, err
	}
	p := &product{config: cfg}

	logger, err := log.NamedFor(reg, ModuleName)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
	case err != nil:
		return nil, fmt.Errorf("%s: taking up the logging capability: %w", ModuleName, err)
	default:
		p.logger = logger
	}

	database, err := core.Resolve[db.Database](reg)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
	case err != nil:
		return nil, fmt.Errorf("%s: taking up the database capability: %w", ModuleName, err)
	default:
		p.database = database
	}
	return p, nil
}

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

func stopProduct(_ context.Context, _ *core.Registry, instance any) error {
	if p, ok := instance.(*product); ok && p.logger != nil {
		p.logger.Info("stop")
	}
	return nil
}

func closeProduct(_ context.Context, _ *core.Registry, instance any) error {
	if p, ok := instance.(*product); ok && p.logger != nil {
		p.logger.Info("close")
	}
	return nil
}
