// Package stream is the notification capability's real-time push module: it
// pushes "the inbox just gained a row" to the clients already connected, so
// they need not poll.
//
// It subscribes to notification.inbox.created on the event bus and delivers
// each event to the connections subscribed to that event's (tenant, recipient)
// scope. The inbox row is committed before the event is published, so a push
// that reaches nobody loses nothing: the row is already in the table and a
// client reads it back. The Hub holds only this replica's connections, in
// memory; it stores nothing, guarantees no delivery and does not cross
// replicas. Its only value is latency.
//
// The module registers notification.stream, delivers no capability of its own,
// and mounts GET /stream (a server-sent-event stream) on the entry point's
// registration surface under the base path /api/v1/notifications.
package stream

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

// ModuleName is this module's name in the registry. It is also the configuration
// namespace its input items hang from and the name a host writes into the
// command that mounts the stream route.
const ModuleName = "notification.stream"

// ConfigNamespace is where this module's input items are mounted. It is this
// module's own name, as it is for every registering module.
const ConfigNamespace = ModuleName

// defaultEndpoint is the listening endpoint this module declares its OpenAPI
// fragment for. The endpoint's name belongs to the host's HTTP configuration, and
// a resource is a static declaration written before any configuration is read, so
// the fragment carries this conventional name rather than an endpoint a run
// happens to configure; the routes a run actually mounts follow the endpoints the
// module is told to (notification.stream.endpoints).
const defaultEndpoint = "api"

//go:embed openapi/stream.json
var streamFragment []byte

// eventInboxCreated is the type name this module subscribes to. It is the value
// of the inbox module's EventInboxCreated constant, spelled out here because a
// subscriber cannot name a publisher's type without importing the publisher and
// inverting the dependency; the bus matches a subscription by an exact string
// comparison and interprets no prefix, so one literal is enough.
const eventInboxCreated = "notification.inbox.created"

// The configuration item this module accepts, relative to its namespace, and
// the full path a failure names.
const (
	endpointsItem = "endpoints"
	endpointsKey  = ConfigNamespace + "." + endpointsItem
)

// allEndpoints is the name that stands for every endpoint in an endpoint list.
// This module's list refuses it: mounting the stream route on every endpoint,
// the public ones included, would put it where no gateway guards it.
const allEndpoints = "*"

// init registers this module with the process registry. Registration travels
// with the import, which is what makes "does this command serve the stream
// route" a question the host answers by importing this package and writing
// ModuleName into the command.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The event bus is required: with no bus there is no event to subscribe to and
// the module does nothing, so an assembly that takes this one in without a bus
// fails resolution rather than serving a route no event will ever fill.
//
// The HTTP entry point and the logging capability are optional. The route is
// mounted only when an entry point is present, and the logger is the optional
// diagnostic requirement. This module resolves neither the gateway nor any other
// capability the request path passes through: where the stream route sits
// relative to a gateway is the host's assembly, not a line in this descriptor.
//
// The module delivers no capability: nothing resolves it and no data plane asks
// it a question, so it leaves only GET /stream behind. Its stance is
// StateEnabled and never StateAuto — whether to serve the stream route is a
// decision on the command tree, not a conclusion a configuration infers.
//
// The descriptor also declares this module's own http.Spec, carrying the OpenAPI
// fragment that describes GET /api/v1/notifications/stream; the operations module
// merges it into the document a process serves.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*event.Bus)(nil)},
			{Token: (*speedhttp.Router)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Resources: []any{
			schema(),
			speedhttp.Spec{Endpoint: defaultEndpoint, Document: streamFragment},
			// The real-time push surface: its route is mounted whenever the
			// module is in force and the process has an entry point, so the
			// feature is static.
			feature.Spec{Name: "notification.stream"},
		},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
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

		// Stop ends every open stream, so the entry point's drain does not have
		// to wait out its timeout on a long-lived connection that would otherwise
		// never end. It is safe on an instance that was never initialised.
		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			if p, ok := productOf(instance); ok {
				p.hub.closeAll()
			}
			return nil
		},

		Close: func(context.Context, *core.Registry, any) error { return nil },
	}
}

// productOf narrows an instance to this module's product. An instance of
// another shape is one the host supplied for this module, such as a stand-in
// built for a test, and the callbacks leave it alone.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// product is the module's instance: the per-replica Hub, the bus it subscribes
// on, and the endpoint list the route is mounted by.
type product struct {
	hub    *Hub
	bus    event.Bus
	logger *slog.Logger
	cfg    resolved
}

// readerOf takes up the configuration reader. A reader that is not there means
// the host never supplied one as the instance of the config module, which is
// the host's omission rather than something to read as "nothing was
// configured".
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	return reader, nil
}

// newProduct takes up the bus and the configuration and builds the product. It
// reads nothing and subscribes nothing: the subscription is made in Init, where
// every instance exists.
func newProduct(reg *core.Registry) (*product, error) {
	bus, err := core.Resolve[event.Bus](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the event bus: %w", ModuleName, err)
	}
	reader, err := readerOf(reg)
	if err != nil {
		return nil, err
	}
	cfg, err := readConfig(reader)
	if err != nil {
		return nil, err
	}
	return &product{
		hub:    &Hub{},
		bus:    bus,
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
// logging capability, subscribes to notification.inbox.created, and mounts the
// stream route when an entry point is present.
//
// Subscribing here and not in New is what the bus needs: every module
// subscribes in its own Init, and the bus freezes its table at the stage after
// them, so a subscription made in New would be one made before the table is
// ready to hold it.
//
// The endpoint list is judged here and only here: a run that assembles an entry
// point has to name the endpoints the stream route is mounted on, while a run
// that assembles none mounts nothing and reads the list nowhere.
func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
	}

	p.subscribe()

	router, found, err := optional[speedhttp.Router](reg, "the HTTP entry point")
	if err != nil {
		return err
	}
	if found {
		if err := validateEndpoints(p.cfg.endpoints); err != nil {
			return err
		}
		if err := p.mount(router); err != nil {
			return err
		}
		p.diagnose()
	}
	return nil
}

// subscribe registers the module's handler for notification.inbox.created. The
// handler drops what it cannot read: the Hub's Publish never reports an error,
// so there is nothing to hand back to the bus.
func (p *product) subscribe() {
	p.bus.Subscribe(eventInboxCreated, ModuleName, func(_ context.Context, evt event.Event) error {
		p.hub.Publish(evt)
		return nil
	})
}

// diagnose writes the start diagnostic: the endpoints the stream route was
// mounted on. The line is the module's own, so a reader finds what the push
// module mounted without scanning a chain listing.
func (p *product) diagnose() {
	p.logger.Info(ModuleName+": the stream route is mounted", "endpoints", p.cfg.endpoints)
}

// readConfig decodes this module's section. Prepare and New both go through it,
// so the two see one configuration.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve(), nil
}

// schema declares this module's input items. The namespace is this module's own
// name: two modules declaring one path is a startup failure even when only one
// of them runs.
//
// The endpoint list takes the primary source alone. It decides where the stream
// route is mounted, so an environment variable or a command-line argument that
// could rewrite it would be a way to move the route onto an endpoint from
// outside the configuration file.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			endpointsItem: {
				Origins: config.OriginPrimary,
				Description: "the listening endpoints the stream route is mounted on, by the names " +
					"the entry point's own configuration gives them; concrete names only, never *. " +
					"Required of a run that mounts the route, and unread by a run that assembles no " +
					"entry point",
			},
		},
	}
}

// configDefaults is what this module is worth without a configuration file.
//
// There is no default for the endpoints: a list that mounted the stream route
// nowhere would silently drop it, and guessing which endpoints should carry it
// is a guess about what is exposed. A run that mounts the route and cannot name
// an endpoint fails the startup, naming the key.
var configDefaults = moduleConfig{}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Endpoints names the listening endpoints the stream route is mounted on, by
	// the entry point's own names. The list is a set of concrete names: the name
	// that stands for every endpoint is not accepted.
	Endpoints []string `config:"endpoints"`
}

// resolved is the configuration this module runs on.
type resolved struct {
	endpoints []string
}

// resolve turns decoded configuration into what the module runs on. An empty
// list is legal here: the list says where the route is mounted, so a run that
// assembles no entry point reads it nowhere, and the judgement belongs to
// validateEndpoints, which runs only where the mounting happens.
func (c moduleConfig) resolve() resolved { return resolved{endpoints: c.Endpoints} }

// validateEndpoints refuses an endpoint list the route cannot be mounted on: an
// empty one, an empty name, the name that stands for every endpoint, or a name
// written twice.
//
// It runs in Init, where the entry point is in hand, and is called only when one
// is present. A run that mounts the route has to name the endpoints; being
// refused here is what keeps it from mounting nothing silently. The module has
// no failure sentinel of its own, so a shape it cannot run on is the
// capability's notification.ErrInvalidConfig.
func validateEndpoints(names []string) error {
	if len(names) == 0 {
		return fmt.Errorf("%w: %s is empty. The stream route is mounted on the endpoints this module "+
			"is told to and on none by default; list the endpoints that should carry it",
			notification.ErrInvalidConfig, endpointsKey)
	}
	seen := make(map[string]struct{}, len(names))
	for i, name := range names {
		switch name {
		case "":
			return fmt.Errorf("%w: %s holds an empty name at position %d",
				notification.ErrInvalidConfig, endpointsKey, i)
		case allEndpoints:
			return fmt.Errorf("%w: %s holds %q at position %d. This module takes concrete "+
				"endpoint names only: mounting the stream route on every endpoint would put it on the "+
				"public ones, where no gateway guards it",
				notification.ErrInvalidConfig, endpointsKey, allEndpoints, i)
		}
		// The list is a set. The entry point holds one endpoint per name, so a
		// name written twice would register the route on that endpoint twice,
		// which the route engine refuses as a conflict.
		if _, twice := seen[name]; twice {
			return fmt.Errorf("%w: %s names the endpoint %q twice, which would register "+
				"the stream route on it twice", notification.ErrInvalidConfig, endpointsKey, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// tenantOf reads the tenant the request resolved to. The resolving layer writes
// it into the context; a request that carries none is scoped to the empty
// tenant, and the route does not judge it — who may reach the route is the
// host's decision.
func tenantOf(ctx context.Context) string {
	id, _ := tenant.FromContext(ctx)
	return string(id)
}
