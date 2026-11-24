// Package profile is the tenant capability's organization-profile module: it mounts GET and PATCH
// /api/v1/tenants/{tenant}/profile and delivers no capability of its own. A write requires a proved
// email address and lands a mandatory audit row before the roster write.
package profile

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/audit"
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

// ModuleName is this module's name in the registry.
const ModuleName = "tenant.profile"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

const (
	endpointsItem = "endpoints"
	endpointsKey  = ConfigNamespace + "." + endpointsItem
)

const allEndpoints = "*"

// ErrInvalidConfig reports a configuration this module cannot run on; the
// message names the key.
var ErrInvalidConfig = errors.New("tenant.profile: a configuration this module cannot run on")

const defaultEndpoint = "api"

// openAPIFragment is this module's OpenAPI fragment, generated from operations.
//
//go:embed openapi/tenant.profile.json
var openAPIFragment []byte

var tenantProfileSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Scope:    speedhttp.ScopeTenants,
	Document: openAPIFragment,
}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. The roster, handler-internal guard, audit capability
// and signup-verification read are required; the logger and account read surface are optional. The
// entry point, tenant administration guard and route-declaration surface are taken up in Init.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*guard.Guard)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*authn.SignupVerification)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			schema(),
			tenantProfileSpec,
			operations,
			feature.Spec{Name: "tenant.profile"},
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
	catalog      tenant.Catalog
	guard        guard.Guard
	audit        audit.Audit
	verification authn.SignupVerification

	accounts authn.Accounts

	gate       authz.Gate
	routeGuard speedhttp.TenantAdminRouteGuard
	kind       tenant.KindResolver
	logger     *slog.Logger

	cfg resolved
}

func newProduct(reg *core.Registry) (*product, error) {
	catalog, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant roster: %w", ModuleName, err)
	}
	guardAPI, err := core.Resolve[guard.Guard](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the handler-internal guard: %w", ModuleName, err)
	}
	auditor, err := core.Resolve[audit.Audit](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the audit capability: %w", ModuleName, err)
	}
	verification, err := core.Resolve[authn.SignupVerification](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the signup-verification read: %w", ModuleName, err)
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
		catalog:      catalog,
		guard:        guardAPI,
		audit:        auditor,
		verification: verification,
		logger:       log.Default(),
		cfg:          cfg,
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
	if accounts, found, err := optional[authn.Accounts](reg, "the account read surface"); err != nil {
		return err
	} else if found {
		p.accounts = accounts
	}
	if gate, found, err := optional[authz.Gate](reg, "the route-declaration surface"); err != nil {
		return err
	} else if found {
		p.gate = gate
	}
	if routeGuard, found, err := optional[speedhttp.TenantAdminRouteGuard](reg, "the tenant administration guard"); err != nil {
		return err
	} else if found {
		p.routeGuard = routeGuard
	}
	if kind, found, err := optional[tenant.KindResolver](reg, "the tenant kind resolver"); err != nil {
		return err
	} else if found {
		p.kind = kind
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
	p.logger.Info(ModuleName+": the organization profile routes are mounted", "endpoints", p.cfg.endpoints)
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			endpointsItem: {
				Origins: config.OriginPrimary,
				Description: "the listening endpoints the organization profile routes are mounted on, " +
					"by the names the entry point's own configuration gives them; concrete names only, " +
					"never *. Unset means the API endpoint, [\"api\"]",
			},
		},
	}
}

var configDefaults = moduleConfig{Endpoints: []string{"api"}}

type moduleConfig struct {
	// Endpoints names the listening endpoints the routes are mounted on, by the
	// entry point's own names; the every-endpoint name is not accepted.
	Endpoints []string `config:"endpoints"`
}

type resolved struct {
	endpoints []string
}

func (c moduleConfig) resolve() (resolved, error) {
	if len(c.Endpoints) == 0 {
		return resolved{}, fmt.Errorf("%w: %s is empty. The organization profile routes are mounted on "+
			"the endpoints this module is told to and on none by default; list the endpoints that should "+
			"carry them, or leave this module out of the command", ErrInvalidConfig, endpointsKey)
	}
	seen := make(map[string]struct{}, len(c.Endpoints))
	for i, name := range c.Endpoints {
		switch name {
		case "":
			return resolved{}, fmt.Errorf("%w: %s holds an empty name at position %d",
				ErrInvalidConfig, endpointsKey, i)
		case allEndpoints:
			return resolved{}, fmt.Errorf("%w: %s holds %q at position %d. This module takes concrete "+
				"endpoint names only: mounting the organization profile routes on every endpoint would put "+
				"them on endpoints no tenant resolution or tenant guard reaches",
				ErrInvalidConfig, endpointsKey, allEndpoints, i)
		}
		if _, twice := seen[name]; twice {
			return resolved{}, fmt.Errorf("%w: %s names the endpoint %q twice, which would register "+
				"every organization profile route on it twice", ErrInvalidConfig, endpointsKey, name)
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
