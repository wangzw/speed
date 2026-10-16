package http

import (
	"context"
	"errors"
	"io"
	nethttp "net/http"
	"strings"
	"sync"
	"sync/atomic"
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

// TestARouteShadowedByTheFallbackIsStillAStartupFailure pins that the
// one-route-one-endpoint rule reaches the fallback endpoint too. The fallback is
// left out of prefix ownership, and the prefix owning the path answers it, so a
// route the fallback registers beside another endpoint's is not routed by the
// listener at all; without the fallback in the duplicate check the startup
// succeeds and the shadowed route is silently unreachable. The check therefore
// covers every endpoint, and the failure names the listener, the two endpoints
// and the route.
func TestARouteShadowedByTheFallbackIsStillAStartupFailure(t *testing.T) {
	probe := &stageProbe{
		name:     "probe.registers-a-route-the-fallback-shadows",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("web", nethttp.MethodGet, "/*path", filler),
			routeOn("web", nethttp.MethodPost, "/api/x", filler),
			routeOn("api", nethttp.MethodPost, "/api/x", filler),
		),
	}
	a := startAssembly(t, backendConfig("api", "web"), Module(), probe.module())

	runErr := a.stop(t)
	if !errors.Is(runErr, ErrRouteConflict) {
		t.Fatalf("a route the fallback endpoint also registered started up with %v, want ErrRouteConflict", runErr)
	}
	mustContain(t, runErr.Error(), `"backend"`, "the listener carrying both endpoints")
	mustContain(t, runErr.Error(), `"web"`, "the fallback endpoint")
	mustContain(t, runErr.Error(), `"api"`, "the endpoint whose route was shadowed")
	mustContain(t, runErr.Error(), "/api/x", "the route both registered")
}

// TestAMethodNotAllowedRequestRunsTheOwningEndpointsChain pins that a request
// whose path an endpoint registered but whose method it does not allow is handed
// to that endpoint rather than answered by the dispatch. On one listener the
// endpoint's own chain has to run: a guard layer's CORS preflight is the request
// that depends on it, and a dispatch that answers 405 itself would leave the
// browser without the CORS headers on the preflight. A request that is not the
// preflight is still answered 405 with the Allow header, through the same chain.
func TestAMethodNotAllowedRequestRunsTheOwningEndpointsChain(t *testing.T) {
	var guardCalls int32
	guard := func(c *gin.Context) {
		atomic.AddInt32(&guardCalls, 1)
		if c.Request.Method == nethttp.MethodOptions && c.GetHeader("Access-Control-Request-Method") != "" {
			c.Header("Access-Control-Allow-Origin", "*")
			c.AbortWithStatus(nethttp.StatusNoContent)
			return
		}
		c.Next()
	}
	serving := make(chan struct{}, 1)
	probe := &stageProbe{
		name:     "probe.registers-api-and-its-fallback",
		requires: requiresRouter(),
		onInit: inOrder(
			useLayer("api", Middleware{Name: "api.preflight", Stage: StageGuard, Handler: guard}),
			routeOn("api", nethttp.MethodGet, "/api/x", func(c *gin.Context) { c.String(nethttp.StatusOK, "api") }),
			routeOn("web", nethttp.MethodGet, "/*path", func(c *gin.Context) { c.String(nethttp.StatusOK, "web") }),
		),
		onServe: func(*core.Registry) error {
			serving <- struct{}{}
			return nil
		},
	}

	a := startAssembly(t, backendConfig("api", "web"), Module(), probe.module())
	a.waitForOrEnd(t, serving, "the listener to be served")
	r, err := routerFrom(a.reg)
	if err != nil {
		t.Fatalf("taking this module's product out of the registry: %v", err)
	}
	addr, err := boundAddr(r, "api")
	if err != nil {
		t.Fatalf("finding the address the listener bound: %v", err)
	}

	// The registered route runs the guard once and answers from the route.
	got, err := getResponse(context.Background(), addr, "/api/x")
	if err != nil {
		t.Fatalf("the request for the api route failed: %v", err)
	}
	if got.status != nethttp.StatusOK || got.body != "api" {
		t.Errorf("the api route answered %d %q, want 200 %q", got.status, got.body, "api")
	}
	if calls := atomic.LoadInt32(&guardCalls); calls != 1 {
		t.Errorf("the guard ran %d times on the registered route, want 1", calls)
	}

	// A CORS preflight names a method the path does not register (OPTIONS), so
	// gin routes it as method-not-allowed: the request must reach the api
	// endpoint's own chain, where the guard answers it with the CORS headers.
	preflight := nethttp.Header{}
	preflight.Set("Origin", "https://example.test")
	preflight.Set("Access-Control-Request-Method", nethttp.MethodGet)
	got, err = exchange(context.Background(), nethttp.MethodOptions, addr, "/api/x", preflight)
	if err != nil {
		t.Fatalf("the preflight failed: %v", err)
	}
	if got.status != nethttp.StatusNoContent {
		t.Errorf("the preflight answered %d, want %d; the guard layer on the api endpoint did not run",
			got.status, nethttp.StatusNoContent)
	}
	if allow := got.header.Get("Access-Control-Allow-Origin"); allow != "*" {
		t.Errorf("the preflight carries Access-Control-Allow-Origin %q, want %q", allow, "*")
	}
	if calls := atomic.LoadInt32(&guardCalls); calls != 2 {
		t.Errorf("the guard ran %d times after the registered route and the preflight, want 2", calls)
	}

	// A disallowed method that is not a preflight is still answered 405 with the
	// Allow header, and it too reaches the guard: the endpoint's own engine
	// answers it after the chain has run.
	got, err = exchange(context.Background(), nethttp.MethodPost, addr, "/api/x", nil)
	if err != nil {
		t.Fatalf("the POST request failed: %v", err)
	}
	if got.status != nethttp.StatusMethodNotAllowed {
		t.Errorf("a disallowed method answered %d, want %d", got.status, nethttp.StatusMethodNotAllowed)
	}
	if allow := got.header.Get("Allow"); allow != "GET" {
		t.Errorf("the 405 carries Allow %q, want %q", allow, "GET")
	}
	if calls := atomic.LoadInt32(&guardCalls); calls != 3 {
		t.Errorf("the guard ran %d times after the third request, want 3", calls)
	}
}

// TestAPathUnderAnEndpointsPrefixIsAnsweredByThatEndpoint pins the selector a
// shared listener routes by: a request path that falls under an endpoint's
// static prefix is answered by that endpoint — its engine runs its chain and
// writes its own 404 — even when no route registered the path, and only a path
// under no endpoint's prefix reaches the fallback. Without it the web bundle's
// catch-all, the fallback, answers a mistyped API path with 200 text/html, and a
// client or a monitor reads a misspelling as a success.
func TestAPathUnderAnEndpointsPrefixIsAnsweredByThatEndpoint(t *testing.T) {
	serving := make(chan struct{}, 1)
	probe := &stageProbe{
		name:     "probe.registers-an-api-prefix-and-a-web-fallback",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("api", nethttp.MethodGet, "/api/v1/things", func(c *gin.Context) { c.String(nethttp.StatusOK, "api") }),
			routeOn("web", nethttp.MethodGet, "/*path", func(c *gin.Context) {
				c.Data(nethttp.StatusOK, "text/html; charset=utf-8", []byte("<html>index</html>"))
			}),
		),
		onServe: func(*core.Registry) error {
			serving <- struct{}{}
			return nil
		},
	}
	a := startAssembly(t, backendConfig("api", "web"), Module(), probe.module())
	a.waitForOrEnd(t, serving, "the listener to be served")
	r, err := routerFrom(a.reg)
	if err != nil {
		t.Fatalf("taking this module's product out of the registry: %v", err)
	}
	addr, err := boundAddr(r, "api")
	if err != nil {
		t.Fatalf("finding the address the listener bound: %v", err)
	}

	// A registered route still reaches its handler through the api endpoint.
	got, err := getResponse(context.Background(), addr, "/api/v1/things")
	if err != nil {
		t.Fatalf("the request for the registered route failed: %v", err)
	}
	if got.status != nethttp.StatusOK || got.body != "api" {
		t.Errorf("the registered route answered %d %q, want 200 %q", got.status, got.body, "api")
	}

	// A path under the api prefix that no route registered is answered by api in
	// api's own shape — a problem+json 404 — not swallowed by the web fallback.
	nope, err := getResponse(context.Background(), addr, "/api/v1/nope")
	if err != nil {
		t.Fatalf("the request for the unregistered api path failed: %v", err)
	}
	if nope.status != nethttp.StatusNotFound {
		t.Errorf("a path under the api prefix answered %d, want %d; the web fallback swallowed it",
			nope.status, nethttp.StatusNotFound)
	}
	if ct := nope.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("the api 404 carries Content-Type %q, want application/problem+json", ct)
	}
	if strings.Contains(nope.body, "index") {
		t.Errorf("the unregistered api path was served the web bundle: %q", nope.body)
	}

	// A path under no endpoint's prefix goes to the fallback, the web bundle.
	for _, path := range []string{"/console/tenants", "/"} {
		web, err := getResponse(context.Background(), addr, path)
		if err != nil {
			t.Fatalf("the request for %q failed: %v", path, err)
		}
		if web.status != nethttp.StatusOK || !strings.Contains(web.body, "index") {
			t.Errorf("%q answered %d %q, want the web bundle (200 with index)", path, web.status, web.body)
		}
	}
}

// TestTwoEndpointsWithTheSameStaticPrefixIsStartupFailure pins that one static
// prefix belongs to one endpoint: the listener selects an endpoint by the
// longest prefix a request falls under, so two endpoints owning the same prefix
// leave the request's endpoint undecided, and the startup ends naming the
// listener, the two endpoints and the prefix.
func TestTwoEndpointsWithTheSameStaticPrefixIsStartupFailure(t *testing.T) {
	probe := &stageProbe{
		name:     "probe.registers-two-endpoints-on-one-prefix",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("a", nethttp.MethodGet, "/api/v1/things", filler),
			routeOn("b", nethttp.MethodGet, "/api/v1/other", filler),
		),
	}
	a := startAssembly(t, backendConfig("a", "b"), Module(), probe.module())

	runErr := a.stop(t)
	if !errors.Is(runErr, ErrRouteConflict) {
		t.Fatalf("two endpoints on one static prefix started up with %v, want ErrRouteConflict", runErr)
	}
	mustContain(t, runErr.Error(), `"backend"`, "the listener carrying both endpoints")
	mustContain(t, runErr.Error(), `"a"`, "the first endpoint on the prefix")
	mustContain(t, runErr.Error(), `"b"`, "the second endpoint on the prefix")
	mustContain(t, runErr.Error(), "/api/v1", "the prefix both owned")
}

// TestNestedStaticPrefixesPickTheLongestMatch pins that nested prefixes are not a
// collision: the endpoint owning the longer prefix answers a path under it, and
// the endpoint owning the shorter one answers the rest of its subtree, including
// a path no route registered, since the longer prefix does not cover it.
func TestNestedStaticPrefixesPickTheLongestMatch(t *testing.T) {
	serving := make(chan struct{}, 1)
	probe := &stageProbe{
		name:     "probe.registers-nested-prefixes",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("outer", nethttp.MethodGet, "/api/v1/things", func(c *gin.Context) { c.String(nethttp.StatusOK, "outer") }),
			routeOn("inner", nethttp.MethodGet, "/api/v1/internal/things", func(c *gin.Context) { c.String(nethttp.StatusOK, "inner") }),
		),
		onServe: func(*core.Registry) error {
			serving <- struct{}{}
			return nil
		},
	}
	a := startAssembly(t, backendConfig("outer", "inner"), Module(), probe.module())
	a.waitForOrEnd(t, serving, "the listener to be served")
	r, err := routerFrom(a.reg)
	if err != nil {
		t.Fatalf("taking this module's product out of the registry: %v", err)
	}
	addr, err := boundAddr(r, "outer")
	if err != nil {
		t.Fatalf("finding the address the listener bound: %v", err)
	}

	// The longer prefix wins for a path under it.
	inner, err := getResponse(context.Background(), addr, "/api/v1/internal/things")
	if err != nil {
		t.Fatalf("the request for the inner route failed: %v", err)
	}
	if inner.status != nethttp.StatusOK || inner.body != "inner" {
		t.Errorf("/api/v1/internal/things answered %d %q, want 200 %q (the inner endpoint)",
			inner.status, inner.body, "inner")
	}

	// The shorter prefix answers the rest of its subtree. Its registered route
	// answers normally; a path it owns but does not register gets the outer
	// endpoint's own 404, not the inner endpoint's and not a fallback (there is
	// none).
	for _, tc := range []struct {
		path   string
		status int
		body   string
	}{
		{"/api/v1/things", nethttp.StatusOK, "outer"},
		{"/api/v1/other", nethttp.StatusNotFound, ""},
	} {
		got, err := getResponse(context.Background(), addr, tc.path)
		if err != nil {
			t.Fatalf("the request for %q failed: %v", tc.path, err)
		}
		if got.status != tc.status || (tc.body != "" && got.body != tc.body) {
			t.Errorf("%q answered %d %q, want %d %q", tc.path, got.status, got.body, tc.status, tc.body)
		}
	}
}

// TestAnEndpointWithNoStaticPrefixBesideOthersIsStartupFailure pins the other
// half of prefix selection: an endpoint whose routes all begin with a dynamic
// segment owns no prefix, so a listener carrying it beside another endpoint has
// no path to select it by, and the startup ends naming the listener and the
// endpoint. Alone on a listener the same endpoint is the fallback and carries
// every request, so this is only a failure when it shares the socket.
func TestAnEndpointWithNoStaticPrefixBesideOthersIsStartupFailure(t *testing.T) {
	probe := &stageProbe{
		name:     "probe.registers-a-root-dynamic-endpoint",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("a", nethttp.MethodGet, "/:tenant/things", filler),
			routeOn("b", nethttp.MethodGet, "/api/v1/things", filler),
		),
	}
	a := startAssembly(t, backendConfig("a", "b"), Module(), probe.module())

	runErr := a.stop(t)
	if !errors.Is(runErr, ErrRouteConflict) {
		t.Fatalf("an endpoint with no static prefix beside another started up with %v, want ErrRouteConflict", runErr)
	}
	mustContain(t, runErr.Error(), `"backend"`, "the listener carrying both endpoints")
	mustContain(t, runErr.Error(), `"a"`, "the endpoint with no static prefix")
}

// TestRoutePrefix pins the static prefix a route contributes to its endpoint's
// ownership: the literal segments it leads with, up to the first dynamic
// segment, with a fully literal route of two or more segments contributing its
// parent so the endpoint also answers paths beside its own routes. A route whose
// first segment is already dynamic contributes nothing.
func TestRoutePrefix(t *testing.T) {
	cases := []struct {
		path string
		want string
		ok   bool
	}{
		{"/api/v1/objects", "/api/v1", true},
		{"/api/v1/objects/:id", "/api/v1/objects", true},
		{"/api/v1/notifications/messages/:messageId/read", "/api/v1/notifications/messages", true},
		{"/auth/login", "/auth", true},
		{"/tenant/admin/tenants/:id/status", "/tenant/admin/tenants", true},
		{"/health", "/health", true},
		{"/", "/", true},
		{"/:tenant/things", "", false},
		{"/*path", "", false},
	}
	for _, tc := range cases {
		got, ok := routePrefix(tc.path)
		if ok != tc.ok || got != tc.want {
			t.Errorf("routePrefix(%q) = %q, %v; want %q, %v", tc.path, got, ok, tc.want, tc.ok)
		}
	}
}

// exchange issues one request of the given method, with the given headers,
// against a bound address and reports what the server answered. getResponse
// covers the GET case; a method-not-allowed test needs the method and the
// preflight headers a plain GET does not carry.
func exchange(ctx context.Context, method, addr, path string, header nethttp.Header) (answer, error) {
	req, err := nethttp.NewRequestWithContext(ctx, method, "http://"+addr+path, nil)
	if err != nil {
		return answer{}, err
	}
	for name, values := range header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	client := &nethttp.Client{
		Timeout:   waitDeadline,
		Transport: &nethttp.Transport{DisableKeepAlives: true, DisableCompression: true},
	}
	resp, err := client.Do(req)
	if err != nil {
		return answer{}, err
	}
	defer resp.Body.Close()
	body, bodyErr := io.ReadAll(resp.Body)
	return answer{status: resp.StatusCode, header: resp.Header, body: string(body), bodyErr: bodyErr}, nil
}
