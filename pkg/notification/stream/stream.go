// Package stream is the notification capability's real-time push module:
// it pushes "the inbox just gained a row" to the clients already
// connected, so they need not poll.
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

const defaultEndpoint = "api"

// streamFragment is this module's OpenAPI fragment, read at build time. It is
// generated from operations, not written by hand.
//
//go:embed openapi/stream.json
var streamFragment []byte

var streamSpec = speedhttp.Spec{
	Endpoint: defaultEndpoint,
	Prefix:   "api",
	Version:  "v1",
	Document: streamFragment,
}

const eventInboxCreated = "notification.inbox.created"

const (
	endpointsItem = "endpoints"
	endpointsKey  = ConfigNamespace + "." + endpointsItem
)

// allEndpoints is the name that stands for every endpoint in an endpoint list.
// This module's list refuses it: mounting the stream route on every endpoint,
// the public ones included, would put it where no gateway guards it.
const allEndpoints = "*"

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
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
			streamSpec,
			operations,
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

func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

type product struct {
	hub    *Hub
	bus    event.Bus
	logger *slog.Logger
	cfg    resolved
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the configuration reader: %w", ModuleName, err)
	}
	return reader, nil
}

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

// wire runs in Init, where every instance exists.
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

func (p *product) diagnose() {
	p.logger.Info(ModuleName+": the stream route is mounted", "endpoints", p.cfg.endpoints)
}

func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve(), nil
}

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

var configDefaults = moduleConfig{}

type moduleConfig struct {
	// Endpoints names the listening endpoints the stream route is mounted on, by
	// the entry point's own names. The list is a set of concrete names: the name
	// that stands for every endpoint is not accepted.
	Endpoints []string `config:"endpoints"`
}

type resolved struct {
	endpoints []string
}

func (c moduleConfig) resolve() resolved { return resolved{endpoints: c.Endpoints} }

// validateEndpoints refuses an endpoint list the route cannot be mounted
// on: an empty one, an empty name, the name that stands for every
// endpoint, or a name written twice.
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

func tenantOf(ctx context.Context) string {
	id, _ := tenant.FromContext(ctx)
	return string(id)
}
