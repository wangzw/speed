package main

import (
	"context"
	nethttp "net/http"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// ordersModuleName is the name this host's application module is registered
// under.
const ordersModuleName = "orders"

// endpointName is the listening endpoint this module binds its routes on. It
// is the name the entry point's configuration gives the endpoint, and the same
// name this host writes into the gateway's guarded set.
const endpointName = "public"

// The two routes. One is public and one is not, which is the whole of what
// this example has to show on the routing side.
const (
	healthRoute = "GET /health"
	ordersRoute = "GET /orders"
)

// init registers this host's application module.
func init() { core.ProcessRegistry.Register(ordersModule()) }

// ordersModule binds the routes and opens the public one.
//
// It declares no Requires. The entry point and the gateway are both taken up in
// Init, where every instance exists, and a requirement would make this module's
// presence in a command drag in whichever provider happens to be registered —
// which is this host's choice to write into the command tree.
func ordersModule() core.Module {
	return core.Module{
		Name: ordersModuleName,
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			endpoint, err := router.Endpoint(endpointName)
			if err != nil {
				return err
			}
			endpoint.Route(healthRoute, nethttp.HandlerFunc(reportHealth))
			endpoint.Route(ordersRoute, nethttp.HandlerFunc(listOrders))

			// The health check is opened through the gateway's own
			// surface rather than by leaving it off a guarded
			// endpoint: which routes are public is the registering
			// module's to say, and saying it here keeps the route and
			// its exemption in one place.
			gate, err := core.Resolve[authn.Gate](reg)
			if err != nil {
				return err
			}
			gate.Exempt(endpointName, healthRoute)
			return nil
		},
	}
}

// health is what the public route answers with.
type health struct {
	Status string `json:"status"`
}

// orders is what the guarded route answers with. The caller is echoed back so
// that a case can see which identity reached the handler, qualification and
// all.
type orders struct {
	Caller string   `json:"caller"`
	Items  []string `json:"items"`
}

// reportHealth answers the public route. It reads no identity, because there
// is none: an exempt request reaches no backend and nothing stamps one.
func reportHealth(w nethttp.ResponseWriter, _ *nethttp.Request) {
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, health{Status: "ok"})
}

// listOrders answers the guarded route.
//
// The identity is taken from the context and not from anything the request
// carries: by the time a handler runs, who the caller is has been settled once,
// by the gateway, and re-reading the Authorization header here would be a
// second authentication decision made somewhere nobody is looking.
func listOrders(w nethttp.ResponseWriter, r *nethttp.Request) {
	caller, ok := authn.FromContext(r.Context())
	if !ok {
		// Unreachable behind the gateway, and worth failing loudly
		// rather than answering as though the caller were anonymous: a
		// handler on a guarded route that finds no identity has been
		// reached by a path that skipped authentication.
		log.FromContext(r.Context()).Error("a guarded route was reached with no identity",
			"route", ordersRoute)
		w.WriteHeader(nethttp.StatusInternalServerError)
		return
	}
	_ = speedhttp.WriteJSON(w, nethttp.StatusOK, orders{
		Caller: caller.Key(),
		Items:  []string{"a widget", "a sprocket"},
	})
}
