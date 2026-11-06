// Package profile is the tenant capability's organization-profile module: it
// mounts GET and PATCH /api/v1/tenants/{tenant}/profile and delivers no
// capability of its own.
//
// The profile is the descriptive face of a tenant — its display name,
// description, contact email, website — beside the identifier, alias and state
// the roster decides by. The routes read and write through the tenant.Catalog
// seam; this module holds no table and no rule of its own.
//
// The routes are tenant-scoped: the path names the tenant, the tenant
// administration guard holds it to the tenant the request resolved to and
// refuses the platform domain, and every decision is made in that tenant's
// policy domain. A write requires the acting account to have proved an email
// address, lands a mandatory audit row before the roster write, and answers a
// refusal with this surface's problem types.
//
// The routes are mounted on the endpoints the tenant.profile.endpoints list
// names, and the start diagnostics name them.
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

// ModuleName is this module's name in the registry. A host writes it into the
// command that mounts the organization profile routes.
const ModuleName = "tenant.profile"

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
// This module's list refuses it: the profile routes belong on an endpoint that
// carries tenant resolution and the tenant administration guard, and mounting
// them everywhere would put them where neither reaches.
const allEndpoints = "*"

// ErrInvalidConfig reports a configuration this module cannot run on: an
// endpoint list that is empty, carries an empty name, names one endpoint twice,
// or names the endpoint that stands for every one. The fix is in the file, and
// the message names the key.
var ErrInvalidConfig = errors.New("tenant.profile: a configuration this module cannot run on")

// defaultEndpoint is the endpoint name this module's OpenAPI fragment is
// declared against. The routes are mounted on every endpoint
// tenant.profile.endpoints lists — data read only at start, which may name
// several — and a resource is a static declaration fixed before configuration
// is read, so the fragment carries this conventional name rather than the
// configured ones.
const defaultEndpoint = "api"

// openAPIFragment is this module's OpenAPI fragment, read at build time. It is
// generated from the operations this module declares, not written by hand.
//
//go:embed openapi/tenant.profile.json
var openAPIFragment []byte

// tenantProfileSpec is the API root this module's fragment and routes hang off.
var tenantProfileSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Scope:    speedhttp.ScopeTenants,
	Document: openAPIFragment,
}

// init registers this module with the process registry. Registration travels
// with the import, which is what makes "does this command mount the
// organization profile routes" a question the host answers by importing this
// package and writing ModuleName into the command.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The roster, the handler-internal guard, the audit capability and the
// signup-verification read are required. The roster is delivered exclusively by
// the roster module and the guard by the enforcement module, so the closure of
// a command that takes this one in does not pull either in: the host writes
// them down. The verification read is what lets a write refuse an account whose
// email address is not proved. The logger is optional, taken up so that it is
// initialised first. The account read surface is optional too: with it the
// owner is shown by display name and username, and without it the owner stays
// unresolved rather than being named by the internal account identifier.
//
// The HTTP entry point, the tenant administration guard, the route-declaration
// surface and the logger are taken up in Init: a command that mounts routes and
// has no entry point is a failure to report at wiring time; a command with no
// tenant administration guard cannot mount a tenant-scoped route and is
// refused; the declaration surface's absence is legal.
//
// The stance is StateEnabled and never StateAuto: whether to mount the profile
// routes is a decision on the command tree, not a conclusion a configuration
// can infer.
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
			// The organization profile surface: its routes are mounted whenever
			// the module is in force and the process has an entry point, so the
			// feature is static.
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

// productOf narrows an instance to this module's product. The callbacks are
// also handed an instance a host supplied for the module, which can be any
// value that delivers the capability; this module delivers none, so a supplied
// instance is left alone.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// product is the module's instance. It holds the required capabilities and the
// optional seams taken up in Init.
type product struct {
	catalog tenant.Catalog
	guard   guard.Guard
	audit   audit.Audit
	// verification reads how far the acting account's email verification has
	// got. A write route reads it before the permission decision, so an account
	// whose email address is not proved cannot change a profile.
	verification authn.SignupVerification

	// accounts reads the account that owns a tenant, so the owner is shown by
	// display name and username rather than by the internal account identifier.
	// Nil means the assembly delivers none: the owner is unresolved and both
	// names stay empty.
	accounts authn.Accounts

	// gate is the route-declaration surface, nil when the assembly delivers
	// none. When it is there, Init declares every route open on it.
	gate authz.Gate
	// routeGuard is the tenant administration guard the tenant capability
	// delivers under the pkg/http TenantAdminRouteGuard capability: the identity
	// rule plus the refusal of the platform domain. Every route is wrapped with
	// it at the mount. Nil means the assembly delivers none, which a mount
	// refuses.
	routeGuard speedhttp.TenantAdminRouteGuard
	// kind answers one tenant's kind, nil when the assembly supplies no
	// resolver. With none every tenant reads as tenant.KindBusiness, the same
	// answer the caller tenant read gives and the same rule the platform roster
	// applies.
	kind   tenant.KindResolver
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
// seams, mounts the routes, declares them open, and writes the start
// diagnostic. The account read is optional: without it the owner is unresolved
// rather than named by the identifier, which is never shown.
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

// diagnose writes the start diagnostic: the endpoints the profile routes were
// mounted on. The line is the module's own, so that a reader finds what the
// surface did without scanning a chain listing.
func (p *product) diagnose() {
	p.logger.Info(ModuleName+": the organization profile routes are mounted", "endpoints", p.cfg.endpoints)
}

// schema declares this module's input items. The namespace is this module's own
// name: two modules declaring one path is a startup failure even when only one
// of them runs.
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

// configDefaults is what this module is worth without a configuration file: the
// API endpoint, which is the endpoint tenant resolution guards in the assembled
// product.
var configDefaults = moduleConfig{Endpoints: []string{"api"}}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Endpoints names the listening endpoints the organization profile routes
	// are mounted on, by the entry point's own names. The list is a set of
	// concrete names: the name that stands for every endpoint is not accepted.
	Endpoints []string `config:"endpoints"`
}

// resolved is the configuration this module runs on.
type resolved struct {
	endpoints []string
}

// resolve turns decoded configuration into what the module runs on, refusing
// what cannot be run on. A refusal names the key.
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
		// The list is a set. The entry point holds one endpoint per name, so a
		// name written twice would register every route on that endpoint twice,
		// which the route engine refuses as a conflict.
		if _, twice := seen[name]; twice {
			return resolved{}, fmt.Errorf("%w: %s names the endpoint %q twice, which would register "+
				"every organization profile route on it twice", ErrInvalidConfig, endpointsKey, name)
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
