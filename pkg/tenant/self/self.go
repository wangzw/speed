// Package self mounts GET /api/v1/auth/me/tenants, which lists the tenants the calling
// account holds an active membership in.
package self

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry.
const ModuleName = "tenant.self"

// ConfigNamespace is the namespace of this module's configuration items.
const ConfigNamespace = ModuleName

const (
	endpointsItem = "endpoints"
	endpointsKey  = ConfigNamespace + "." + endpointsItem
)

const allEndpoints = "*"

const defaultEndpoint = "account"

//go:embed openapi/tenant.self.json
var openAPIFragment []byte

var tenantSelfSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: openAPIFragment,
}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It requires authn.Memberships and
// tenant.Catalog, takes tenant.KindResolver and log.Logger optionally, and is always
// StateEnabled.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*authn.Memberships)(nil)},
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*tenant.KindResolver)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			schema(),
			tenantSelfSpec,
			operations,
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
	memberships authn.Memberships
	catalog     tenant.Catalog
	kind        tenant.KindResolver

	gate      authz.Gate
	admission authn.Gate
	logger    *slog.Logger

	cfg resolved
}

func newProduct(reg *core.Registry) (*product, error) {
	memberships, err := core.Resolve[authn.Memberships](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the membership read: %w", ModuleName, err)
	}
	catalog, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant roster: %w", ModuleName, err)
	}
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	cfg, err := readConfig(reader)
	if err != nil {
		return nil, err
	}
	kind, _, err := optional[tenant.KindResolver](reg, "the tenant kind resolver")
	if err != nil {
		return nil, err
	}
	return &product{
		memberships: memberships,
		catalog:     catalog,
		kind:        kind,
		logger:      log.Default(),
		cfg:         cfg,
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
	if admission, found, err := optional[authn.Gate](reg, "the gateway admission surface"); err != nil {
		return err
	} else if found {
		p.admission = admission
	}

	router, found, err := optional[speedhttp.Router](reg, "the HTTP entry point")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%s: %w", ModuleName, tenant.ErrNoRouter)
	}
	if err := p.mount(router); err != nil {
		return err
	}
	p.declareOpen()
	p.diagnose()
	return nil
}

func (p *product) diagnose() {
	p.logger.Info(ModuleName+": the caller's tenant read is mounted", "endpoints", p.cfg.endpoints)
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			endpointsItem: {
				Origins:  config.OriginPrimary,
				Required: true,
				Description: "the listening endpoints the caller's tenant read is mounted on, by the " +
					"names the entry point's own configuration gives them; concrete names only, never *",
			},
		},
	}
}

var configDefaults = moduleConfig{}

type moduleConfig struct {
	// Endpoints lists the endpoints the route is mounted on; it must be non-empty, hold
	// concrete names only (not "*"), and have no repeats.
	Endpoints []string `config:"endpoints"`
}

type resolved struct {
	endpoints []string
}

func (c moduleConfig) resolve() (resolved, error) {
	if len(c.Endpoints) == 0 {
		return resolved{}, fmt.Errorf("%w: %s is empty. The caller's tenant read is mounted on the "+
			"endpoints this module is told to and on none by default; list the endpoints that should "+
			"carry it, or leave this module out of the command", ErrInvalidConfig, endpointsKey)
	}
	seen := make(map[string]struct{}, len(c.Endpoints))
	for i, name := range c.Endpoints {
		switch name {
		case "":
			return resolved{}, fmt.Errorf("%w: %s holds an empty name at position %d",
				ErrInvalidConfig, endpointsKey, i)
		case allEndpoints:
			return resolved{}, fmt.Errorf("%w: %s holds %q at position %d. This module takes concrete "+
				"endpoint names only: the caller's tenant read belongs on an endpoint a gateway guards",
				ErrInvalidConfig, endpointsKey, allEndpoints, i)
		}
		if _, twice := seen[name]; twice {
			return resolved{}, fmt.Errorf("%w: %s names the endpoint %q twice, which would register "+
				"the caller's tenant read on it twice", ErrInvalidConfig, endpointsKey, name)
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
