// Package admin is the authorization capability's management module: it mounts
// the permission catalog, role, grant and binding routes under /api/v1/system/*
// and delivers no capability of its own.
//
// Every route is declared open on the authorization middleware; the route's
// guard wrapper makes the decision in front of the typed handler, asking for
// authz.role.read on a read or authz.role.manage on a write. A write then runs
// its shape checks, asks the optional host WriteGuard, lands a mandatory audit
// row before the action, calls the manager, and maps a refusal to its answer.
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
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
)

// ModuleName is this module's name in the registry.
const ModuleName = "authz.admin"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

// The configuration item this module accepts, relative to its namespace, and
// the full path a failure names.
const (
	endpointsItem = "endpoints"
	endpointsKey  = ConfigNamespace + "." + endpointsItem
)

// allEndpoints is the name that stands for every endpoint in an endpoint list.
// This module's list refuses it.
const allEndpoints = "*"

// defaultEndpoint is the endpoint name this module's OpenAPI fragment is
// declared against, a static name rather than a configured one.
const defaultEndpoint = "admin"

// openAPIFragment is this module's OpenAPI fragment, read at build time. It is
// generated from operations, not written by hand.
//
//go:embed openapi/authz.admin.json
var openAPIFragment []byte

// authzAdminSpec is the API root this module's fragment and routes hang off.
var authzAdminSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Scope:    speedhttp.ScopeSystem,
	Document: openAPIFragment,
}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The policy manager, the permission catalog, the handler-internal guard, the
// audit capability, the registration-verification read and the caller's
// permission read are required; the logger is optional. The HTTP entry point,
// the route-declaration surface and the host write guard are taken up in Init
// rather than declared. The stance is StateEnabled and never StateAuto.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*authz.Manager)(nil)},
			{Token: (*authz.Catalog)(nil)},
			{Token: (*guard.Guard)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*authn.SignupVerification)(nil)},
			{Token: (*authz.SubjectPermissions)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			schema(),
			authzAdminSpec,
			operations,
			feature.Spec{Name: "authz.admin"},
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

// productOf narrows an instance to this module's product. A supplied instance of
// another shape is left alone: this module delivers no capability.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// product is the module's instance.
type product struct {
	manager      authz.Manager
	catalog      authz.Catalog
	guard        guard.Guard
	audit        audit.Audit
	verification authn.SignupVerification
	subject      authz.SubjectPermissions
	identities   authz.AccountIdentityReader

	gate       authz.Gate
	writeGuard authz.WriteGuard
	localizer  i18n.Localizer
	names      authz.TenantNameResolver
	logger     *slog.Logger

	cfg resolved
}

func newProduct(reg *core.Registry) (*product, error) {
	manager, err := core.Resolve[authz.Manager](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the policy manager: %w", ModuleName, err)
	}
	catalog, err := core.Resolve[authz.Catalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the permission catalog: %w", ModuleName, err)
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
	subject, err := core.Resolve[authz.SubjectPermissions](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the caller's permission read: %w", ModuleName, err)
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
		manager:      manager,
		catalog:      catalog,
		guard:        guardAPI,
		audit:        auditor,
		verification: verification,
		subject:      subject,
		logger:       log.Default(),
		cfg:          cfg,
	}, nil
}

// optional takes up a capability whose absence is a legal assembly. Any other
// failure to take it up ends the start.
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

// wire runs in Init: it takes up the optional capabilities and the logger,
// resolves the configured endpoints, mounts the routes, declares them open, and
// writes the start diagnostic.
func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
	}
	if gate, found, err := optional[authz.Gate](reg, "the route-declaration surface"); err != nil {
		return err
	} else if found {
		p.gate = gate
	}
	if writeGuard, found, err := optional[authz.WriteGuard](reg, "the write guard"); err != nil {
		return err
	} else if found {
		p.writeGuard = writeGuard
	}
	if localizer, found, err := optional[i18n.Localizer](reg, "the message localizer"); err != nil {
		return err
	} else if found {
		p.localizer = localizer
	}
	if identities, found, err := optional[authz.AccountIdentityReader](reg, "the account-identity reader"); err != nil {
		return err
	} else if found {
		p.identities = identities
	}
	if names, found, err := optional[authz.TenantNameResolver](reg, "the tenant name resolver"); err != nil {
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
	p.logger.Info(ModuleName+": the management routes are mounted",
		"endpoints", p.cfg.endpoints,
		"groups", managementGroups)
}

// schema declares this module's input items. The endpoint list takes the primary
// source alone.
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

// configDefaults is what this module is worth without a configuration file.
// There is no default for the endpoints: the module that cannot answer fails the
// startup, naming the key.
var configDefaults = moduleConfig{}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	Endpoints []string `config:"endpoints"`
}

// resolved is the configuration this module runs on.
type resolved struct {
	endpoints []string
}

// resolve turns decoded configuration into what the module runs on, refusing
// what cannot be run on. A shape it cannot run on is authn.ErrInvalidConfig.
func (c moduleConfig) resolve() (resolved, error) {
	if len(c.Endpoints) == 0 {
		return resolved{}, fmt.Errorf("%w: %s is empty. The management routes are mounted on the "+
			"endpoints this module is told to and on none by default; list the endpoints that should "+
			"carry them, or leave this module out of the command", authn.ErrInvalidConfig, endpointsKey)
	}
	seen := make(map[string]struct{}, len(c.Endpoints))
	for i, name := range c.Endpoints {
		switch name {
		case "":
			return resolved{}, fmt.Errorf("%w: %s holds an empty name at position %d",
				authn.ErrInvalidConfig, endpointsKey, i)
		case allEndpoints:
			return resolved{}, fmt.Errorf("%w: %s holds %q at position %d. This module takes concrete "+
				"endpoint names only: mounting the management routes on every endpoint would put them "+
				"on the public ones, where no gateway guards them",
				authn.ErrInvalidConfig, endpointsKey, allEndpoints, i)
		}
		// The list is a set. The entry point holds one endpoint per name, so a
		// name written twice would register every management route on that
		// endpoint twice, which the route engine refuses as a conflict.
		if _, twice := seen[name]; twice {
			return resolved{}, fmt.Errorf("%w: %s names the endpoint %q twice, which would register "+
				"every management route on it twice", authn.ErrInvalidConfig, endpointsKey, name)
		}
		seen[name] = struct{}{}
	}
	return resolved{endpoints: c.Endpoints}, nil
}

// readConfig decodes this module's section and validates it.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
