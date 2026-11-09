// Package tenantadmin is the settings capability's tenant management module: it
// mounts the tenant layer's setting read and write routes under
// /api/v1/tenants/{tenant}/settings and delivers no capability of its own.
//
// It is the tenant-domain counterpart of pkg/settings/admin. Both read and write
// through the same settings.Store and share the setting view; they differ in the
// layer a route acts on and in the permission they ask for. The layer here is
// fixed by the tenant in the path — settings.ForTenant(tenant) — and there is no
// query parameter that could point a write at the global layer or at another
// tenant's. The decision is made in that tenant's policy domain.
//
// The path tenant is held to the tenant the request resolved to by the
// path-tenant layer the tenant capability delivers, and this surface also
// refuses the platform domain: it administers a tenant, and the platform domain
// is not one. This module assumes the rule and does not re-implement it, and it
// does not take the tenant roster in.
//
// A host supplies the mandatory authz.WriteSettings seam, which refuses a write
// to a personal tenant; an assembly that mounts this module without one fails at
// dependency resolution rather than deciding the refusal at run time.
//
// The declarations decide what this surface carries. A schema states
// Schema.TenantFacing, and each item states a TenantAccess: an item with no
// tenant access is invisible here, and a namespace's name neither opens nor
// closes it. A read-only declaration is visible and answers 403 read-only on
// both write routes; only an item declared read-write is writable. The
// exclusion narrows this surface alone: the settings module and the platform
// surface still read and write every declared item.
package tenantadmin

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
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command that mounts the tenant settings routes.
const ModuleName = "settings.tenantadmin"

// ConfigNamespace is where this module's input items are mounted. It is this
// module's own name, as a registering module's namespace always is.
const ConfigNamespace = ModuleName

// The configuration item this module accepts, relative to its namespace, and
// the full path a failure names.
const (
	endpointsItem = "endpoints"
	endpointsKey  = ConfigNamespace + "." + endpointsItem
)

// allEndpoints is the name that stands for every endpoint in an endpoint list.
// This module's list refuses it: mounting the tenant settings routes on every
// endpoint, the public ones included, would put them where no tenant resolution
// or tenant policy guard reaches.
const allEndpoints = "*"

// defaultEndpoint is the endpoint name this module's OpenAPI fragment is
// declared against. The management routes are mounted on every endpoint
// settings.tenantadmin.endpoints lists — data read only at start, which may name
// several — and a resource is a static declaration fixed before configuration is
// read, so the fragment carries a conventional name for the tenant settings
// surface rather than the configured ones. A declared endpoint name that no
// endpoint carries is not an error: the declaration and the mounting are
// separately maintained facts, in the same way as the declared paths and the
// registered routes (pkg/http's Spec).
const defaultEndpoint = "api"

// openAPIFragment is this module's OpenAPI fragment, read at build time. It is
// generated from operations, not written by hand.
//
//go:embed openapi/settings.tenantadmin.json
var openAPIFragment []byte

// tenantAdminSpec is the API root this module's fragment and routes hang off.
var tenantAdminSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Scope:    speedhttp.ScopeTenants,
	Document: openAPIFragment,
}

// init registers this module with the process registry. Registration travels
// with the import, which is what makes "does this command mount the tenant
// settings routes" a question the host answers by importing this package and
// writing ModuleName into the command.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The settings surface, the handler-internal guard, the audit capability and the
// registration-verification read are required, as they are for the platform
// settings surface. The settings surface is delivered exclusively by the settings
// module and the guard by the enforcement module, so the closure of a command
// that takes this one in does not pull them in: the host writes them down.
//
// The host's settings write guard is required rather than optional: it is the
// seam that refuses a write to a personal tenant, and an assembly that mounts
// this surface without one must fail the startup instead of leaving that refusal
// to be decided at run time. The HTTP entry point and the route-declaration
// surface are taken up in Init.
//
// The outward declaration carries a speedhttp.Spec resource beside the schema and
// the operation set: it holds this module's OpenAPI fragment, which describes
// the tenant settings routes, for a reader that merges the fragments into one
// document.
//
// The stance is StateEnabled and never StateAuto: whether to mount the
// management routes is a decision on the command tree, not a conclusion a
// configuration can infer.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*settings.Store)(nil)},
			{Token: (*guard.Guard)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*authn.SignupVerification)(nil)},
			{Token: (*authz.WriteSettings)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			schema(),
			messageCatalog,
			tenantAdminSpec,
			operations,
			// The tenant settings management surface: its routes are mounted
			// whenever the module is in force and the process has an entry point,
			// so the feature is static.
			feature.Spec{Name: "settings.tenantadmin"},
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

// productOf narrows an instance to this module's product. The callbacks are also
// handed an instance a host supplied for the module, which can be any value that
// delivers the capability; this module delivers none, so a supplied instance is
// left alone.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// product is the module's instance. It holds the required capabilities and the
// optional route-declaration surface taken up in Init.
type product struct {
	store settings.Store
	guard guard.Guard
	audit audit.Audit
	// verification reads how far the acting account's self-registration has
	// got. A write route reads it before the permission decision.
	verification authn.SignupVerification
	// writeSettings is the host seam that refuses a write to a personal tenant.
	// It is required, so it is present whenever this product exists.
	writeSettings authz.WriteSettings

	// gate is the route-declaration surface, nil when the assembly delivers
	// none. When it is there, Init declares every management route open on it.
	gate authz.Gate
	// localizer renders the titles and descriptions of the items this surface
	// exposes in the request's language, nil when no module delivers the
	// capability; the declared English text is then the answer.
	localizer i18n.Localizer
	// routeGuard is the tenant administration guard the tenant capability
	// delivers under the pkg/http TenantAdminRouteGuard capability: the identity
	// rule plus the refusal of the platform domain. Every route is wrapped with
	// it at the mount, so the tenant its path names has to be the tenant the
	// request resolved to and cannot be the platform domain, which is not a
	// tenant this surface administers. Nil means the assembly delivers none,
	// which a mount of a route refuses.
	routeGuard speedhttp.TenantAdminRouteGuard
	// names answers the name a tenant identifier is addressed by, nil when
	// the assembly supplies no resolver. The layer and the policy domain key by
	// the internal identifier; the audit detail carries the name, and with no
	// resolver it carries no tenant rather than the identifier.
	names  authn.TenantNameResolver
	logger *slog.Logger

	cfg resolved
}

// newProduct takes up what the descriptor requires.
func newProduct(reg *core.Registry) (*product, error) {
	store, err := core.Resolve[settings.Store](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the settings surface: %w", ModuleName, err)
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
	writeSettings, err := core.Resolve[authz.WriteSettings](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the settings write guard: %w", ModuleName, err)
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
		store:         store,
		guard:         guardAPI,
		audit:         auditor,
		verification:  verification,
		writeSettings: writeSettings,
		logger:        log.Default(),
		cfg:           cfg,
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
// route-declaration surface and the logger, resolves the configured endpoints,
// mounts the routes, declares them open, and writes the start diagnostic.
//
// The entry point is required: a command that mounts routes and has no entry
// point has nothing to mount them on, and the failure names the module so the
// fix — writing the entry point module's name into this command — is visible.
func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
	}
	if gate, found, err := optional[authz.Gate](reg, "the route-declaration surface"); err != nil {
		return err
	} else if found {
		p.gate = gate
	}
	if localizer, found, err := optional[i18n.Localizer](reg, "the message localizer"); err != nil {
		return err
	} else if found {
		p.localizer = localizer
	}
	if routeGuard, found, err := optional[speedhttp.TenantAdminRouteGuard](reg, "the tenant administration guard"); err != nil {
		return err
	} else if found {
		p.routeGuard = routeGuard
	}
	if names, found, err := optional[authn.TenantNameResolver](reg, "the tenant name resolver"); err != nil {
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

// diagnose writes the start diagnostic: the endpoints the management routes were
// mounted on and the groups that were mounted.
func (p *product) diagnose() {
	p.logger.Info(ModuleName+": the tenant settings routes are mounted",
		"endpoints", p.cfg.endpoints,
		"groups", managementGroups)
}

// schema declares this module's input items. The namespace is this module's own
// name: two modules declaring one path is a startup failure even when only one
// of them runs.
//
// The endpoint list takes the primary source alone. It decides where the
// management routes are mounted, so an environment variable or a command-line
// argument that could rewrite it would be a way to move them onto an endpoint
// from outside the configuration file.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			endpointsItem: {
				Origins: config.OriginPrimary,
				Description: "the listening endpoints the tenant settings routes are mounted on, " +
					"by the names the entry point's own configuration gives them; concrete names only, " +
					"never *. Unset means the API endpoint, [\"api\"]",
			},
		},
	}
}

// configDefaults is what this module is worth without a configuration file: the
// API endpoint, which is the endpoint tenant resolution guards in the assembled
// product. A deployment that mounts the tenant routes elsewhere writes its own
// list; an explicitly empty list is refused rather than mounting them nowhere.
var configDefaults = moduleConfig{Endpoints: []string{"api"}}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Endpoints names the listening endpoints the tenant settings routes are
	// mounted on, by the entry point's own names. The list is a set of concrete
	// names: the name that stands for every endpoint is not accepted.
	Endpoints []string `config:"endpoints"`
}

// resolved is the configuration this module runs on.
type resolved struct {
	endpoints []string
}

// resolve turns decoded configuration into what the module runs on, refusing
// what cannot be run on. A refusal names the key and wraps ErrInvalidConfig, the
// sentinel this module judges a configuration it cannot run on with.
func (c moduleConfig) resolve() (resolved, error) {
	if len(c.Endpoints) == 0 {
		return resolved{}, fmt.Errorf("%w: %s is empty. The tenant settings routes are mounted on "+
			"the endpoints this module is told to and on none by default; list the endpoints that "+
			"should carry them, or leave this module out of the command", ErrInvalidConfig, endpointsKey)
	}
	seen := make(map[string]struct{}, len(c.Endpoints))
	for i, name := range c.Endpoints {
		switch name {
		case "":
			return resolved{}, fmt.Errorf("%w: %s holds an empty name at position %d",
				ErrInvalidConfig, endpointsKey, i)
		case allEndpoints:
			return resolved{}, fmt.Errorf("%w: %s holds %q at position %d. This module takes concrete "+
				"endpoint names only: mounting the tenant settings routes on every endpoint would put "+
				"them on endpoints no tenant resolution or tenant policy guard reaches",
				ErrInvalidConfig, endpointsKey, allEndpoints, i)
		}
		// The list is a set. The entry point holds one endpoint per name, so a
		// name written twice would register every management route on that
		// endpoint twice, which the route engine refuses as a conflict.
		if _, twice := seen[name]; twice {
			return resolved{}, fmt.Errorf("%w: %s names the endpoint %q twice, which would register "+
				"every management route on it twice", ErrInvalidConfig, endpointsKey, name)
		}
		seen[name] = struct{}{}
	}
	return resolved{endpoints: c.Endpoints}, nil
}

// readConfig decodes this module's section and validates it. Prepare and New
// both go through it, so the two see one configuration.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
