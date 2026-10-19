// Package admin is the settings capability's management module: it mounts the
// setting read and write routes under /api/v1/settings/admin/* and delivers no
// capability of its own.
//
// The module holds no data and no rule. Every route's action is a call on the
// settings surface — reached through the settings.Store capability — so the
// settings module owns what a setting item is, its declared type, its value
// domain and the three-level fallback, and this module only decides who may ask
// and how the answer is shaped.
//
// Every route is declared open on the authorization middleware, so the
// middleware lets a request through and the route's own authorization step makes
// the decision: it reads the caller from the request, settles the layer and the
// policy domain from the query string, asks the handler-internal authz.guard API
// for settings.read on a read or settings.write on a write, and answers a
// shortfall before the declared handler runs. The domain follows the layer: a
// global read or write is decided in the platform domain, a tenant's in that
// tenant's own, so the two are two decisions and a caller authorized in one
// tenant cannot reach the global layer. A read that is allowed ends there; a
// write lands a mandatory audit row outside any transaction and before the
// action, calls the settings module, and maps a refusal to its answer.
//
// A sensitive item's value is masked here. The settings module withholds a
// sensitive item's default from its listing but still hands out its value from a
// read; this module masks that value before it reaches a client, so a credential
// never travels out with a response.
//
// The routes are mounted on the endpoints the settings.admin.endpoints list
// names, and the start diagnostics name the endpoints and the group.
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
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command that mounts the management routes.
const ModuleName = "settings.admin"

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
// This module's list refuses it: mounting the management routes on every
// endpoint, the public ones included, would put them where no gateway guards
// them.
const allEndpoints = "*"

// defaultEndpoint is the endpoint name this module's OpenAPI fragment is
// declared against. The management routes are mounted on every endpoint
// settings.admin.endpoints lists — data read only at start, which may name
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

// openAPIFragment is this module's OpenAPI fragment, read at build time. It is
// generated from operations, not written by hand.
//
//go:embed openapi/settings.admin.json
var openAPIFragment []byte

// adminSpec is the API root this module's fragment and routes hang off.
var adminSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: openAPIFragment,
}

// init registers this module with the process registry. Registration travels
// with the import, which is what makes "does this command mount the management
// routes" a question the host answers by importing this package and writing
// ModuleName into the command.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The settings surface, the handler-internal guard and the audit capability are
// required. That the guard is required is what makes the per-request decision
// available; that the audit capability is required is what makes an assembly
// without one fail before any instance is built, rather than at the first write.
// The settings surface is delivered exclusively by the settings module and the
// guard by the enforcement module, so the closure of a command that takes this
// one in does not pull them in: the host writes them down. The logger is the one
// optional requirement, taken up so that it is initialised first.
//
// The HTTP entry point and the route-declaration surface are taken up in Init
// rather than declared: a command that mounts routes and has no entry point is a
// failure to report at wiring time, and the declaration surface's absence is
// legal — with no gate the routes carry no declaration and a guarded endpoint
// answers them as undeclared.
//
// The outward declaration carries a speedhttp.Spec resource beside the schema:
// it holds this module's OpenAPI fragment, which describes the management
// routes, for a reader that merges the fragments into one document (the run's
// ops module, or a build's gen command).
//
// The module declares no permission of its own: the settings.read and
// settings.write it decides on are carried for the settings capability by the
// authorization directory, and it registers no catalog entry and no translation
// row. The stance is StateEnabled and never StateAuto: whether to mount the
// management routes is a decision on the command tree, not a conclusion a
// configuration can infer.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*settings.Store)(nil)},
			{Token: (*guard.Guard)(nil)},
			{Token: (*audit.Audit)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			schema(),
			adminSpec,
			operations,
			// The settings management surface: its routes are mounted whenever
			// the module is in force and the process has an entry point, so the
			// feature is static.
			feature.Spec{Name: "settings.admin"},
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
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	cfg, err := readConfig(reader)
	if err != nil {
		return nil, err
	}
	return &product{
		store:  store,
		guard:  guardAPI,
		audit:  auditor,
		logger: log.Default(),
		cfg:    cfg,
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
// Every endpoint in the configured list is looked up on the entry point before
// any route is mounted; a name the entry point does not declare is
// ErrUnknownEndpoint and stops the startup, because a route on an endpoint
// nobody serves is unreachable.
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

// diagnose writes the start diagnostic: the endpoints the management routes were
// mounted on and the groups that were mounted. The line is the module's own, so
// that a reader finds what the management routes did without scanning a chain
// listing.
func (p *product) diagnose() {
	p.logger.Info(ModuleName+": the management routes are mounted",
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
				Origins:  config.OriginPrimary,
				Required: true,
				Description: "the listening endpoints the management routes are mounted on, by the " +
					"names the entry point's own configuration gives them; concrete names only, never *",
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
	// Endpoints names the listening endpoints the management routes are mounted
	// on, by the entry point's own names. The list is a set of concrete names:
	// the name that stands for every endpoint is not accepted.
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
		return resolved{}, fmt.Errorf("%w: %s is empty. The management routes are mounted on the "+
			"endpoints this module is told to and on none by default; list the endpoints that should "+
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
				"endpoint names only: mounting the management routes on every endpoint would put them "+
				"on the public ones, where no gateway guards them",
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
