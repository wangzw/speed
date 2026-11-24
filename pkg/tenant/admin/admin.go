// Package admin mounts the tenant roster-management routes for platform staff and
// the organization-lifecycle routes for an organization's owner.
package admin

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
const ModuleName = "tenant.admin"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

const (
	endpointsItem       = "endpoints"
	endpointsKey        = ConfigNamespace + "." + endpointsItem
	tenantEndpointsItem = "tenant-endpoints"
	tenantEndpointsKey  = ConfigNamespace + "." + tenantEndpointsItem
	selfEndpointsItem   = "self-endpoints"
	selfEndpointsKey    = ConfigNamespace + "." + selfEndpointsItem
)

const allEndpoints = "*"

// ErrInvalidConfig reports a configuration this module cannot run on; the message names the key.
var ErrInvalidConfig = errors.New("tenant.admin: a configuration this module cannot run on")

const defaultEndpoint = "admin"

const defaultTenantEndpoint = "api"

const defaultSelfEndpoint = "account"

//go:embed openapi/tenant.admin.json
var openAPIFragment []byte

//go:embed openapi/tenant.owner.json
var tenantOwnerFragment []byte

//go:embed openapi/tenant.self.json
var selfFragment []byte

var tenantAdminSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Scope:    speedhttp.ScopeSystem,
	Document: openAPIFragment,
}

var tenantOwnerSpec = speedhttp.Spec{
	Endpoint: defaultTenantEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Scope:    speedhttp.ScopeTenants,
	Document: tenantOwnerFragment,
}

var selfSpec = speedhttp.Spec{
	Endpoint: defaultSelfEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: selfFragment,
}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*guard.Guard)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*authn.SignupVerification)(nil)},
			{Token: (*tenant.WriteGuard)(nil)},
			{Token: (*tenant.Provisioner)(nil)},
			{Token: (*tenant.OrganizationWriter)(nil)},
			{Token: (*tenant.OwnershipTarget)(nil)},
			{Token: (*speedhttp.TenantAdminRouteGuard)(nil)},
			{Token: (*tenant.KindResolver)(nil), Optional: true},
			{Token: (*tenant.NameResolver)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			schema(), permissionSpec(), translationSpec(), messageCatalog,
			tenantAdminSpec,
			tenantOwnerSpec,
			selfSpec,
			operations,
			tenantOperations,
			selfOperations,
			feature.Spec{Name: "tenant.admin"},
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
	writeGuard   tenant.WriteGuard
	provisioner  tenant.Provisioner
	organization tenant.OrganizationWriter
	ownerTarget  tenant.OwnershipTarget
	accounts     authn.Accounts
	routeGuard   speedhttp.TenantAdminRouteGuard
	kind         tenant.KindResolver
	names        tenant.NameResolver

	gate   authz.Gate
	logger *slog.Logger

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
	writeGuard, err := core.Resolve[tenant.WriteGuard](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant write guard: %w", ModuleName, err)
	}
	provisioner, err := core.Resolve[tenant.Provisioner](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant provisioner: %w", ModuleName, err)
	}
	organization, err := core.Resolve[tenant.OrganizationWriter](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the organization writer: %w", ModuleName, err)
	}
	ownerTarget, err := core.Resolve[tenant.OwnershipTarget](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the ownership target surface: %w", ModuleName, err)
	}
	routeGuard, err := core.Resolve[speedhttp.TenantAdminRouteGuard](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant administration guard: %w", ModuleName, err)
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
	names, _, err := optional[tenant.NameResolver](reg, "the tenant name resolver")
	if err != nil {
		return nil, err
	}
	return &product{
		catalog:      catalog,
		guard:        guardAPI,
		audit:        auditor,
		verification: verification,
		writeGuard:   writeGuard,
		provisioner:  provisioner,
		organization: organization,
		ownerTarget:  ownerTarget,
		routeGuard:   routeGuard,
		kind:         kind,
		names:        names,
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
		"tenant-endpoints", p.cfg.tenantEndpoints,
		"self-endpoints", p.cfg.selfEndpoints,
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
				Description: "the listening endpoints the platform management routes are mounted on, by the " +
					"names the entry point's own configuration gives them; concrete names only, never *",
			},
			tenantEndpointsItem: {
				Origins: config.OriginPrimary,
				Description: "the listening endpoints the tenant-surface owner-transfer and organization-deletion " +
					"routes are mounted on, by the names the entry point's own configuration gives them; concrete " +
					"names only, never *. They must be endpoints that carry tenant resolution and the tenant " +
					"administration guard. Unset means the API endpoint, [\"api\"]",
			},
			selfEndpointsItem: {
				Origins: config.OriginPrimary,
				Description: "the listening endpoints the target's account-level owner-transfer routes are mounted " +
					"on, by the names the entry point's own configuration gives them; concrete names only, never *. " +
					"They must carry the caller's identity and must not carry tenant resolution or the tenant " +
					"administration guard. Unset means the account endpoint, [\"account\"]",
			},
		},
	}
}

var configDefaults = moduleConfig{}

type moduleConfig struct {
	// Endpoints names the endpoints the platform management routes are mounted on.
	Endpoints []string `config:"endpoints"`
	// TenantEndpoints names the endpoints the tenant-surface routes are mounted on. Unset means the API endpoint.
	TenantEndpoints []string `config:"tenant-endpoints"`
	// SelfEndpoints names the endpoints the owner-transfer routes are mounted on. Unset means the account endpoint.
	SelfEndpoints []string `config:"self-endpoints"`
}

type resolved struct {
	endpoints       []string
	tenantEndpoints []string
	selfEndpoints   []string
}

func (c moduleConfig) resolve() (resolved, error) {
	endpoints, err := resolveEndpoints(endpointsKey, "management", c.Endpoints, true, "")
	if err != nil {
		return resolved{}, err
	}
	tenantEndpoints, err := resolveEndpoints(tenantEndpointsKey, "tenant-surface organization-lifecycle",
		c.TenantEndpoints, false, defaultTenantEndpoint)
	if err != nil {
		return resolved{}, err
	}
	selfEndpoints, err := resolveEndpoints(selfEndpointsKey, "account-level owner-transfer",
		c.SelfEndpoints, false, defaultSelfEndpoint)
	if err != nil {
		return resolved{}, err
	}
	return resolved{endpoints: endpoints, tenantEndpoints: tenantEndpoints, selfEndpoints: selfEndpoints}, nil
}

func resolveEndpoints(key, what string, list []string, required bool, fallback string) ([]string, error) {
	if len(list) == 0 {
		if !required {
			return []string{fallback}, nil
		}
		return nil, fmt.Errorf("%w: %s is empty. The %s routes are mounted on the "+
			"endpoints this module is told to and on none by default; list the endpoints that should "+
			"carry them, or leave this module out of the command", ErrInvalidConfig, key, what)
	}
	seen := make(map[string]struct{}, len(list))
	for i, name := range list {
		switch name {
		case "":
			return nil, fmt.Errorf("%w: %s holds an empty name at position %d",
				ErrInvalidConfig, key, i)
		case allEndpoints:
			return nil, fmt.Errorf("%w: %s holds %q at position %d. This module takes concrete "+
				"endpoint names only: mounting the %s routes on every endpoint would put them "+
				"on the public ones, where no gateway guards them",
				ErrInvalidConfig, key, allEndpoints, i, what)
		}
		if _, twice := seen[name]; twice {
			return nil, fmt.Errorf("%w: %s names the endpoint %q twice, which would register "+
				"every %s route on it twice", ErrInvalidConfig, key, name, what)
		}
		seen[name] = struct{}{}
	}
	return list, nil
}

func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
