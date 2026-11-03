// Package self is the tenant capability's caller-facing module: it mounts
// GET /api/v1/auth/me/tenants, the read that answers which tenants the calling
// account holds an active membership in. It delivers no capability of its own.
//
// The identity is the one the request's credential settled, read off the
// request; no path parameter or body names a subject. The route resolves no
// tenant: it is mounted on the account endpoint, whose requests carry no tenant
// name, because the answer is about the caller and not about one tenant.
//
// The route carries no permission: every authenticated caller may read its own
// memberships, and a route that required one could be held only by callers that
// already hold it. It is declared open on the route-declaration surface so the
// enforcement middleware lets it through, and it requires authenticated
// admission: the endpoint it is mounted on is one the gateway guards. A handler
// reached without an identity answers the same 401 the other authenticated
// surfaces answer, with the endpoint's realm.
//
// The read crosses two capabilities: the account module's Memberships lists an
// account's memberships across tenants, ordered by identifier, and the roster's
// Catalog answers a tenant's status and alias from its snapshot. A membership is
// answered only when it is itself active and its roster row is active; a
// suspended membership, a disabled tenant and an identifier the snapshot does
// not carry are all left out. A read that cannot answer is a 503 and never an
// empty list: an empty list is a conclusion about the memberships, and an
// outage is not.
//
// The routes are mounted on the endpoints the tenant.self.endpoints list names,
// and the start diagnostics name them.
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

// ModuleName is this module's name in the registry. A host writes it into the
// command that mounts the caller's tenant read.
const ModuleName = "tenant.self"

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
// This module's list refuses it: the route belongs on an endpoint a gateway
// guards and no other, so mounting it everywhere would put it on endpoints no
// admission layer guards.
const allEndpoints = "*"

// defaultEndpoint is the endpoint name this module's OpenAPI fragment is
// declared against. The routes are mounted on every endpoint the
// tenant.self.endpoints list names — data read only at start — and a resource is
// a static declaration fixed before configuration is read, so the fragment
// carries the conventional account endpoint rather than the configured ones.
// The route only makes sense on the endpoint the /auth prefix is claimed by.
const defaultEndpoint = "account"

// openAPIFragment is this module's OpenAPI fragment, read at build time. It is
// generated from operations, not written by hand.
//
//go:embed openapi/tenant.self.json
var openAPIFragment []byte

// tenantSelfSpec is the API root this module's fragment and routes hang off.
var tenantSelfSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: openAPIFragment,
}

// init registers this module with the process registry. Registration travels
// with the import, which is what makes "does this command mount the caller's
// tenant read" a question the host answers by importing this package and
// writing ModuleName into the command.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The membership read and the roster are required. Each is delivered
// exclusively by another module, so the closure of a command that takes this
// one in does not pull either provider in: the host writes them down. The kind
// resolver is optional: an assembly with no personal-tenant policy supplies
// none, and every tenant then reads as business, while an assembly that carries
// that policy provides one through its own module. The logger is optional,
// taken up so that it is initialised first.
//
// The HTTP entry point and the route-declaration surface are taken up in Init
// rather than declared: a command that mounts a route and has no entry point is
// a failure to report at wiring time, and the declaration surface's absence is
// legal — with no gate the route carries no declaration and a guarded endpoint
// answers it as undeclared, which is why a command that mounts it should name
// the enforcement module too.
//
// The stance is StateEnabled and never StateAuto: whether to mount the caller's
// tenant read is a decision on the command tree, not a conclusion a
// configuration can infer.
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

// productOf narrows an instance to this module's product. The callbacks are also
// handed an instance a host supplied for the module, which can be any value that
// delivers the capability; this module delivers none, so a supplied instance is
// left alone.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// product is the module's instance. It holds the two reads the answer composes
// and the optional route-declaration and gateway-admission surfaces taken up in
// Init.
type product struct {
	memberships authn.Memberships
	catalog     tenant.Catalog
	// kind answers one tenant's kind, nil when the assembly supplies no
	// resolver. A nil resolver reads every tenant as tenant.KindBusiness: an
	// assembly without a personal-tenant policy has no personal tenant.
	kind tenant.KindResolver

	// gate is the route-declaration surface, nil when the assembly delivers
	// none. When it is there, Init declares the route open on it so that a
	// guarded endpoint lets the request reach the handler.
	gate authz.Gate
	// admission is the gateway's route-admission surface, nil when the
	// assembly delivers none. When it is there, Init registers the route as
	// Impersonable: an impersonating identity reads the impersonated account's
	// membership in the tenant its grant is confined to, which is the alias
	// source a host has after a full page load.
	admission authn.Gate
	logger    *slog.Logger

	cfg resolved
}

// newProduct takes up what the descriptor requires.
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
// route-declaration surface and the logger, mounts the route on every
// configured endpoint, declares it open, and writes the start diagnostic.
//
// The entry point is required: a command that mounts a route and has no entry
// point has nothing to mount it on, and the failure names the module so the fix
// — writing the entry point module's name into this command — is visible. Every
// endpoint in the configured list is looked up before any route is registered,
// so a name the entry point does not declare fails the startup naming that
// endpoint rather than leaving a partial mounting behind.
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

// diagnose writes the start diagnostic: the endpoints the caller's tenant read
// was mounted on. The line is the module's own, so that a reader finds what the
// read surface did without scanning a chain listing.
func (p *product) diagnose() {
	p.logger.Info(ModuleName+": the caller's tenant read is mounted", "endpoints", p.cfg.endpoints)
}

// schema declares this module's input items. The namespace is this module's own
// name: two modules declaring one path is a startup failure even when only one
// of them runs.
//
// The endpoint list takes the primary source alone. It decides where the route
// is mounted, so an environment variable or a command-line argument that could
// rewrite it would be a way to move it onto an endpoint from outside the
// configuration file.
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

// configDefaults is what this module is worth without a configuration file.
//
// There is no default for the endpoints: a list that mounted the read nowhere
// would silently drop it, and guessing which endpoints should carry a
// caller-facing read is a guess about what is exposed. A module that cannot
// answer fails the startup, naming the key.
var configDefaults = moduleConfig{}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Endpoints names the listening endpoints the caller's tenant read is
	// mounted on, by the entry point's own names. The list is a set of concrete
	// names: the name that stands for every endpoint is not accepted.
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
		// The list is a set. The entry point holds one endpoint per name, so a
		// name written twice would register the route on that endpoint twice,
		// which the route engine refuses as a conflict.
		if _, twice := seen[name]; twice {
			return resolved{}, fmt.Errorf("%w: %s names the endpoint %q twice, which would register "+
				"the caller's tenant read on it twice", ErrInvalidConfig, endpointsKey, name)
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
