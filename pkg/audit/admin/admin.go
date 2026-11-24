// Package admin is the audit capability's management module: it mounts
// the record-query and export routes under /api/v1/system/audit/* and
// delivers no capability of its own.
package admin

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command that mounts the query routes.
const ModuleName = "audit.admin"

// ConfigNamespace is where this module's input items are mounted. It is this
// module's own name, as a registering module's namespace always is.
const ConfigNamespace = ModuleName

const (
	endpointsItem = "endpoints"
	endpointsKey  = ConfigNamespace + "." + endpointsItem
)

// allEndpoints is the name that stands for every endpoint in an endpoint list.
// This module's list refuses it: mounting the query routes on every endpoint,
// the public ones included, would put them where no gateway guards them.
const allEndpoints = "*"

const defaultEndpoint = "admin"

// openAPIFragment is this module's OpenAPI fragment, read at build time. It is
// generated from operations, not written by hand.
//
//go:embed openapi/audit.admin.json
var openAPIFragment []byte

var auditSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Scope:    speedhttp.ScopeSystem,
	Document: openAPIFragment,
}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*journal.Reader)(nil)},
			{Token: (*guard.Guard)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			schema(),
			auditSpec,
			feature.Spec{Name: "audit.admin"},
		},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
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

		Start: func(context.Context, *core.Registry, any) error { return nil },
		Stop:  func(context.Context, *core.Registry, any) error { return nil },
		Close: func(context.Context, *core.Registry, any) error { return nil },
	}
}

func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

type product struct {
	reader journal.Reader
	guard  guard.Guard

	// gate is the route-declaration surface, nil when the assembly delivers
	// none.
	gate   authz.Gate
	names  tenant.NameResolver
	logger *slog.Logger

	cfg resolved
}

func newProduct(reg *core.Registry) (*product, error) {
	reader, err := core.Resolve[journal.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the audit read surface: %w", ModuleName, err)
	}
	guardAPI, err := core.Resolve[guard.Guard](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the handler-internal guard: %w", ModuleName, err)
	}
	configReader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	cfg, err := readConfig(configReader)
	if err != nil {
		return nil, err
	}
	return &product{
		reader: reader,
		guard:  guardAPI,
		logger: log.Default(),
		cfg:    cfg,
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
	if gate, found, err := optional[authz.Gate](reg, "the route-declaration surface"); err != nil {
		return err
	} else if found {
		p.gate = gate
	}
	if names, found, err := optional[tenant.NameResolver](reg, "the tenant name resolver"); err != nil {
		return err
	} else if found {
		p.names = names
	}

	router, found, err := optional[speedhttp.Router](reg, "the HTTP entry point")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%s: %w", ModuleName, authz.ErrNoRouter)
	}
	if err := p.mount(router); err != nil {
		return err
	}
	p.declareOpen()
	p.diagnose()
	return nil
}

func (p *product) diagnose() {
	p.logger.Info(ModuleName+": the query routes are mounted",
		"endpoints", p.cfg.endpoints,
		"groups", managementGroups)
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			endpointsItem: {
				Origins:  config.OriginPrimary,
				Required: true,
				Description: "the listening endpoints the query routes are mounted on, by the names the " +
					"entry point's own configuration gives them; concrete names only, never *",
			},
		},
	}
}

var configDefaults = moduleConfig{}

type moduleConfig struct {
	// Endpoints names the listening endpoints the query routes are mounted on,
	// by the entry point's own names. The list is a set of concrete names: the
	// name that stands for every endpoint is not accepted.
	Endpoints []string `config:"endpoints"`
}

type resolved struct {
	endpoints []string
}

func (c moduleConfig) resolve() (resolved, error) {
	if len(c.Endpoints) == 0 {
		return resolved{}, fmt.Errorf("%w: %s is empty. The query routes are mounted on the endpoints "+
			"this module is told to and on none by default; list the endpoints that should carry them, "+
			"or leave this module out of the command", authn.ErrInvalidConfig, endpointsKey)
	}
	seen := make(map[string]struct{}, len(c.Endpoints))
	for i, name := range c.Endpoints {
		switch name {
		case "":
			return resolved{}, fmt.Errorf("%w: %s holds an empty name at position %d",
				authn.ErrInvalidConfig, endpointsKey, i)
		case allEndpoints:
			return resolved{}, fmt.Errorf("%w: %s holds %q at position %d. This module takes concrete "+
				"endpoint names only: mounting the query routes on every endpoint would put them on the "+
				"public ones, where no gateway guards them",
				authn.ErrInvalidConfig, endpointsKey, allEndpoints, i)
		}
		// The list is a set. The entry point holds one endpoint per name, so a
		// name written twice would register every query route on that endpoint
		// twice, which the route engine refuses as a conflict.
		if _, twice := seen[name]; twice {
			return resolved{}, fmt.Errorf("%w: %s names the endpoint %q twice, which would register every "+
				"query route on it twice", authn.ErrInvalidConfig, endpointsKey, name)
		}
		seen[name] = struct{}{}
	}
	return resolved{endpoints: c.Endpoints}, nil
}

func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
