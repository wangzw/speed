package http

import (
	"context"
	"errors"
	"io"
	"log/slog"
	nethttp "net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// backendConfig is one listener named backend with the named endpoints hanging
// on it, all on a loopback port the operating system picks. None of them writes
// prefixes, so every one claims "/": this is the configuration two endpoints on
// one listener cannot have, and the tests that want a claim conflict start from
// it.
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

// claimedConfig is one listener named backend with each endpoint's claimed
// prefixes given explicitly, on a loopback port the operating system picks.
func claimedConfig(claims map[string][]string) moduleConfig {
	cfg := moduleConfig{
		Listeners: map[string]listenerConfig{"backend": {Address: "127.0.0.1:0"}},
		Endpoints: make(map[string]endpointConfig, len(claims)),
	}
	for name, prefixes := range claims {
		cfg.Endpoints[name] = endpointConfig{Listener: "backend", Prefixes: prefixes}
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

	a := startAssembly(t, claimedConfig(map[string][]string{"api": {"/api"}, "web": {"/"}}),
		Module(), probe.module())
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
	// api endpoint's chain, and a path no endpoint claims falls to the web
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

// TestTwoEndpointsOmittingPrefixesIsAClaimConflict pins the configuration
// failure two endpoints on one listener share: omitting prefixes claims "/",
// and "/" at most one endpoint may claim. The failure comes from reading
// configuration, before any chain is assembled, and its text tells the host to
// give each endpoint its own prefixes.
func TestTwoEndpointsOmittingPrefixesIsAClaimConflict(t *testing.T) {
	a := startAssembly(t, backendConfig("a", "b"), Module())

	runErr := a.stop(t)
	if !errors.Is(runErr, ErrClaimConflict) {
		t.Fatalf("two endpoints that both omitted prefixes started up with %v, want ErrClaimConflict", runErr)
	}
	mustContain(t, runErr.Error(), `"backend"`, "the listener carrying both endpoints")
	mustContain(t, runErr.Error(), `"a"`, "the first endpoint claiming \"/\"")
	mustContain(t, runErr.Error(), `"b"`, "the second endpoint claiming \"/\"")
	mustContain(t, runErr.Error(), "prefixes", "the key the failure tells the host to write")
}

// TestTwoEndpointsWithTheSameStaticPrefixIsAClaimConflict pins that one prefix
// belongs to one endpoint: the listener selects an endpoint by the longest
// prefix a request falls under, so two endpoints claiming the same prefix leave
// the request's endpoint undecided. It is a configuration failure, naming the
// listener, both endpoints and the prefix.
func TestTwoEndpointsWithTheSameStaticPrefixIsAClaimConflict(t *testing.T) {
	probe := &stageProbe{
		name:     "probe.registers-two-endpoints-on-one-prefix",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("a", nethttp.MethodGet, "/api/v1/things", filler),
			routeOn("b", nethttp.MethodGet, "/api/v1/other", filler),
		),
	}
	a := startAssembly(t,
		claimedConfig(map[string][]string{"a": {"/api/v1"}, "b": {"/api/v1"}}),
		Module(), probe.module())

	runErr := a.stop(t)
	if !errors.Is(runErr, ErrClaimConflict) {
		t.Fatalf("two endpoints claiming one prefix started up with %v, want ErrClaimConflict", runErr)
	}
	mustContain(t, runErr.Error(), `"backend"`, "the listener carrying both endpoints")
	mustContain(t, runErr.Error(), `"a"`, "the first endpoint on the prefix")
	mustContain(t, runErr.Error(), `"b"`, "the second endpoint on the prefix")
	mustContain(t, runErr.Error(), "/api/v1", "the prefix both claimed")
}

// TestTwoEndpointsOnARouteOnOneListenerIsStartupFailure pins that selection is
// by configuration: a route registered on an endpoint whose claim does not
// select it is mounted but unreachable, so the startup ends with
// ErrUnclaimedRoute naming the listener, the route and the endpoint that would
// answer it instead.
func TestTwoEndpointsOnARouteOnOneListenerIsStartupFailure(t *testing.T) {
	probe := &stageProbe{
		name:     "probe.registers-one-route-twice",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("a", nethttp.MethodGet, "/things", filler),
			routeOn("b", nethttp.MethodGet, "/things", filler),
		),
	}
	a := startAssembly(t,
		claimedConfig(map[string][]string{"a": {"/things"}, "b": {"/other"}}),
		Module(), probe.module())

	runErr := a.stop(t)
	if !errors.Is(runErr, ErrUnclaimedRoute) {
		t.Fatalf("two endpoints claiming one route started up with %v, want ErrUnclaimedRoute", runErr)
	}
	mustContain(t, runErr.Error(), `"backend"`, "the listener carrying both endpoints")
	mustContain(t, runErr.Error(), `"b"`, "the endpoint whose route is unclaimed")
	mustContain(t, runErr.Error(), `"a"`, "the endpoint its claim selects")
	mustContain(t, runErr.Error(), "/things", "the route both claimed")
}

// TestARouteShadowedByTheFallbackIsStillAStartupFailure pins that the claim
// check reaches the fallback endpoint too. A literal route the fallback
// registers under another endpoint's claim is answered by that other endpoint,
// so it is unreachable; the failure names the listener, the registering
// endpoint and the endpoint and prefix that would answer it.
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
	a := startAssembly(t,
		claimedConfig(map[string][]string{"api": {"/api"}, "web": {"/"}}),
		Module(), probe.module())

	runErr := a.stop(t)
	if !errors.Is(runErr, ErrUnclaimedRoute) {
		t.Fatalf("a route the fallback endpoint also registered started up with %v, want ErrUnclaimedRoute", runErr)
	}
	mustContain(t, runErr.Error(), `"backend"`, "the listener carrying both endpoints")
	mustContain(t, runErr.Error(), `"web"`, "the fallback endpoint that registered the route")
	mustContain(t, runErr.Error(), `"api"`, "the endpoint whose claim answers it")
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

	a := startAssembly(t,
		claimedConfig(map[string][]string{"api": {"/api"}, "web": {"/"}}),
		Module(), probe.module())
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
// claimed prefix is answered by that endpoint — its engine runs its chain and
// writes its own 404 — even when no route registered the path, and only a path
// under no endpoint's claim reaches the fallback. Without it the web bundle's
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
	a := startAssembly(t,
		claimedConfig(map[string][]string{"api": {"/api"}, "web": {"/"}}),
		Module(), probe.module())
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

	// A path under the api claim that no route registered is answered by api in
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

	// A path under no endpoint's claim goes to the fallback, the web bundle.
	for _, path := range []string{"/admin/tenants", "/"} {
		web, err := getResponse(context.Background(), addr, path)
		if err != nil {
			t.Fatalf("the request for %q failed: %v", path, err)
		}
		if web.status != nethttp.StatusOK || !strings.Contains(web.body, "index") {
			t.Errorf("%q answered %d %q, want the web bundle (200 with index)", path, web.status, web.body)
		}
	}
}

// TestAPathNoEndpointOwnsWithNoFallbackIsTheDispatchsOwn404 pins the dispatch's
// own answer when a listener carries no endpoint claiming "/": a path under no
// endpoint's claim is answered by the dispatch itself, as a problem+json 404,
// rather than by any endpoint. Each endpoint's chain sets a marker header, and
// the answer carries neither, which is what shows the dispatch wrote it and no
// endpoint's chain ran.
func TestAPathNoEndpointOwnsWithNoFallbackIsTheDispatchsOwn404(t *testing.T) {
	serving := make(chan struct{}, 1)
	mark := func(name string) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Header("X-Endpoint-Saw", name)
			c.Next()
		}
	}
	probe := &stageProbe{
		name:     "probe.registers-two-disjoint-endpoints",
		requires: requiresRouter(),
		onInit: inOrder(
			useLayer("a", Middleware{Name: "a.marker", Stage: StageGuard, Handler: mark("a")}),
			useLayer("b", Middleware{Name: "b.marker", Stage: StageGuard, Handler: mark("b")}),
			routeOn("a", nethttp.MethodGet, "/api/v1/things", filler),
			routeOn("b", nethttp.MethodGet, "/other/things", filler),
		),
		onServe: func(*core.Registry) error {
			serving <- struct{}{}
			return nil
		},
	}
	a := startAssembly(t,
		claimedConfig(map[string][]string{"a": {"/api/v1"}, "b": {"/other"}}),
		Module(), probe.module())
	a.waitForOrEnd(t, serving, "the listener to be served")
	r, err := routerFrom(a.reg)
	if err != nil {
		t.Fatalf("taking this module's product out of the registry: %v", err)
	}
	addr, err := boundAddr(r, "a")
	if err != nil {
		t.Fatalf("finding the address the listener bound: %v", err)
	}

	got, err := getResponse(context.Background(), addr, "/nowhere")
	if err != nil {
		t.Fatalf("the request for the unowned path failed: %v", err)
	}
	if got.status != nethttp.StatusNotFound {
		t.Errorf("a path under no endpoint's claim answered %d, want %d", got.status, nethttp.StatusNotFound)
	}
	if ct := got.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("the dispatch's 404 carries Content-Type %q, want application/problem+json", ct)
	}
	if saw := got.header.Get("X-Endpoint-Saw"); saw != "" {
		t.Errorf("the answer carries the endpoint marker %q; the request reached an endpoint's chain, not the dispatch", saw)
	}
}

// TestNestedClaimsPickTheLongestMatch pins that nested claims are not a
// collision: the endpoint claiming the longer prefix answers a path under it,
// and the endpoint claiming the shorter one answers the rest of its subtree,
// including a path no route registered, since the longer claim does not cover it.
func TestNestedClaimsPickTheLongestMatch(t *testing.T) {
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
	a := startAssembly(t,
		claimedConfig(map[string][]string{"outer": {"/api"}, "inner": {"/api/v1/internal"}}),
		Module(), probe.module())
	a.waitForOrEnd(t, serving, "the listener to be served")
	r, err := routerFrom(a.reg)
	if err != nil {
		t.Fatalf("taking this module's product out of the registry: %v", err)
	}
	addr, err := boundAddr(r, "outer")
	if err != nil {
		t.Fatalf("finding the address the listener bound: %v", err)
	}

	// The longer claim wins for a path under it.
	inner, err := getResponse(context.Background(), addr, "/api/v1/internal/things")
	if err != nil {
		t.Fatalf("the request for the inner route failed: %v", err)
	}
	if inner.status != nethttp.StatusOK || inner.body != "inner" {
		t.Errorf("/api/v1/internal/things answered %d %q, want 200 %q (the inner endpoint)",
			inner.status, inner.body, "inner")
	}

	// The shorter claim answers the rest of its subtree. Its registered route
	// answers normally; a path it claims but does not register gets the outer
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

// TestACatchAllBesideARootClaimIsUnclaimed pins that a catch-all is selected by
// its literal leading path, which is empty: only the endpoint claiming "/" can
// register one. An endpoint claiming /web and registering /*path would never be
// selected, so the startup ends with ErrUnclaimedRoute naming both the
// registering endpoint and the fallback that answers it.
func TestACatchAllBesideARootClaimIsUnclaimed(t *testing.T) {
	probe := &stageProbe{
		name:     "probe.registers-a-root-and-a-catch-all",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("root", nethttp.MethodGet, "/", filler),
			routeOn("web", nethttp.MethodGet, "/*path", filler),
		),
	}
	a := startAssembly(t,
		claimedConfig(map[string][]string{"root": {"/"}, "web": {"/web"}}),
		Module(), probe.module())

	runErr := a.stop(t)
	if !errors.Is(runErr, ErrUnclaimedRoute) {
		t.Fatalf("a catch-all beside a root claim started up with %v, want ErrUnclaimedRoute", runErr)
	}
	mustContain(t, runErr.Error(), `"backend"`, "the listener carrying both endpoints")
	mustContain(t, runErr.Error(), `"web"`, "the endpoint registering the catch-all")
	mustContain(t, runErr.Error(), `"root"`, "the endpoint claiming \"/\" that answers it")
	mustContain(t, runErr.Error(), "/web", "the claim of the endpoint registering the catch-all")
}

// TestAnEndpointClaimingTheRootIsTheListenersFallback pins how a listener
// treats an endpoint that claims "/": it is the fallback, the endpoint that
// answers the paths no other endpoint claims. Beside an endpoint claiming a
// deeper prefix the two coexist: the deeper claim wins for a path under it, and
// the root endpoint answers the rest, including "/" itself.
func TestAnEndpointClaimingTheRootIsTheListenersFallback(t *testing.T) {
	serving := make(chan struct{}, 1)
	probe := &stageProbe{
		name:     "probe.registers-a-root-endpoint-beside-an-api",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("root", nethttp.MethodGet, "/", func(c *gin.Context) { c.String(nethttp.StatusOK, "root") }),
			routeOn("api", nethttp.MethodGet, "/api/v1/things", func(c *gin.Context) { c.String(nethttp.StatusOK, "api") }),
		),
		onServe: func(*core.Registry) error {
			serving <- struct{}{}
			return nil
		},
	}
	a := startAssembly(t,
		claimedConfig(map[string][]string{"root": {"/"}, "api": {"/api"}}),
		Module(), probe.module())
	a.waitForOrEnd(t, serving, "the listener to be served")
	r, err := routerFrom(a.reg)
	if err != nil {
		t.Fatalf("taking this module's product out of the registry: %v", err)
	}
	addr, err := boundAddr(r, "root")
	if err != nil {
		t.Fatalf("finding the address the listener bound: %v", err)
	}

	for _, tc := range []struct {
		path string
		want string
	}{
		{"/api/v1/things", "api"},
		{"/", "root"},
	} {
		got, err := getResponse(context.Background(), addr, tc.path)
		if err != nil {
			t.Fatalf("the request for %q failed: %v", tc.path, err)
		}
		if got.status != nethttp.StatusOK || got.body != tc.want {
			t.Errorf("%q answered %d %q, want 200 %q", tc.path, got.status, got.body, tc.want)
		}
	}
}

// TestAZeroRouteEndpointOwnsItsClaim pins the property that made claims
// configuration: an endpoint with no routes starts up and answers the paths its
// claim selects, running its own chain and writing its own 404. Without it an
// endpoint whose modules are not running would have to leave the listener, and
// the paths under its claim would drift to the fallback with the assembly.
func TestAZeroRouteEndpointOwnsItsClaim(t *testing.T) {
	serving := make(chan struct{}, 1)
	mark := func(c *gin.Context) {
		c.Header("X-Endpoint-Saw", "e")
		c.Next()
	}
	probe := &stageProbe{
		name:     "probe.registers-a-zero-route-endpoint",
		requires: requiresRouter(),
		onInit: inOrder(
			useLayer("e", Middleware{Name: "e.marker", Stage: StageGuard, Handler: mark}),
			routeOn("f", nethttp.MethodGet, "/*path", func(c *gin.Context) { c.String(nethttp.StatusOK, "fallback") }),
		),
		onServe: func(*core.Registry) error {
			serving <- struct{}{}
			return nil
		},
	}
	a := startAssembly(t,
		claimedConfig(map[string][]string{"e": {"/x"}, "f": {"/"}}),
		Module(), probe.module())
	a.waitForOrEnd(t, serving, "the listener to be served")
	r, err := routerFrom(a.reg)
	if err != nil {
		t.Fatalf("taking this module's product out of the registry: %v", err)
	}
	addr, err := boundAddr(r, "e")
	if err != nil {
		t.Fatalf("finding the address the listener bound: %v", err)
	}

	got, err := getResponse(context.Background(), addr, "/x/y")
	if err != nil {
		t.Fatalf("the request under the zero-route endpoint's claim failed: %v", err)
	}
	if got.status != nethttp.StatusNotFound {
		t.Errorf("a path under the zero-route endpoint answered %d, want its own 404", got.status)
	}
	if ct := got.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("the endpoint's 404 carries Content-Type %q, want application/problem+json", ct)
	}
	if saw := got.header.Get("X-Endpoint-Saw"); saw != "e" {
		t.Errorf("the answer carries the marker %q, want %q: the fallback answered instead of the "+
			"endpoint that claims the path", saw, "e")
	}
	if strings.Contains(got.body, "fallback") {
		t.Errorf("the fallback answered a path under another endpoint's claim: %q", got.body)
	}
}

// TestAPathUnderAClaimIsAnsweredByThatEndpointEvenWithoutARoute pins that
// selection reads the claim and not the routes: an endpoint claiming /api
// answers /api/v1/objects with its own 404 even though the only route it
// registered is /api/v1/notifications/m. The record module that registers the
// other routes may be absent, and the answer stays the same.
func TestAPathUnderAClaimIsAnsweredByThatEndpointEvenWithoutARoute(t *testing.T) {
	serving := make(chan struct{}, 1)
	mark := func(c *gin.Context) {
		c.Header("X-Endpoint-Saw", "e")
		c.Next()
	}
	probe := &stageProbe{
		name:     "probe.registers-one-deep-route",
		requires: requiresRouter(),
		onInit: inOrder(
			useLayer("e", Middleware{Name: "e.marker", Stage: StageGuard, Handler: mark}),
			routeOn("e", nethttp.MethodGet, "/api/v1/notifications/m",
				func(c *gin.Context) { c.String(nethttp.StatusOK, "e") }),
			routeOn("f", nethttp.MethodGet, "/*path", func(c *gin.Context) { c.String(nethttp.StatusOK, "fallback") }),
		),
		onServe: func(*core.Registry) error {
			serving <- struct{}{}
			return nil
		},
	}
	a := startAssembly(t,
		claimedConfig(map[string][]string{"e": {"/api"}, "f": {"/"}}),
		Module(), probe.module())
	a.waitForOrEnd(t, serving, "the listener to be served")
	r, err := routerFrom(a.reg)
	if err != nil {
		t.Fatalf("taking this module's product out of the registry: %v", err)
	}
	addr, err := boundAddr(r, "e")
	if err != nil {
		t.Fatalf("finding the address the listener bound: %v", err)
	}

	got, err := getResponse(context.Background(), addr, "/api/v1/objects")
	if err != nil {
		t.Fatalf("the request for an unregistered path under the claim failed: %v", err)
	}
	if got.status != nethttp.StatusNotFound {
		t.Errorf("a path under the claim answered %d, want the endpoint's own 404", got.status)
	}
	if saw := got.header.Get("X-Endpoint-Saw"); saw != "e" {
		t.Errorf("the answer carries the marker %q, want %q: the fallback answered instead", saw, "e")
	}
}

// TestADeepCatchAllUnderItsOwnClaimStarts pins that a wildcard is selected by
// its literal leading path: /files/*p leads with /files, so an endpoint
// claiming /files may register it beside an endpoint claiming "/". It is not a
// second fallback.
func TestADeepCatchAllUnderItsOwnClaimStarts(t *testing.T) {
	serving := make(chan struct{}, 1)
	probe := &stageProbe{
		name:     "probe.registers-a-deep-catch-all",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("files", nethttp.MethodGet, "/files/*p",
				func(c *gin.Context) { c.String(nethttp.StatusOK, "files:"+c.Param("p")) }),
			routeOn("web", nethttp.MethodGet, "/*path", func(c *gin.Context) { c.String(nethttp.StatusOK, "web") }),
		),
		onServe: func(*core.Registry) error {
			serving <- struct{}{}
			return nil
		},
	}
	a := startAssembly(t,
		claimedConfig(map[string][]string{"files": {"/files"}, "web": {"/"}}),
		Module(), probe.module())
	a.waitForOrEnd(t, serving, "the listener to be served")
	r, err := routerFrom(a.reg)
	if err != nil {
		t.Fatalf("taking this module's product out of the registry: %v", err)
	}
	addr, err := boundAddr(r, "files")
	if err != nil {
		t.Fatalf("finding the address the listener bound: %v", err)
	}

	got, err := getResponse(context.Background(), addr, "/files/a/b")
	if err != nil {
		t.Fatalf("the request for the deep wildcard failed: %v", err)
	}
	if got.status != nethttp.StatusOK || got.body != "files:/a/b" {
		t.Errorf("/files/a/b answered %d %q, want 200 %q", got.status, got.body, "files:/a/b")
	}

	other, err := getResponse(context.Background(), addr, "/other")
	if err != nil {
		t.Fatalf("the request for the fallback failed: %v", err)
	}
	if other.status != nethttp.StatusOK || other.body != "web" {
		t.Errorf("/other answered %d %q, want 200 %q", other.status, other.body, "web")
	}
}

// TestAWildcardOnAPrefixedEndpointIsUnclaimed pins the failing side of the
// literal leading path: a catch-all leads with nothing, so only the endpoint
// claiming "/" can register one. An endpoint claiming /api and registering
// /*path is refused at startup with ErrUnclaimedRoute, naming the route, the
// registering endpoint and the fallback that would answer it.
func TestAWildcardOnAPrefixedEndpointIsUnclaimed(t *testing.T) {
	probe := &stageProbe{
		name:     "probe.registers-a-wildcard-on-a-prefixed-endpoint",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("e", nethttp.MethodGet, "/*path", filler),
			routeOn("f", nethttp.MethodGet, "/*rest", filler),
		),
	}
	a := startAssembly(t,
		claimedConfig(map[string][]string{"e": {"/api"}, "f": {"/"}}),
		Module(), probe.module())

	runErr := a.stop(t)
	if !errors.Is(runErr, ErrUnclaimedRoute) {
		t.Fatalf("a wildcard on a prefixed endpoint started up with %v, want ErrUnclaimedRoute", runErr)
	}
	mustContain(t, runErr.Error(), `"backend"`, "the listener")
	mustContain(t, runErr.Error(), `"e"`, "the endpoint registering the wildcard")
	mustContain(t, runErr.Error(), "GET /*path", "the route that is unclaimed")
	mustContain(t, runErr.Error(), `"f"`, "the fallback that would answer it")
}

// TestARouteTakenByALongerClaimIsUnclaimed pins the rule the claim check exists
// for: a route is selected by the longest claim under it, not by whether its own
// endpoint claims something above it. An endpoint claiming /api registers
// /api/internal/x while another claims /api/internal; the request would go to
// the longer claim, so the route is unreachable and the startup names the
// endpoint and prefix that take it.
func TestARouteTakenByALongerClaimIsUnclaimed(t *testing.T) {
	probe := &stageProbe{
		name:     "probe.registers-a-route-taken-by-a-longer-claim",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("e", nethttp.MethodGet, "/api/internal/x", filler),
		),
	}
	a := startAssembly(t,
		claimedConfig(map[string][]string{"e": {"/api"}, "b": {"/api/internal"}}),
		Module(), probe.module())

	runErr := a.stop(t)
	if !errors.Is(runErr, ErrUnclaimedRoute) {
		t.Fatalf("a route taken by a longer claim started up with %v, want ErrUnclaimedRoute", runErr)
	}
	mustContain(t, runErr.Error(), `"e"`, "the endpoint that registered the route")
	mustContain(t, runErr.Error(), `"b"`, "the endpoint whose longer claim selects it")
	mustContain(t, runErr.Error(), "/api/internal", "the claim that takes it")
	mustContain(t, runErr.Error(), "/api/internal/x", "the route")
}

// TestTheUnclaimedRouteSuggestionCoversTheRoutes pins the suggested claim a
// startup failure offers. The suggestion has to be one the failing endpoint can
// adopt, so it takes the depth of the routes themselves: the top-level segment
// /api already belongs to another endpoint, and proposing it would send the
// reader to a configuration the claim check refuses.
func TestTheUnclaimedRouteSuggestionCoversTheRoutes(t *testing.T) {
	probe := &stageProbe{
		name:     "probe.registers-unclaimed-routes-under-the-api-namespace",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("account", nethttp.MethodGet, "/api/v1/auth/me", filler),
			routeOn("account", nethttp.MethodGet, "/api/v1/auth/session", filler),
		),
	}
	a := startAssembly(t,
		claimedConfig(map[string][]string{"account": {"/api/v1/features"}, "api": {"/api"}}),
		Module(), probe.module())

	runErr := a.stop(t)
	if !errors.Is(runErr, ErrUnclaimedRoute) {
		t.Fatalf("routes under an unclaimed namespace started up with %v, want ErrUnclaimedRoute", runErr)
	}
	mustContain(t, runErr.Error(), `for example "/api/v1/auth"`, "the claim covering the routes")
	if strings.Contains(runErr.Error(), `for example "/api"`) {
		t.Errorf("the suggestion names a prefix another endpoint already claims: %v", runErr)
	}
}

// TestPartialShadowingStarts pins that nested claims may partition a subtree: an
// endpoint claiming /api registers /api/:id and another claims /api/internal.
// /api/:id's literal leading path is /api, which selects the first endpoint, and
// /api/internal is the meaning of the nested claim. The startup succeeds and the
// request for /api/internal reaches the second endpoint.
func TestPartialShadowingStarts(t *testing.T) {
	serving := make(chan struct{}, 1)
	probe := &stageProbe{
		name:     "probe.registers-partially-shadowed-routes",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("a", nethttp.MethodGet, "/api/:id",
				func(c *gin.Context) { c.String(nethttp.StatusOK, "a:"+c.Param("id")) }),
			routeOn("b", nethttp.MethodGet, "/api/internal",
				func(c *gin.Context) { c.String(nethttp.StatusOK, "b") }),
		),
		onServe: func(*core.Registry) error {
			serving <- struct{}{}
			return nil
		},
	}
	a := startAssembly(t,
		claimedConfig(map[string][]string{"a": {"/api"}, "b": {"/api/internal"}}),
		Module(), probe.module())
	a.waitForOrEnd(t, serving, "the listener to be served")
	r, err := routerFrom(a.reg)
	if err != nil {
		t.Fatalf("taking this module's product out of the registry: %v", err)
	}
	addr, err := boundAddr(r, "a")
	if err != nil {
		t.Fatalf("finding the address the listener bound: %v", err)
	}

	got, err := getResponse(context.Background(), addr, "/api/internal")
	if err != nil {
		t.Fatalf("the request for the nested claim failed: %v", err)
	}
	if got.status != nethttp.StatusOK || got.body != "b" {
		t.Errorf("/api/internal answered %d %q, want 200 %q (the endpoint claiming it)",
			got.status, got.body, "b")
	}

	got, err = getResponse(context.Background(), addr, "/api/other")
	if err != nil {
		t.Fatalf("the request for the outer parameter route failed: %v", err)
	}
	if got.status != nethttp.StatusOK || got.body != "a:other" {
		t.Errorf("/api/other answered %d %q, want 200 %q", got.status, got.body, "a:other")
	}
}

// TestTheChainLineNamesTheClaims pins that the startup line carries the claims
// beside the route count. A zero-route endpoint is the case that shows why: the
// route count alone cannot say which paths it answers, and an operator checking
// that a module's namespace still points at its endpoint needs the claim there.
func TestTheChainLineNamesTheClaims(t *testing.T) {
	logger, records := newRecordingLogger()
	serving := make(chan struct{}, 1)
	probe := &stageProbe{
		name:     "probe.registers-a-zero-route-endpoint",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("f", nethttp.MethodGet, "/*path", filler),
		),
		onServe: func(*core.Registry) error {
			serving <- struct{}{}
			return nil
		},
	}
	modules := []core.Module{
		Module(),
		(&stageProbe{
			name:     "probe.delivers-the-logger",
			provides: []core.Provision{{Token: (*log.Logger)(nil)}},
			product:  stubLogger{logger: logger},
		}).module(),
		probe.module(),
	}
	a := startAssembly(t,
		claimedConfig(map[string][]string{"e": {"/x"}, "f": {"/"}}), modules...)
	a.waitForOrEnd(t, serving, "the listener to be served")

	var line string
	for _, written := range records.at(slog.LevelInfo) {
		if strings.Contains(written, "endpoint=e ") {
			line = written
			break
		}
	}
	if line == "" {
		t.Fatalf("the startup wrote no chain line for endpoint e; the records were %v",
			records.at(slog.LevelInfo))
	}
	mustContain(t, line, "routes=0", "the route count of the zero-route endpoint")
	mustContain(t, line, "prefixes=[/x]", "the claims of the zero-route endpoint")
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
