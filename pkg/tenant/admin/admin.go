// Package admin is the tenant capability's management module: it mounts the
// roster-management routes under /api/v1/system/tenants for platform staff and
// the organization-lifecycle routes under /api/v1/tenants for the organization's
// own owner, and delivers no capability of its own.
//
// The module holds no data and no rule. Every route's action is a call on the
// roster — reached through the tenant.Catalog capability — so the roster owns
// which tenants exist, what names they carry and which are served, and this
// module only decides who may ask and how the answer is shaped. It builds no
// table, caches nothing and migrates nothing: each action is one Catalog call.
//
// Every route is declared open on the authorization middleware, so the
// middleware lets a request through and the handler makes the decision itself:
// it reads the caller from the request, reads the acting account's email
// verification state on a write and refuses one whose email address is not
// proved with 403 contact-required, then asks the handler-internal authz.guard
// API for tenant.read on a read or tenant.manage on a write, in the platform
// domain, and answers a shortfall before anything else runs. The tenant-surface
// routes instead hold the path tenant to the request's resolved tenant and
// admit only the row's owner, asking no permission. A write runs its shape
// checks, asks the host's tenant.WriteGuard on the status route, lands a
// mandatory audit row outside any transaction and before the action, calls the
// roster — or, for a registration, the host's tenant.Provisioner, which creates
// the tenant and its first administrator together — and maps a refusal to its
// answer.
//
// The platform routes are mounted on the endpoints the tenant.admin.endpoints
// list names, and the tenant-surface routes on the endpoints
// tenant.admin.tenant-endpoints names; the start diagnostics name both lists and
// the groups.
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

// ModuleName is this module's name in the registry. A host writes it into the
// command that mounts the management routes.
const ModuleName = "tenant.admin"

// ConfigNamespace is where this module's input items are mounted. It is this
// module's own name, as a registering module's namespace always is.
const ConfigNamespace = ModuleName

// The configuration items this module accepts, relative to its namespace, and
// the full path a failure names.
const (
	endpointsItem       = "endpoints"
	endpointsKey        = ConfigNamespace + "." + endpointsItem
	tenantEndpointsItem = "tenant-endpoints"
	tenantEndpointsKey  = ConfigNamespace + "." + tenantEndpointsItem
	selfEndpointsItem   = "self-endpoints"
	selfEndpointsKey    = ConfigNamespace + "." + selfEndpointsItem
)

// allEndpoints is the name that stands for every endpoint in an endpoint list.
// This module's list refuses it: mounting the management routes on every
// endpoint, the public ones included, would put them where no gateway guards
// them.
const allEndpoints = "*"

// ErrInvalidConfig reports a configuration this module cannot run on: an
// endpoint list that is empty, carries an empty name, names one endpoint twice,
// or names the endpoint that stands for every one. The fix is in the file, and
// the message names the key.
var ErrInvalidConfig = errors.New("tenant.admin: a configuration this module cannot run on")

// defaultEndpoint is the endpoint name this module's OpenAPI fragment is
// declared against. The management routes are mounted on every endpoint
// tenant.admin.endpoints lists — data read only at start, which may name
// several — and a resource is a static declaration fixed before configuration
// is read, so the fragment carries this conventional name for the management
// surface rather than the configured ones. A declared endpoint name that no
// endpoint carries is not an error: the declaration and the mounting are
// separately maintained facts, in the same way as the declared paths and the
// registered routes (pkg/http's Spec).
//
// Unlike the ops module, this module does not rewrite the endpoint in Init:
// ops is the merger and hands its own slice to the merge, while a declaring
// module's resources are read back as copies, so a rewrite here would not reach
// the merged document.
const defaultEndpoint = "admin"

// defaultTenantEndpoint is the endpoint name the tenant-surface fragment is
// declared against. The tenant-surface routes need the endpoint that carries
// tenant resolution and the tenant administration guard, which the demo names
// "api"; the declaration uses that conventional name for the same reason
// defaultEndpoint uses "admin".
const defaultTenantEndpoint = "api"

// defaultSelfEndpoint is the endpoint the target's account-level routes are
// mounted on by default. They carry the caller's own identity and no tenant
// resolution, so they belong on the account endpoint with the rest of
// /api/v1/auth/me.
const defaultSelfEndpoint = "account"

// openAPIFragment is this module's OpenAPI fragment, read at build time. It is
// generated from the operations this module declares, not written by hand.
//
//go:embed openapi/tenant.admin.json
var openAPIFragment []byte

// tenantOwnerFragment is the fragment of the organization-lifecycle routes that
// belong to the tenant surface. It is a second fragment because a Spec carries
// one scope: the platform routes live under /api/v1/system/tenants, while the
// owner transfer and the organization deletion are addressed by the tenant
// under /api/v1/tenants and decided by the caller's ownership.
//
//go:embed openapi/tenant.owner.json
var tenantOwnerFragment []byte

// selfFragment is the fragment of the target's account-level transfer routes.
// It is a third fragment because those routes are ordinary ones: they carry no
// tenant segment and no scope root, and the account endpoint mounts them under
// /api/v1/auth/me.
//
//go:embed openapi/tenant.self.json
var selfFragment []byte

// tenantAdminSpec is the API root the platform management routes hang off. The
// fragment is generated from operations; the routes are mounted from the same
// declarations and resolved against this root.
var tenantAdminSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Scope:    speedhttp.ScopeSystem,
	Document: openAPIFragment,
}

// tenantOwnerSpec is the API root the tenant-surface organization-lifecycle
// routes hang off. It is the tenants scope, so every path names the tenant
// first and the path-tenant layer holds the segment to the tenant the request
// resolved to.
var tenantOwnerSpec = speedhttp.Spec{
	Endpoint: defaultTenantEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Scope:    speedhttp.ScopeTenants,
	Document: tenantOwnerFragment,
}

// selfSpec is the API root the target's account-level routes hang off. It is the
// ordinary scope: the caller's own resources under the account endpoint.
var selfSpec = speedhttp.Spec{
	Endpoint: defaultSelfEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: selfFragment,
}

// init registers this module with the process registry. Registration travels
// with the import, which is what makes "does this command mount the management
// routes" a question the host answers by importing this package and writing
// ModuleName into the command.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The roster, the handler-internal guard, the audit capability, the
// email-verification read, the tenant administration route guard and the host's
// two write seams are required. That the guard is required is what makes the
// per-request decision available; that the audit capability is required is what
// makes an assembly without one fail before any instance is built, rather than
// at the first write; the verification read is what lets a write refuse an
// account whose email address is not proved; the tenant administration route
// guard is what holds a tenant-surface path to the request's resolved tenant;
// the write guard is what lets the host declare a tenant that may not be
// disabled; and the provisioner is what creates a tenant together with its first
// administrator. The roster is delivered exclusively by the
// roster module and the guard by the enforcement module, so the closure of a
// command that takes this one in does not pull them in: the host writes them
// down. The write guard and the provisioner are host-supplied seams, so a
// command that mounts every write route names them too. The logger is the one
// optional requirement, taken up so that it is initialised first.
//
// The HTTP entry point and the route-declaration surface are taken up in Init
// rather than declared: a command that mounts routes and has no entry point is a
// failure to report at wiring time, and the declaration surface's absence is
// legal — with no gate the routes carry no declaration and a guarded endpoint
// answers them as undeclared.
//
// The outward declaration carries a speedhttp.Spec resource beside the schema
// and the two authorization specs: it holds this module's OpenAPI fragment,
// which describes the management routes, for a reader that merges the fragments
// into one document (the run's ops module, or a build's gen command). The
// operations the fragment is generated from are declared beside it.
//
// The stance is StateEnabled and never StateAuto: whether to mount the
// management routes is a decision on the command tree, not a conclusion a
// configuration can infer.
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
			// The roster management surface: its routes are mounted whenever the
			// module is in force and the process has an entry point, so the
			// feature is static.
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
	catalog tenant.Catalog
	guard   guard.Guard
	audit   audit.Audit
	// verification reads how far the acting account's email verification has
	// got. A write route reads it before the permission decision, so an account
	// whose email address is not proved cannot change the roster.
	verification authn.SignupVerification
	// writeGuard is the host's answer to whether a tenant refuses an operator
	// write. The status route asks it before the audit and the roster write.
	writeGuard tenant.WriteGuard
	// provisioner creates a tenant together with its first administrator, so
	// the registration route never calls Catalog.Create itself.
	provisioner tenant.Provisioner
	// organization performs the owner transfer and the organization deletion,
	// the two writes that span the roster, the policy store and the membership
	// store and therefore have to be one transaction.
	organization tenant.OrganizationWriter
	// ownerTarget is the target's own side of a transfer request. Its routes are
	// account-level and carry no tenant context.
	ownerTarget tenant.OwnershipTarget
	// accounts resolves a transfer target's display name and username, nil when
	// the assembly supplies no account read surface. A nil surface leaves both
	// names empty rather than falling back to an internal identifier.
	accounts authn.Accounts
	// routeGuard is the tenant administration guard the tenant-scoped
	// organization-lifecycle routes are wrapped with: the path-tenant identity
	// rule plus the refusal of the platform domain.
	routeGuard speedhttp.TenantAdminRouteGuard
	// kind answers one tenant's kind, nil when the assembly supplies no
	// resolver. A nil resolver reads every tenant as tenant.KindBusiness, the
	// same rule the caller's tenant read applies.
	kind tenant.KindResolver
	// names answers the name an identifier is addressed by, nil when the
	// assembly supplies no resolver. A nil resolver names no tenant in an audit
	// row rather than falling back to the internal identifier.
	names tenant.NameResolver

	// gate is the route-declaration surface, nil when the assembly delivers
	// none. When it is there, Init declares every management route open on it so
	// that a guarded endpoint lets the request reach the handler, which makes the
	// decision.
	gate   authz.Gate
	logger *slog.Logger

	cfg resolved
}

// newProduct takes up what the descriptor requires.
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
// route-declaration surface and the logger, mounts the routes, declares them
// open, and writes the start diagnostic.
//
// The entry point is required: a command that mounts routes and has no entry
// point has nothing to mount them on, and the failure names the module so the
// fix — writing the entry point module's name into this command — is visible.
// Every endpoint in the configured list is looked up on the entry point before
// any route is mounted; a name the entry point does not declare is
// ErrUnknownEndpoint and stops the startup, because a route on an endpoint
// nobody serves is unreachable.
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

// diagnose writes the start diagnostic: the endpoints the management routes were
// mounted on and the groups that were mounted. The line is the module's own, so
// that a reader finds what the management routes did without scanning a chain
// listing.
func (p *product) diagnose() {
	p.logger.Info(ModuleName+": the management routes are mounted",
		"endpoints", p.cfg.endpoints,
		"tenant-endpoints", p.cfg.tenantEndpoints,
		"self-endpoints", p.cfg.selfEndpoints,
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

// configDefaults is what this module is worth without a configuration file.
//
// There is no default for the endpoints: a list that mounted the management
// routes nowhere would silently drop them, and guessing which endpoints should
// carry them is a guess about what is exposed. A module that cannot answer fails
// the startup, naming the key.
var configDefaults = moduleConfig{}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Endpoints names the listening endpoints the platform management routes
	// are mounted on, by the entry point's own names. The list is a set of
	// concrete names: the name that stands for every endpoint is not accepted.
	Endpoints []string `config:"endpoints"`
	// TenantEndpoints names the listening endpoints the tenant-surface
	// organization-lifecycle routes are mounted on. They must carry tenant
	// resolution and the tenant administration guard. Unset means the API
	// endpoint.
	TenantEndpoints []string `config:"tenant-endpoints"`
	// SelfEndpoints names the listening endpoints the target's account-level
	// owner-transfer routes are mounted on. They must carry the caller's identity
	// and no tenant resolution. Unset means the account endpoint.
	SelfEndpoints []string `config:"self-endpoints"`
}

// resolved is the configuration this module runs on.
type resolved struct {
	endpoints       []string
	tenantEndpoints []string
	selfEndpoints   []string
}

// resolve turns decoded configuration into what the module runs on, refusing
// what cannot be run on. A refusal names the key and is this module's own
// ErrInvalidConfig: a management surface mounted nowhere, or on every endpoint,
// is a shape this module will not run on.
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

// resolveEndpoints validates one endpoint list the same way. required says an
// empty list is a failure; otherwise an empty list takes fallback.
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
		// The list is a set. The entry point holds one endpoint per name, so a
		// name written twice would register every route on that endpoint twice,
		// which the route engine refuses as a conflict.
		if _, twice := seen[name]; twice {
			return nil, fmt.Errorf("%w: %s names the endpoint %q twice, which would register "+
				"every %s route on it twice", ErrInvalidConfig, key, name, what)
		}
		seen[name] = struct{}{}
	}
	return list, nil
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
