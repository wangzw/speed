package http

import (
	"context"
	"errors"
	nethttp "net/http"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
)

// backendConfig is one listener named backend with the named endpoints hanging
// on it, all on a loopback port the operating system picks.
func backendConfig(names ...string) moduleConfig {
	cfg := moduleConfig{
		Listeners: map[string]listenerConfig{"backend": {Address: "127.0.0.1:0"}},
		Endpoints: make(map[string]endpointConfig, len(names)),
	}
	for _, name := range names {
		cfg.Endpoints[name] = endpointConfig{Listener: "backend"}
	}
	return cfg
}

// TestOneListenerServesTwoEndpointsOverOneSocket pins the capability the
// listener layer adds: two endpoints hanging on one listener share one socket,
// and a request routed to either is answered over that one socket. It is the
// case the older shape could not express at all, where an endpoint was a
// socket.
func TestOneListenerServesTwoEndpointsOverOneSocket(t *testing.T) {
	serving := make(chan struct{}, 1)
	var (
		mu       sync.Mutex
		captured *router
	)
	probe := &stageProbe{
		name:     "probe.registers-two-endpoints",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("api", nethttp.MethodGet, "/api/things", func(c *gin.Context) { c.String(nethttp.StatusOK, "api") }),
			routeOn("web", nethttp.MethodGet, "/*path", func(c *gin.Context) { c.String(nethttp.StatusOK, "web") }),
		),
		onServe: func(reg *core.Registry) error {
			r, err := routerFrom(reg)
			if err != nil {
				return err
			}
			mu.Lock()
			captured = r
			mu.Unlock()
			serving <- struct{}{}
			return nil
		},
	}

	a := startAssembly(t, backendConfig("api", "web"), Module(), probe.module())
	a.waitForOrEnd(t, serving, "the listener to be served")

	mu.Lock()
	r := captured
	mu.Unlock()
	if r == nil {
		t.Fatal("the probe never captured the router")
	}

	apiAddr, err := boundAddr(r, "api")
	if err != nil {
		t.Fatalf("the api endpoint bound no address: %v", err)
	}
	webAddr, err := boundAddr(r, "web")
	if err != nil {
		t.Fatalf("the web endpoint bound no address: %v", err)
	}
	if apiAddr != webAddr {
		t.Fatalf("the two endpoints on one listener bound different addresses, %q and %q; "+
			"one listener is one socket", apiAddr, webAddr)
	}

	// Both endpoints are reached over the same socket: the api route hits the
	// api endpoint's chain, and a path no endpoint registered falls to the web
	// endpoint, whose catch-all covers it.
	api, err := getResponse(context.Background(), apiAddr, "/api/things")
	if err != nil {
		t.Fatalf("the request for the api route failed: %v", err)
	}
	if api.status != nethttp.StatusOK || api.body != "api" {
		t.Errorf("the api route answered %d %q, want 200 %q", api.status, api.body, "api")
	}

	web, err := getResponse(context.Background(), apiAddr, "/some/page")
	if err != nil {
		t.Fatalf("the request for the fallback route failed: %v", err)
	}
	if web.status != nethttp.StatusOK || web.body != "web" {
		t.Errorf("the fallback route answered %d %q, want 200 %q", web.status, web.body, "web")
	}
}

// TestTwoCatchAllEndpointsOnOneListenerIsStartupFailure pins the one-endpoint
// rule for the fallback: a listener hands every request no endpoint registered
// to its one fallback endpoint, so a second catch-all has no request it could
// ever be reached by, and the startup ends naming the listener and the two
// endpoints.
func TestTwoCatchAllEndpointsOnOneListenerIsStartupFailure(t *testing.T) {
	probe := &stageProbe{
		name:     "probe.registers-two-catch-alls",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("a", nethttp.MethodGet, "/*path", filler),
			routeOn("b", nethttp.MethodGet, "/*rest", filler),
		),
	}
	a := startAssembly(t, backendConfig("a", "b"), Module(), probe.module())

	runErr := a.stop(t)
	if !errors.Is(runErr, ErrRouteConflict) {
		t.Fatalf("two catch-all endpoints on one listener started up with %v, want ErrRouteConflict", runErr)
	}
	mustContain(t, runErr.Error(), `"backend"`, "the listener carrying both catch-alls")
	mustContain(t, runErr.Error(), `"a"`, "the first catch-all endpoint")
	mustContain(t, runErr.Error(), `"b"`, "the second catch-all endpoint")
}

// TestTwoEndpointsOnARouteOnOneListenerIsStartupFailure pins that one (method,
// path) belongs to one endpoint: two endpoints on one listener claiming the same
// route is a startup failure naming the listener and the two endpoints, because
// the dispatch could not tell which chain the request belongs to.
func TestTwoEndpointsOnARouteOnOneListenerIsStartupFailure(t *testing.T) {
	probe := &stageProbe{
		name:     "probe.registers-one-route-twice",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("a", nethttp.MethodGet, "/things", filler),
			routeOn("b", nethttp.MethodGet, "/things", filler),
		),
	}
	a := startAssembly(t, backendConfig("a", "b"), Module(), probe.module())

	runErr := a.stop(t)
	if !errors.Is(runErr, ErrRouteConflict) {
		t.Fatalf("two endpoints claiming one route started up with %v, want ErrRouteConflict", runErr)
	}
	mustContain(t, runErr.Error(), `"backend"`, "the listener carrying both endpoints")
	mustContain(t, runErr.Error(), `"a"`, "the first endpoint on the route")
	mustContain(t, runErr.Error(), `"b"`, "the second endpoint on the route")
	mustContain(t, runErr.Error(), "/things", "the route both claimed")
}
