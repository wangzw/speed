// Package tenantadmin is the authorization capability's tenant management
// module: it mounts the tenant-scoped permission catalog, role, grant and
// binding routes under /api/v1/tenants/{tenant}/* and delivers no capability of
// its own.
//
// The module is the tenant-domain counterpart of pkg/authz/admin. It reads and
// writes the same policy tables through the same Manager and Catalog; the two
// differ in the policy domain a route is decided in, the permission codes it
// asks for and the anti-escalation rules it applies. Every route acts in the
// tenant the path names, and the path tenant is held to the tenant the request
// resolved to by the path-tenant layer the tenant capability delivers and this
// module wraps each route with. This surface also refuses the platform domain:
// it administers a tenant, and the platform domain is not one.
//
// The module grants itself no permission; it carries only the tenant role
// vocabulary the catalog already declares. Every route is declared open on the
// authorization middleware, so the middleware lets a request through to the
// route's guard wrapper, which makes the decision in front of the typed
// handler: it reads the caller from the request, asks the handler-internal
// Guard for authz.tenant.role.read on a read or authz.tenant.role.manage on a
// write in the path tenant's domain, and answers a shortfall before anything
// else runs. A read that is allowed ends there; a write runs the registration
// gate, its shape checks, every anti-escalation check, the optional host
// WriteGuard, a mandatory audit row outside any transaction and before the
// action, calls the manager, and maps a refusal to its answer.
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
)

// ModuleName is this module's name in the registry. A host writes it into the
// command that mounts the tenant management routes.
const ModuleName = "authz.tenantadmin"

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
// This module's list refuses it: mounting the tenant management routes on every
// endpoint, the public ones included, would put them where no tenant resolution
// or tenant policy guard reaches.
const allEndpoints = "*"

// defaultEndpoint is the endpoint name this module's OpenAPI fragment is
// declared against. The management routes are mounted on every endpoint
// authz.tenantadmin.endpoints lists — data read only at start, which may name
// several — and a resource is a static declaration fixed before configuration is
// read, so the fragment carries a conventional name for the tenant management
// surface rather than the configured ones. A declared endpoint name that no
// endpoint carries is not an error: the declaration and the mounting are
// separately maintained facts, in the same way as the declared paths and the
// registered routes (pkg/http's Spec).
const defaultEndpoint = "api"

// openAPIFragment is this module's OpenAPI fragment, read at build time. It is
// generated from operations, not written by hand.
//
//go:embed openapi/authz.tenantadmin.json
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
// management routes" a question the host answers by importing this package and
// writing ModuleName into the command.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The policy manager, the permission catalog, the permission reader, the
// membership checker, the handler-internal guard, the audit capability and the
// registration-verification read are required. The manager and the catalog are
// delivered exclusively by the policy module and the guard by the enforcement
// module, so the closure of a command that takes this one in does not pull them
// in: the host writes them down. The permission reader is what makes the
// anti-escalation rule "a caller may not grant a permission it does not hold"
// answerable, and the membership checker is what makes a binding address only a
// tenant's active member; both absent is a startup failure, never a write that
// proceeds on an empty answer. The audit capability being required is what
// makes an assembly without one fail before any instance is built, and the
// verification read is what lets a write refuse an account whose
// self-registration has not proved an email address.
//
// The HTTP entry point, the tenant administration guard, the route-declaration
// surface, the host write guard, the message localizer and the logger are taken
// up in Init: a command that mounts routes and has no entry point is a failure
// to report at wiring time; a command with no tenant administration guard
// cannot mount a tenant-scoped route and is refused; the declaration surface's
// absence, the write guard's absence and the localizer's absence are all legal.
//
// The outward declaration carries a speedhttp.Spec resource beside the schema:
// it holds this module's OpenAPI fragment, which describes the tenant
// management routes, for a reader that merges the fragments into one document.
//
// The stance is StateEnabled and never StateAuto: whether to mount the tenant
// management routes is a decision on the command tree, not a conclusion a
// configuration can infer.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*authz.Manager)(nil)},
			{Token: (*authz.Catalog)(nil)},
			{Token: (*authz.SubjectPermissions)(nil)},
			{Token: (*authz.MembershipChecker)(nil)},
			{Token: (*guard.Guard)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*authn.SignupVerification)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			schema(),
			tenantAdminSpec,
			operations,
			// The tenant management surface: its routes are mounted whenever the
			// module is in force and the process has an entry point, neither of
			// which the module itself varies, so the feature is static.
			feature.Spec{Name: "authz.tenantadmin"},
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
	manager    authz.Manager
	catalog    authz.Catalog
	subject    authz.SubjectPermissions
	membership authz.MembershipChecker
	guard      guard.Guard
	audit      audit.Audit
	// verification reads how far the acting account's self-registration has
	// got. A write route reads it before the permission decision, so an account
	// that has not proved an email address cannot change policy data.
	verification authn.SignupVerification

	// gate is the route-declaration surface, nil when the assembly delivers
	// none. When it is there, Init declares every route open on it.
	gate authz.Gate
	// writeGuard is the optional host seam a role, grant or binding write asks
	// before the mandatory audit and the manager. Nil means the assembly
	// delivers none, and the writes run as they do without the seam.
	writeGuard authz.WriteGuard
	// localizer renders a permission's description into the request's language,
	// nil when the assembly delivers no i18n engine. With none the catalog route
	// answers an empty description.
	localizer i18n.Localizer
	// routeGuard is the tenant administration guard the tenant capability
	// delivers under the pkg/http TenantAdminRouteGuard capability: the identity
	// rule plus the refusal of the platform domain. Every route is wrapped with
	// it at the mount, so the tenant its path names has to be the tenant the
	// request resolved to and cannot be the platform domain, which is not a
	// tenant this surface administers. Nil means the assembly delivers none,
	// which a mount refuses.
	routeGuard speedhttp.TenantAdminRouteGuard
	logger     *slog.Logger

	cfg resolved
}

// newProduct takes up what the descriptor requires.
func newProduct(reg *core.Registry) (*product, error) {
	manager, err := core.Resolve[authz.Manager](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the policy manager: %w", ModuleName, err)
	}
	catalog, err := core.Resolve[authz.Catalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the permission catalog: %w", ModuleName, err)
	}
	subject, err := core.Resolve[authz.SubjectPermissions](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the permission reader: %w", ModuleName, err)
	}
	membership, err := core.Resolve[authz.MembershipChecker](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the membership checker: %w", ModuleName, err)
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
		manager:      manager,
		catalog:      catalog,
		subject:      subject,
		membership:   membership,
		guard:        guardAPI,
		audit:        auditor,
		verification: verification,
		logger:       log.Default(),
		cfg:          cfg,
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
	if routeGuard, found, err := optional[speedhttp.TenantAdminRouteGuard](reg, "the tenant administration guard"); err != nil {
		return err
	} else if found {
		p.routeGuard = routeGuard
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
	p.logger.Info(ModuleName+": the tenant management routes are mounted",
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
				Description: "the listening endpoints the tenant management routes are mounted on, " +
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
	// Endpoints names the listening endpoints the tenant management routes are
	// mounted on, by the entry point's own names. The list is a set of concrete
	// names: the name that stands for every endpoint is not accepted.
	Endpoints []string `config:"endpoints"`
}

// resolved is the configuration this module runs on.
type resolved struct {
	endpoints []string
}

// resolve turns decoded configuration into what the module runs on, refusing
// what cannot be run on. A refusal names the key. The module has no failure
// sentinel of its own, so a shape it cannot run on is authn.ErrInvalidConfig,
// the sentinel the platform management surface refuses its own shapes with.
func (c moduleConfig) resolve() (resolved, error) {
	if len(c.Endpoints) == 0 {
		return resolved{}, fmt.Errorf("%w: %s is empty. The tenant management routes are mounted on "+
			"the endpoints this module is told to and on none by default; list the endpoints that "+
			"should carry them, or leave this module out of the command", authn.ErrInvalidConfig, endpointsKey)
	}
	seen := make(map[string]struct{}, len(c.Endpoints))
	for i, name := range c.Endpoints {
		switch name {
		case "":
			return resolved{}, fmt.Errorf("%w: %s holds an empty name at position %d",
				authn.ErrInvalidConfig, endpointsKey, i)
		case allEndpoints:
			return resolved{}, fmt.Errorf("%w: %s holds %q at position %d. This module takes concrete "+
				"endpoint names only: mounting the tenant management routes on every endpoint would put "+
				"them on endpoints no tenant resolution or tenant policy guard reaches",
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

// readConfig decodes this module's section and validates it. Prepare and New
// both go through it, so the two see one configuration.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
