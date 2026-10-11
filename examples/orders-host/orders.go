package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	nethttp "net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// ordersModuleName is the name this host's application module is registered
// under.
const ordersModuleName = "orders"

// The route paths, in gin's spelling. The listing is guarded and reads a scoped
// table; the placement is guarded too, shares the listing's path and is the write
// a case watches the bus and the journal for; the status route sits on the
// guarded endpoint and is exempted from the credential by the module that owns
// it, which is the only way a route is reached without one; the health check is
// on the endpoint neither guard covers.
//
// The exemption and the journal's request entries name a route by its method and
// this spelling, and a request is compared with them character for character, so
// the path a route is registered under is the path they are written with.
const (
	healthPath = "/health"
	statusPath = "/status"
	ordersPath = "/orders"
)

// orderTable is the table the scoped model maps to.
const orderTable = "orders"

// placedEventType is the type name an order is published under. It is agreed
// between this module and whoever subscribes, with nothing checking the two
// ends against each other.
const placedEventType = "order.placed"

// The two settings this module declares. They are here together because they
// are read back through different layers: a value someone writes is read where
// it was written, and a value nobody writes is read at the declaration.
const (
	pageSizeSetting  = ordersModuleName + ".page-size"
	retentionSetting = ordersModuleName + ".retention-days"
)

// What a read answers with when no layer above the declaration holds a value.
const (
	defaultPageSize      = 20
	defaultRetentionDays = 30
)

// The records a module writes as it is released. They are messages rather than
// free text so that a case finds them by name, and where the two modules'
// records appear is what shows the shutdown order: no stop before the entry
// point has answered what it admitted, the stops in reverse construction order,
// and the closes after every stop in that order again.
const (
	stopMsg  = "stop"
	closeMsg = "close"
)

// Order is a scoped model: it embeds the convention, which is both the
// declaration and the column. Everything else about it is an ordinary GORM
// model, and no query in this program names a tenant.
type Order struct {
	tenant.Model
	ID   string `gorm:"column:id;primaryKey"`
	Item string `gorm:"column:item;not null"`
}

// TableName fixes the table this model maps to.
func (Order) TableName() string { return orderTable }

//go:embed migrations
var migrationRoot embed.FS

// migrationsFS drops the wrapping directory, so the tree the database module
// reads has one directory per dialect at its root.
var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		panic("orders-host: the embedded migration tree: " + err.Error())
	}
	return sub
}()

// orderSettings is the carrier of this module's declared setting items. A
// field's value is the declared default, and its tag is the key it is read
// under.
type orderSettings struct {
	PageSize      int `config:"page-size"`
	RetentionDays int `config:"retention-days"`
}

// settingsSchema declares what this module reads from the settings surface.
//
// The declaration is a resource, so it is collected whether or not this module
// is constructed: the command that reaches this module for its declarations
// alone still contributes the keys, which is what keeps the key table complete
// for a run that never constructs this module.
func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ordersModuleName,
		Mounts: []settings.Mount{{Value: &orderSettings{
			PageSize:      defaultPageSize,
			RetentionDays: defaultRetentionDays,
		}}},
		Items: map[string]settings.Item{
			"page-size": {
				Description: "how many orders one listing returns",
			},
			"retention-days": {
				Description: "how long an order is kept before it may be pruned",
			},
		},
	}
}

// init registers this host's application module.
func init() { core.ProcessRegistry.Register(ordersModule()) }

// ordersModule binds the routes, declares the table and declares the settings.
//
// The logging requirement is declared and the rest are not. A logger is a
// module's product, so a module that takes one has to say so and be ordered
// around the module that makes it — in both directions, which is what lets this
// module's release records still reach the configured destinations. The entry
// point, the gateway, the roster, the database, the settings surface and the
// bus are taken up in Init instead, where every instance exists already: none
// of the six is taken up at construction, so there is no order to arrange
// around them. Declaring one would buy a failure that arrives at resolution,
// naming the capability, rather than in this module's Init — and nothing on the
// provider side. All six are delivered exclusively, and the closure never
// expands a required capability into an exclusive provider, so a declaration
// could not bring one into the command: which providers a command runs stays
// this host's choice to write into the command tree.
func ordersModule() core.Module {
	return core.Module{
		Name: ordersModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
		},
		Resources: []any{
			db.Migrations{FS: migrationsFS},
			settingsSchema(),
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := core.Resolve[log.Logger](reg)
			if err != nil {
				return nil, err
			}
			return &ordersProduct{log: logger.Named(ordersModuleName)}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			public, err := router.Endpoint(publicEndpoint)
			if err != nil {
				return err
			}
			ops, err := router.Endpoint(opsEndpoint)
			if err != nil {
				return err
			}
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return err
			}
			set, err := core.Resolve[settings.Store](reg)
			if err != nil {
				return err
			}
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return err
			}
			gateway, err := core.Resolve[authn.Gate](reg)
			if err != nil {
				return err
			}

			public.Route(nethttp.MethodGet, ordersPath, listOrders(database, set))
			public.Route(nethttp.MethodPost, ordersPath, placeOrder(database, bus))
			ops.Route(nethttp.MethodGet, healthPath, reportHealth)

			// The status route sits on the guarded endpoint and is reached
			// without a credential, because the module that owns the route
			// says so through the gateway's own surface. The registration
			// is frozen when the gateway starts, so a route registered
			// after that is not exempt from anything and nothing reports
			// the difference — which is why the two lines live together
			// here rather than in whichever file happens to be read first.
			//
			// It is not exempt from resolution: resolution has no per-route
			// exception, so this route still needs a tenant name. The health
			// check is the one that needs neither, and it is on the endpoint
			// neither guard covers rather than exempted from both.
			public.Route(nethttp.MethodGet, statusPath, reportStatus)
			gateway.Exempt(publicEndpoint, nethttp.MethodGet, statusPath)
			return nil
		},

		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			ordersOf(instance).log.Info(stopMsg)
			return nil
		},

		Close: func(_ context.Context, _ *core.Registry, instance any) error {
			ordersOf(instance).log.Info(closeMsg)
			return nil
		},
	}
}

// ordersProduct is what this module's New returns: the logger its release
// records go through.
type ordersProduct struct {
	log *slog.Logger
}

// ordersOf narrows an instance to this module's product. An instance of another
// shape is one a host supplied for this module, which the registry accepts for a
// module that declares no capability and hands to the callbacks as it hands the
// product. This host supplies none for it, so one is a defect in whatever did,
// and the narrowing panics on it with the type named.
func ordersOf(instance any) *ordersProduct {
	//nolint:errcheck // the doc comment above says why the panic is the report.
	return instance.(*ordersProduct)
}

// health is what the operations route answers with.
type health struct {
	Status string `json:"status"`
}

// listing is what the guarded listing answers with. The caller and the tenant
// are echoed back so that a case can see which of each reached the handler, and
// the two settings so that it can see which layer answered for each. The caller
// is the identity's accountability key: the account of whoever authenticated,
// which for the subject table is the static prefix and the subject's name, as in
// static.alice.
type listing struct {
	Caller        string   `json:"caller"`
	Tenant        string   `json:"tenant"`
	PageSize      int      `json:"page_size"`
	RetentionDays int      `json:"retention_days"`
	Items         []string `json:"items"`
}

// placement is what the guarded write answers with.
type placement struct {
	ID string `json:"id"`
}

// reportHealth answers the route on the endpoint neither guard covers. It reads
// no identity and no tenant, because on this endpoint there are none.
func reportHealth(c *gin.Context) {
	c.JSON(nethttp.StatusOK, health{Status: "ok"})
}

// reportStatus answers the exempted route. It needs no credential and does need
// a tenant, which is what tells the two guards' reach apart: the exemption is
// about one of them and neither has a per-route exception for the other.
func reportStatus(c *gin.Context) {
	id, ok := tenant.FromContext(c.Request.Context())
	if !ok {
		c.Status(nethttp.StatusInternalServerError)
		return
	}
	c.JSON(nethttp.StatusOK, health{Status: "ok: " + string(id)})
}

// listOrders answers the guarded listing.
//
// The query carries no tenant condition, and that is the point: the rows that
// come back are this tenant's because the filter put the condition there. The
// two values it echoes come from the settings surface, one written and one
// never written, so the same response shows both a written layer and the
// declaration.
func listOrders(database db.Database, set settings.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		caller, ok := authn.FromContext(ctx)
		if !ok {
			// Unreachable behind the gateway, and worth failing loudly
			// rather than answering as though the caller were anonymous: a
			// handler on a guarded route that finds no identity has been
			// reached by a path that skipped authentication.
			log.FromContext(ctx).Error("a guarded route was reached with no identity",
				"route", c.FullPath())
			c.Status(nethttp.StatusInternalServerError)
			return
		}
		id, ok := tenant.FromContext(ctx)
		if !ok {
			log.FromContext(ctx).Error("a guarded route was reached with no tenant",
				"route", c.FullPath())
			c.Status(nethttp.StatusInternalServerError)
			return
		}
		// The read falls back tenant row, global row, declared default, and
		// this call site names the tenant so that a case can watch which
		// layer answered.
		layer := settings.ForTenant(string(id))
		limit, err := settings.Get[int](set, layer, pageSizeSetting)
		if err != nil {
			log.FromContext(ctx).Error("reading the page size", "error", err)
			c.Status(nethttp.StatusInternalServerError)
			return
		}
		retention, err := settings.Get[int](set, layer, retentionSetting)
		if err != nil {
			log.FromContext(ctx).Error("reading the retention", "error", err)
			c.Status(nethttp.StatusInternalServerError)
			return
		}
		var found []Order
		if err := database.DB().WithContext(ctx).Order("id").Limit(limit).Find(&found).Error; err != nil {
			log.FromContext(ctx).Error("reading the orders", "error", err)
			c.Status(nethttp.StatusInternalServerError)
			return
		}
		items := make([]string, 0, len(found))
		for _, o := range found {
			items = append(items, o.Item)
		}
		c.JSON(nethttp.StatusOK, listing{
			// The key is the account of the one who authenticated, which is
			// also what rate limiting counts by and the audit trail records.
			Caller:        caller.Key(),
			Tenant:        string(id),
			PageSize:      limit,
			RetentionDays: retention,
			Items:         items,
		})
	}
}

// placeOrder answers the guarded write: it stores the order and publishes it.
//
// Publishing is what makes this module a producer that knows nothing about its
// consumers. The bus it publishes on is the capability, not an implementation,
// and whether anything is subscribed at all is not a question this call site can
// ask.
func placeOrder(database db.Database, bus event.Bus) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		id, ok := tenant.FromContext(ctx)
		if !ok {
			log.FromContext(ctx).Error("a guarded route was reached with no tenant",
				"route", c.FullPath())
			c.Status(nethttp.StatusInternalServerError)
			return
		}
		var body struct {
			ID   string `json:"id"`
			Item string `json:"item"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.Status(bindFailureStatus(err))
			return
		}
		order := Order{Model: tenant.Model{TenantID: id}, ID: body.ID, Item: body.Item}
		if err := database.DB().WithContext(ctx).Create(&order).Error; err != nil {
			log.FromContext(ctx).Error("writing the order", "error", err)
			c.Status(nethttp.StatusInternalServerError)
			return
		}
		if err := bus.Publish(ctx, event.Event{
			Type:     placedEventType,
			TenantID: string(id),
			Payload:  map[string]string{"id": body.ID, "item": body.Item},
		}); err != nil {
			log.FromContext(ctx).Error("publishing the order", "error", err)
			c.Status(nethttp.StatusInternalServerError)
			return
		}
		c.JSON(nethttp.StatusCreated, placement{ID: body.ID})
	}
}

// bindFailureStatus is the status a body that could not be bound is answered
// with. A body the endpoint's limit cut off is too large, and every other body
// that does not read as the JSON object the route takes — empty, malformed, a
// member of the wrong type — is a bad request.
func bindFailureStatus(err error) int {
	var tooLarge *nethttp.MaxBytesError
	if errors.As(err, &tooLarge) {
		return nethttp.StatusRequestEntityTooLarge
	}
	return nethttp.StatusBadRequest
}
