package main

import (
	"context"
	"log/slog"
	nethttp "net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// inventoryModuleName is the name this host's second application module is
// registered under.
const inventoryModuleName = "inventory"

// inventoryPath is the path of the guarded route that reports the counter.
const inventoryPath = "/inventory"

// placedCounter is the key the placement count lives under. The capability
// stores opaque bytes under an opaque key, so the spelling of this name is the
// caller's alone and the module interprets none of it.
const placedCounter = "orders:placed"

// init registers this host's second application module.
func init() { core.ProcessRegistry.Register(inventoryModule()) }

// inventoryModule is the consumer of two capabilities at once: it counts what
// was placed, and it learns about placements by subscribing rather than by
// being called.
//
// It declares both requirements, and neither declaration is the way a provider
// arrives: both backends deliver their capability exclusively, and the closure
// expands a required capability into its declared providers except the ones
// that claim it exclusively. What the declarations do is state what this module
// takes up — ordering it after the modules that deliver it, and failing the
// startup at resolution when the run names no provider for one of them. The run
// that wants a counter is the one that names the two backends in its command
// tree.
//
// The logging requirement orders this module around the logger it writes
// through, and it is declared for the same reason the other two are: this
// module takes a logger out of the registry and nothing else would say so.
func inventoryModule() core.Module {
	return core.Module{
		Name: inventoryModuleName,
		Requires: []core.Requirement{
			{Token: (*kv.KV)(nil)},
			{Token: (*event.Bus)(nil)},
			{Token: (*log.Logger)(nil)},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := core.Resolve[log.Logger](reg)
			if err != nil {
				return nil, err
			}
			return &inventoryProduct{log: logger.Named(inventoryModuleName)}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product := inventoryOf(instance)
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			public, err := router.Endpoint(publicEndpoint)
			if err != nil {
				return err
			}
			store, err := core.Resolve[kv.KV](reg)
			if err != nil {
				return err
			}
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return err
			}

			// The subscription belongs to this stage and to no other: a bus
			// freezes its subscription table when it starts, so a subscriber
			// that arrived later would miss every event published before it
			// and nothing would report the gap.
			bus.Subscribe(placedEventType, countPlacement(store, product.log))
			public.Route(nethttp.MethodGet, inventoryPath, reportCount(store))
			return nil
		},

		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			inventoryOf(instance).log.Info(stopMsg)
			return nil
		},

		Close: func(_ context.Context, _ *core.Registry, instance any) error {
			inventoryOf(instance).log.Info(closeMsg)
			return nil
		},
	}
}

// inventoryProduct is what this module's New returns: the logger its release
// records go through.
type inventoryProduct struct {
	log *slog.Logger
}

// inventoryOf narrows an instance to this module's product. An instance of
// another shape is one a host supplied for this module, which the registry
// accepts for a module that declares no capability and hands to the callbacks as
// it hands the product. This host supplies none for it, so one is a defect in
// whatever did, and the narrowing panics on it with the type named.
func inventoryOf(instance any) *inventoryProduct {
	//nolint:errcheck // the doc comment above says why the panic is the report.
	return instance.(*inventoryProduct)
}

// inventory is what the guarded route answers with.
type inventory struct {
	Tenant string  `json:"tenant"`
	Placed float64 `json:"placed"`
}

// countPlacement is the subscriber: it adds one to the counter for every
// placement it is handed.
//
// It reads the payload nowhere, because it has no use for it — a subscriber
// that only counts does not have to agree with its publisher about the shape of
// what was published, which is the point of a bus that carries an opaque
// payload. A counter that needs the value would read it structurally, by field
// name, rather than by importing the publisher's type.
func countPlacement(store kv.KV, logger *slog.Logger) event.Handler {
	return func(ctx context.Context, evt event.Event) error {
		total, err := store.IncrByFloat(ctx, placedCounter, 1)
		if err != nil {
			return err
		}
		logger.Info("counted", "type", evt.Type, "total", total)
		return nil
	}
}

// reportCount answers the route with what the counter holds.
//
// A key that was never written counts as zero rather than as an error, so a run
// that placed nothing answers zero — which is the shape this route has before
// its first placement, and not a case to special-case.
func reportCount(store kv.KV) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		id, ok := tenant.FromContext(ctx)
		if !ok {
			log.FromContext(ctx).Error("a guarded route was reached with no tenant",
				"route", c.FullPath())
			c.Status(nethttp.StatusInternalServerError)
			return
		}
		raw, present, err := store.Get(ctx, placedCounter)
		if err != nil {
			log.FromContext(ctx).Error("reading the counter", "error", err)
			c.Status(nethttp.StatusInternalServerError)
			return
		}
		var total float64
		if present {
			// The stored form is the shortest exact decimal encoding of the
			// number, so it is parsed rather than compared as text.
			total, err = strconv.ParseFloat(string(raw), 64)
			if err != nil {
				log.FromContext(ctx).Error("parsing the counter", "error", err)
				c.Status(nethttp.StatusInternalServerError)
				return
			}
		}
		c.JSON(nethttp.StatusOK, inventory{Tenant: string(id), Placed: total})
	}
}
