package main

import (
	"context"
	"embed"
	"io/fs"
	nethttp "net/http"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// ordersModuleName is the name this host's application module is registered
// under.
const ordersModuleName = "orders"

// The routes. The guarded one reads a scoped table; the public one reads
// nothing at all.
const (
	healthRoute = "GET /health"
	ordersRoute = "GET /orders"
)

// orderTable is the table the scoped model maps to.
const orderTable = "orders"

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
		panic("tenanted-host: the embedded migration tree: " + err.Error())
	}
	return sub
}()

// init registers this host's application module.
func init() { core.ProcessRegistry.Register(ordersModule()) }

// ordersModule binds the routes and declares the table.
//
// It declares no Requires. The entry point, the gateway, the roster and the
// database are all taken up in Init, where every instance exists, and a
// requirement would make this module's presence in a command drag in whichever
// provider happens to be registered — which is this host's choice to write into
// the command tree.
//
// The migration set is a resource, so a command that reaches this module for
// its declarations alone collects it. That is how the migrate command builds
// this table without constructing this module or putting its input items on
// that command's line.
func ordersModule() core.Module {
	return core.Module{
		Name:      ordersModuleName,
		Resources: []any{db.Migrations{FS: migrationsFS}},

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
			public.Route(ordersRoute, listOrders(database))
			ops.Route(healthRoute, nethttp.HandlerFunc(reportHealth))

			// The health check is registered on the endpoint neither
			// guard covers, and nothing is exempted. Resolution has no
			// per-route exception, so a route that needs no tenant is
			// reached on an endpoint that resolves none — and since
			// the gateway does not guard that endpoint either, there is
			// nothing to exempt it from. Exempting it on the public
			// endpoint would name a route that is not there, which is
			// the dangling-exemption shape the authentication module
			// documents and does not report.
			return nil
		},
	}
}

// health is what the public route answers with.
type health struct {
	Status string `json:"status"`
}

// orders is what the guarded route answers with. The caller and the tenant are
// echoed back so that a case can see which of each reached the handler.
type orders struct {
	Caller string   `json:"caller"`
	Tenant string   `json:"tenant"`
	Items  []string `json:"items"`
}

// reportHealth answers the route on the endpoint neither guard covers. It reads
// no identity and no tenant, because on this endpoint there are none.
func reportHealth(w nethttp.ResponseWriter, _ *nethttp.Request) {
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, health{Status: "ok"})
}

// listOrders answers the guarded route.
//
// The query carries no condition, and that is the point: the rows that come
// back are this tenant's because the filter put the condition there. The
// handler reads the tenant only to echo it back, which is what a case needs to
// see.
func listOrders(database db.Database) nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		ctx := r.Context()
		caller, ok := authn.FromContext(ctx)
		if !ok {
			// Unreachable behind the gateway, and worth failing
			// loudly rather than answering as though the caller were
			// anonymous: a handler on a guarded route that finds no
			// identity has been reached by a path that skipped
			// authentication.
			log.FromContext(ctx).Error("a guarded route was reached with no identity",
				"route", ordersRoute)
			w.WriteHeader(nethttp.StatusInternalServerError)
			return
		}
		id, ok := tenant.FromContext(ctx)
		if !ok {
			log.FromContext(ctx).Error("a guarded route was reached with no tenant",
				"route", ordersRoute)
			w.WriteHeader(nethttp.StatusInternalServerError)
			return
		}
		var found []Order
		if err := database.DB().WithContext(ctx).Order("id").Find(&found).Error; err != nil {
			log.FromContext(ctx).Error("reading the orders", "error", err)
			w.WriteHeader(nethttp.StatusInternalServerError)
			return
		}
		items := make([]string, 0, len(found))
		for _, o := range found {
			items = append(items, o.Item)
		}
		_ = speedhttp.WriteJSON(w, nethttp.StatusOK, orders{
			Caller: caller.Key(),
			Tenant: string(id),
			Items:  items,
		})
	}
}
