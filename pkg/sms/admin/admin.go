// Package admin is the SMS capability's management module: it mounts the
// template-management and carrier-declaration routes under /api/v1/system/sms/*
// and delivers no capability of its own.
package admin

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/sms"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command that mounts the management routes.
const ModuleName = "sms.admin"

// ConfigNamespace is where this module's input items are mounted. It is this
// module's own name, as a registering module's namespace always is.
const ConfigNamespace = ModuleName

const (
	endpointsItem = "endpoints"
	endpointsKey  = ConfigNamespace + "." + endpointsItem
)

// allEndpoints is the name that stands for every endpoint; this module refuses
// it, because the management routes would land where no gateway guards them.
const allEndpoints = "*"

// defaultEndpoint is the endpoint the OpenAPI fragment is declared against; a configured name no endpoint carries is not an error.
const defaultEndpoint = "admin"

//go:embed openapi/sms.admin.json
var openAPIFragment []byte

var adminSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Scope:    speedhttp.ScopeSystem,
	Document: openAPIFragment,
}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It requires the template registry,
// carrier provider, guard and audit capabilities, takes the logging capability
// optionally, and stands itself down when sms.admin.endpoints is absent.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*sms.Registry)(nil)},
			{Token: (*sms.Provider)(nil)},
			{Token: (*guard.Guard)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			schema(), permissionSpec(), translationSpec(), messageCatalog,
			adminSpec,
			operations,
			feature.Spec{Name: "sms.admin"},
		},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			off, reason, err := sms.OffBySelection(reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("%s: reading the SMS carrier selection: %w", ModuleName, err)
			}
			if off {
				return core.Enablement{State: core.StateDisabled, Reason: reason}, nil
			}
			reader, err := core.Resolve[config.Reader](reg)
			if err != nil {
				return core.Enablement{}, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
			}
			given, err := endpointsGiven(reader)
			if err != nil {
				return core.Enablement{}, err
			}
			if !given {
				return core.Enablement{
					State: core.StateDisabled,
					Reason: "sms.admin.endpoints is not configured, so the management routes are mounted " +
						"on no endpoint and this run offers no SMS management surface",
				}, nil
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
	registry sms.Registry
	provider sms.Provider
	guard    guard.Guard
	audit    audit.Audit

	gate   authz.Gate
	logger *slog.Logger

	cfg resolved
}

func newProduct(reg *core.Registry) (*product, error) {
	registry, err := core.Resolve[sms.Registry](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the template registry: %w", ModuleName, err)
	}
	provider, err := core.Resolve[sms.Provider](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the carrier provider: %w", ModuleName, err)
	}
	guardAPI, err := core.Resolve[guard.Guard](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the handler-internal guard: %w", ModuleName, err)
	}
	auditor, err := core.Resolve[audit.Audit](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the audit capability: %w", ModuleName, err)
	}
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	cfg, err := readConfig(reader)
	if err != nil {
		return nil, err
	}
	return &product{
		registry: registry,
		provider: provider,
		guard:    guardAPI,
		audit:    auditor,
		logger:   log.Default(),
		cfg:      cfg,
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
	if gate, found, err := optional[authz.Gate](reg, "the route-declaration surface"); err != nil {
		return err
	} else if found {
		p.gate = gate
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
	p.logger.Info(ModuleName+": the management routes are mounted",
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
				Description: "the listening endpoints the management routes are mounted on, by the " +
					"names the entry point's own configuration gives them; concrete names only, never *",
			},
		},
	}
}

var configDefaults = moduleConfig{}

type moduleConfig struct {
	// Endpoints names the listening endpoints the management routes are mounted on by
	// the entry point's own names; a concrete name is required.
	Endpoints []string `config:"endpoints"`
}

type resolved struct {
	endpoints []string
}

func (c moduleConfig) resolve() (resolved, error) {
	if len(c.Endpoints) == 0 {
		return resolved{}, fmt.Errorf("%w: %s is empty. The management routes are mounted on the "+
			"endpoints this module is told to and on none by default; list the endpoints that should "+
			"carry them, or leave this module out of the command", sms.ErrInvalidConfig, endpointsKey)
	}
	seen := make(map[string]struct{}, len(c.Endpoints))
	for i, name := range c.Endpoints {
		switch name {
		case "":
			return resolved{}, fmt.Errorf("%w: %s holds an empty name at position %d",
				sms.ErrInvalidConfig, endpointsKey, i)
		case allEndpoints:
			return resolved{}, fmt.Errorf("%w: %s holds %q at position %d. This module takes concrete "+
				"endpoint names only: mounting the management routes on every endpoint would put them "+
				"on the public ones, where no gateway guards them",
				sms.ErrInvalidConfig, endpointsKey, allEndpoints, i)
		}
		if _, twice := seen[name]; twice {
			return resolved{}, fmt.Errorf("%w: %s names the endpoint %q twice, which would register "+
				"every management route on it twice", sms.ErrInvalidConfig, endpointsKey, name)
		}
		seen[name] = struct{}{}
	}
	return resolved{endpoints: c.Endpoints}, nil
}

type endpointsOnly struct {
	Endpoints []string `config:"endpoints"`
}

func endpointsGiven(reader config.Reader) (bool, error) {
	err := reader.Decode(ConfigNamespace, &endpointsOnly{})
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, config.ErrMissingRequired):
		return false, nil
	default:
		return false, err
	}
}

func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
