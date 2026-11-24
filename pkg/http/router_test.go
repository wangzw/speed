package http

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
)

func testRouter(names ...string) *router {
	listeners := make([]listenerSettings, 0, len(names))
	endpoints := make([]endpointSettings, 0, len(names))
	for _, name := range names {
		listeners = append(listeners, testListener(name))
		s := testSettings(name)
		s.listenerName = name
		endpoints = append(endpoints, s)
	}
	return newRouter(listeners, endpoints)
}

func endpointOf(t *testing.T, r *router, name string) Endpoint {
	t.Helper()
	e, err := r.Endpoint(name)
	if err != nil {
		t.Fatalf("endpoint %q: %v", name, err)
	}
	return e
}

// mustContain fails with the whole text when a fragment the message has to
// carry is missing.
func mustContain(t *testing.T, text, fragment, why string) {
	t.Helper()
	if !strings.Contains(text, fragment) {
		t.Errorf("the message does not carry %q, which is %s.\nThe message was: %s", fragment, why, text)
	}
}

// TestRouteBeforeGateOpensPanics pins the closed half of the gate.
func TestRouteBeforeGateOpensPanics(t *testing.T) {
	r := testRouter("public")
	e := endpointOf(t, r, "public")

	text := wantPanic(t, "Route before the gate opened", func() {
		e.Route("GET", "/things", filler)
	})
	mustContain(t, text, "public", "the endpoint the call was made on")
	mustContain(t, text, "before the registration gate opened", "which side of the window the call fell on")
	mustContain(t, text, "this module's Migrate callback", "where the window that accepts writes opens")
	// The last two fragments are read as a pair. The action phrase is the same
	// on both sides of the gate, so on its own it places the call on neither;
	// the sentence naming what the open window frames is what ties it to this
	// side.
	mustContain(t, text, "contains the whole Init stage", "what the surface's open window frames")
	mustContain(t, text, "Move the call into the module's Init callback", "the fixing action")
}

// TestUseBeforeGateOpensPanics is the same gate seen from the middleware side.
func TestUseBeforeGateOpensPanics(t *testing.T) {
	r := testRouter("public")
	e := endpointOf(t, r, "public")

	text := wantPanic(t, "Use before the gate opened", func() {
		e.Use(Middleware{Name: "trace", Stage: StageObserve, Handler: passThrough})
	})
	mustContain(t, text, "Use", "the call that was refused")
	mustContain(t, text, "before the registration gate opened", "which side of the window the call fell on")
}

// TestRegistrationInsideInitSucceeds is the open half: the window the gate leaves open really does
// accept both kinds of registration.
func TestRegistrationInsideInitSucceeds(t *testing.T) {
	r := testRouter("public")
	e := endpointOf(t, r, "public")
	r.open()

	e.Route("GET", "/things", filler)
	e.Use(Middleware{Name: "trace", Stage: StageObserve, Handler: passThrough})

	inner := r.endpoints["public"]
	if len(inner.routes) != 1 || inner.routes[0].method != "GET" || inner.routes[0].path != "/things" {
		t.Errorf("the route was not recorded: %+v", inner.routes)
	}
	if len(inner.layers) != 1 || inner.layers[0].mw.Name != "trace" {
		t.Errorf("the middleware was not recorded: %+v", inner.layers)
	}
}

// TestRegistrationAfterSealPanics pins the other end of the window.
func TestRegistrationAfterSealPanics(t *testing.T) {
	r := testRouter("public")
	e := endpointOf(t, r, "public")
	r.open()
	r.seal()

	text := wantPanic(t, "Route after the gate was sealed", func() {
		e.Route("GET", "/things", filler)
	})
	mustContain(t, text, "after the registration gate closed", "which side of the window the call fell on")
	mustContain(t, text, "assembled once in Serve", "why a late registration reaches nothing")

	useText := wantPanic(t, "Use after the gate was sealed", func() {
		e.Use(Middleware{Name: "trace", Stage: StageObserve, Handler: passThrough})
	})
	mustContain(t, useText, "after the registration gate closed", "which side of the window the call fell on")
}

// TestUnknownEndpointNamesAvailableEndpoints pins the failure a misspelled endpoint name produces.
func TestUnknownEndpointNamesAvailableEndpoints(t *testing.T) {
	r := testRouter("public", "admin")

	got, err := r.Endpoint("piblic")
	if got != nil {
		t.Errorf("an unknown endpoint name handed back an endpoint: %#v", got)
	}
	if !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("the error is not ErrUnknownEndpoint: %v", err)
	}
	mustContain(t, err.Error(), `"piblic"`, "the name that was asked for")
	mustContain(t, err.Error(), `"public"`, "one of the names that do exist")
	mustContain(t, err.Error(), `"admin"`, "the other name that does exist")
}

// TestUnknownEndpointWithNoEndpointsSaysSo pins the empty case: "the endpoints are" followed by
// nothing reads as a truncated message rather than as the state it describes.
func TestUnknownEndpointWithNoEndpointsSaysSo(t *testing.T) {
	_, err := testRouter().Endpoint("public")
	if !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("the error is not ErrUnknownEndpoint: %v", err)
	}
	mustContain(t, err.Error(), "declares no endpoint at all", "the state the message describes")
}

// TestEndpointsListsEveryDeclaredNameInAscendingOrder pins the set and its order.
func TestEndpointsListsEveryDeclaredNameInAscendingOrder(t *testing.T) {
	r := testRouter("public", "admin", "metrics")

	got := r.Endpoints()

	if want := []string{"admin", "metrics", "public"}; !equalStrings(got, want) {
		t.Errorf("the router lists the endpoints %v, want %v in ascending order", got, want)
	}
	for _, name := range got {
		if _, err := r.Endpoint(name); err != nil {
			t.Errorf("the router lists %q and Endpoint does not hand it out: %v", name, err)
		}
	}
}

// TestEndpointsReturnsASliceOfItsOwn pins that a caller changing the list it was handed changes
// nothing in the router.
func TestEndpointsReturnsASliceOfItsOwn(t *testing.T) {
	r := testRouter("public", "admin", "metrics")

	first := r.Endpoints()
	first[0], first[len(first)-1] = "tampered", "extra"

	if want := []string{"admin", "metrics", "public"}; !equalStrings(r.Endpoints(), want) {
		t.Errorf("after a caller changed the list it was given, the router lists %v, want %v",
			r.Endpoints(), want)
	}
	for _, name := range []string{"admin", "metrics", "public"} {
		if _, err := r.Endpoint(name); err != nil {
			t.Errorf("after a caller changed the list it was given, Endpoint(%q) failed: %v", name, err)
		}
	}
}

// TestEndpointsOfARouterWithNoEndpointsListsNone pins the empty set as a plain answer rather than a
// failure: a router built from a configuration that declares nothing lists no name.
func TestEndpointsOfARouterWithNoEndpointsListsNone(t *testing.T) {
	if got := testRouter().Endpoints(); len(got) != 0 {
		t.Errorf("a router with no endpoint lists %v", got)
	}
}

// TestEndpointsIsReadableInEveryStage pins that the list, like Endpoint and unlike Route and Use,
// is not behind the registration gate.
func TestEndpointsIsReadableInEveryStage(t *testing.T) {
	m := Module()
	product := testProduct(t, endpointsAt(5*time.Second, "public", "admin"))
	ctx := context.Background()
	t.Cleanup(func() { _ = m.Close(ctx, nil, product) })
	want := []string{"admin", "public"}

	check := func(stage string) {
		t.Helper()
		if got := product.Endpoints(); !equalStrings(got, want) {
			t.Errorf("%s: the router lists %v, want %v", stage, got, want)
		}
	}

	check("before the gate opened")
	if err := m.Migrate(ctx, nil, product); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	check("with the gate open")
	if err := m.Start(ctx, nil, product); err != nil {
		t.Fatalf("Start: %v", err)
	}
	check("after the gate closed")
	if err := m.Serve(ctx, nil, product); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	check("while serving")
	if err := m.Stop(ctx, nil, product); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	check("after Stop")
}

// TestEndpointsReachesARegistrantThroughTheCapability pins the list on the path a registrant takes:
// the Router taken up through the registry, inside Init.
func TestEndpointsReachesARegistrantThroughTheCapability(t *testing.T) {
	listed := make(chan []string, 1)
	served := make(chan struct{}, 1)
	probe := &stageProbe{
		name:     "probe.lists-the-endpoints",
		requires: requiresRouter(),
		onInit: func(reg *core.Registry) error {
			delivered, err := core.Resolve[Router](reg)
			if err != nil {
				return err
			}
			listed <- delivered.Endpoints()
			return nil
		},
		onServe: func(*core.Registry) error {
			served <- struct{}{}
			return nil
		},
	}
	a := startAssembly(t, endpointsAt(5*time.Second, "public", "admin", "metrics"), Module(), probe.module())
	a.waitForOrEnd(t, served, "the Serve callback of the module that lists the endpoints")

	select {
	case got := <-listed:
		if want := []string{"admin", "metrics", "public"}; !equalStrings(got, want) {
			t.Errorf("the Router taken up through the registry lists %v, want %v", got, want)
		}
	default:
		t.Fatal("the module behind the entry point listed nothing in Init")
	}
	if err := a.stop(t); err != nil {
		t.Errorf("the run came back with %v", err)
	}
}

// TestNilHandlerFieldPanics checks that a nil Handler field panics at registration.
func TestNilHandlerFieldPanics(t *testing.T) {
	r := testRouter("public")
	e := endpointOf(t, r, "public")
	r.open()

	text := wantPanic(t, "Use with a nil Handler", func() {
		e.Use(Middleware{Name: "trace", Stage: StageObserve})
	})
	mustContain(t, text, "trace", "the layer that was registered")
	mustContain(t, text, "nil Handler", "what was wrong with it")
	mustContain(t, text, `endpoint "public"`, "the endpoint it was registered on")

	if len(r.endpoints["public"].layers) != 0 {
		t.Error("the layer with no Handler was recorded anyway")
	}
}

// TestRouteWithoutAHandlerPanics pins a route that binds nothing.
func TestRouteWithoutAHandlerPanics(t *testing.T) {
	r := testRouter("public")
	e := endpointOf(t, r, "public")
	r.open()

	text := wantPanic(t, "Route with no handler", func() { e.Route("GET", "/things") })
	mustContain(t, text, `Route("GET", "/things")`, "the call that bound nothing")
	mustContain(t, text, `endpoint "public"`, "the endpoint it was registered on")
	mustContain(t, text, "no handler", "what was wrong with it")

	if len(r.endpoints["public"].routes) != 0 {
		t.Error("the route with no handler was recorded anyway")
	}
}

// TestRouteWithANilHandlerPanics pins the nil handler gin accepts and crashes on at request time,
// wherever in the list it stands: a check on the first position alone passes the second row.
func TestRouteWithANilHandlerPanics(t *testing.T) {
	for _, c := range []struct {
		name     string
		handlers []gin.HandlerFunc
		at       string
	}{
		{"alone", []gin.HandlerFunc{nil}, "position 0"},
		{"after a handler", []gin.HandlerFunc{filler, nil}, "position 1"},
		{"before a handler", []gin.HandlerFunc{nil, filler}, "position 0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := testRouter("public")
			e := endpointOf(t, r, "public")
			r.open()

			text := wantPanic(t, "Route with a nil handler", func() {
				e.Route("GET", "/things", c.handlers...)
			})
			mustContain(t, text, `Route("GET", "/things")`, "the call that carried the nil")
			mustContain(t, text, "nil handler", "what was wrong with it")
			mustContain(t, text, c.at, "which position held it")

			if len(r.endpoints["public"].routes) != 0 {
				t.Error("the route with a nil handler was recorded anyway")
			}
		})
	}
}

// TestRouteWithAPathNotInGinsFormPanics pins the path forms gin rewrites.
func TestRouteWithAPathNotInGinsFormPanics(t *testing.T) {
	for _, c := range []struct {
		path string
		form string
	}{
		{"x", "/x"},
		{"/a//b", "/a/b"},
		{"/a/../b", "/b"},
		{"/a/./b", "/a/b"},
		{"/a/b//", "/a/b/"},
		{"", "/"},
	} {
		t.Run(fmt.Sprintf("%q", c.path), func(t *testing.T) {
			r := testRouter("public")
			e := endpointOf(t, r, "public")
			r.open()

			text := wantPanic(t, "Route with a path not in gin's form", func() {
				e.Route("GET", c.path, filler)
			})
			mustContain(t, text, fmt.Sprintf("Route(%q, %q)", "GET", c.path), "the call that was refused")
			mustContain(t, text, `endpoint "public"`, "the endpoint it was registered on")
			mustContain(t, text, fmt.Sprintf("Write the path as %q", c.form), "the form to write instead")

			if len(r.endpoints["public"].routes) != 0 {
				t.Error("the route with a path in another form was recorded anyway")
			}
		})
	}
}

// TestRouteWithAMethodOrPathGinRefusesPanics pins that what gin rejects on its own is the call-site
// mistake it is, named where it was written.
func TestRouteWithAMethodOrPathGinRefusesPanics(t *testing.T) {
	for _, c := range []struct {
		name   string
		method string
		path   string
	}{
		{"a method with a space", "BAD METHOD", "/x"},
		{"an empty method", "", "/x"},
		{"a catch-all that does not end the path", "GET", "/a/*p/b"},
		{"a parameter with no name", "GET", "/a/:"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := testRouter("public")
			e := endpointOf(t, r, "public")
			r.open()

			text := wantPanic(t, "Route gin refuses", func() {
				e.Route(c.method, c.path, filler)
			})
			mustContain(t, text, fmt.Sprintf("Route(%q, %q)", c.method, c.path), "the call that was refused")
			mustContain(t, text, `endpoint "public"`, "the endpoint it was registered on")
			mustContain(t, text, "refuses on its own", "that gin refused it by itself, with nothing else mounted")

			r.seal()
			if got := len(r.endpoints["public"].routes); got != 0 {
				t.Fatalf("a refused registration was recorded on the endpoint: %d routes", got)
			}
		})
	}
}

// TestRouteAcceptsGinsWritings pins the other side of those checks: the forms a route is
// legitimately written in are accepted and recorded exactly as written.
func TestRouteAcceptsGinsWritings(t *testing.T) {
	for _, path := range []string{
		"/",
		"/things",
		"/things/",
		"/things/:id",
		"/things/:id/",
		"/things/:id/parts/:part",
		"/files/*rest",
		"/things/:id/files/*rest",
	} {
		t.Run(path, func(t *testing.T) {
			r := testRouter("public")
			e := endpointOf(t, r, "public")
			r.open()

			e.Route("GET", path, filler)

			routes := r.endpoints["public"].routes
			if len(routes) != 1 || routes[0].path != path {
				t.Errorf("the path %q was recorded as %+v", path, routes)
			}
		})
	}
}

// TestIllegalConstraintTokenPanicsAtTheRegistration pins where an illegal capability token is
// reported.
func TestIllegalConstraintTokenPanicsAtTheRegistration(t *testing.T) {
	r := testRouter("public")
	e := endpointOf(t, r, "public")
	r.open()

	text := wantPanic(t, "Use with an untyped nil in After", func() {
		e.Use(Middleware{Name: "trace", Stage: StageObserve, After: []any{nil}, Handler: passThrough})
	})
	mustContain(t, text, `endpoint "public"`, "the endpoint the declaration was made on")
	mustContain(t, text, `middleware "trace"`, "the layer that declared it")
	mustContain(t, text, "After[0]", "which declaration in which field")

	if len(r.endpoints["public"].layers) != 0 {
		t.Error("the layer with the illegal declaration was recorded anyway")
	}
}

// TestIllegalBeforeTokenPanicsAtTheRegistration is the same statement for Before: it is read at the
// registration like After and Provides, naming the declaration.
func TestIllegalBeforeTokenPanicsAtTheRegistration(t *testing.T) {
	r := testRouter("public")
	e := endpointOf(t, r, "public")
	r.open()

	text := wantPanic(t, "Use with an untyped nil in Before", func() {
		e.Use(Middleware{Name: "trace", Stage: StageObserve, Before: []any{nil}, Handler: passThrough})
	})
	mustContain(t, text, `endpoint "public"`, "the endpoint the declaration was made on")
	mustContain(t, text, `middleware "trace"`, "the layer that declared it")
	mustContain(t, text, "Before[0]", "which declaration in which field")

	if len(r.endpoints["public"].layers) != 0 {
		t.Error("the layer with the illegal declaration was recorded anyway")
	}
}

// TestIllegalProvidesTokenPanicsNamingTheDeclaration pins that Provides is checked at the call,
// alongside After and Before, and with the same locating text: which endpoint, which layer.
func TestIllegalProvidesTokenPanicsNamingTheDeclaration(t *testing.T) {
	var interfaceValue probe
	for _, c := range []struct {
		name  string
		token core.Token
		says  string
	}{
		{name: "an untyped nil", token: nil, says: "untyped nil"},
		{name: "a non-pointer", token: concreteProbe{}, says: "must be a typed nil pointer"},
		{name: "a non-nil pointer", token: &interfaceValue, says: "must be a nil pointer"},
		{name: "a pointer to a concrete type", token: (*concreteProbe)(nil), says: "must point to an interface"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := testRouter("public")
			e := endpointOf(t, r, "public")
			r.open()

			text := wantPanic(t, "Use with "+c.name+" in Provides", func() {
				e.Use(Middleware{
					Name:     "trace",
					Stage:    StageObserve,
					Provides: []core.Token{(*probe)(nil), c.token},
					Handler:  passThrough,
				})
			})
			mustContain(t, text, `endpoint "public"`, "the endpoint the declaration was made on")
			mustContain(t, text, `middleware "trace"`, "the layer that declared it")
			mustContain(t, text, "Provides[1]", "which declaration in which field")
			mustContain(t, text, c.says, "what is wrong with the token")

			if len(r.endpoints["public"].layers) != 0 {
				t.Error("the layer with the illegal declaration was recorded anyway")
			}
		})
	}
}

// TestProvidesIsRecordedOnTheLayer pins the field reaching the graph node the ordering reads.
func TestProvidesIsRecordedOnTheLayer(t *testing.T) {
	r := testRouter("public")
	e := endpointOf(t, r, "public")
	r.open()

	e.Use(Middleware{Name: "auth", Stage: StageObserve, Provides: []core.Token{(*probe)(nil)}, Handler: passThrough})

	layers := r.endpoints["public"].layers
	if len(layers) != 1 {
		t.Fatalf("%d layers were recorded, want 1", len(layers))
	}
	if len(layers[0].provides) != 1 {
		t.Fatalf("the recorded layer stands for %v, and the registration named one capability",
			layers[0].provides)
	}
	if got := capabilityKey(layers[0].provides[0], "the recorded layer"); got != reflect.TypeOf((*probe)(nil)).Elem() {
		t.Errorf("the recorded layer stands for %s, and the registration named probe", got)
	}
}

// TestRouteKeepsItsHandlersWhenTheCallersSliceChanges pins that a registration records the handlers
// it was given and not the caller's slice.
func TestRouteKeepsItsHandlersWhenTheCallersSliceChanges(t *testing.T) {
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		handlers := []gin.HandlerFunc{func(c *gin.Context) { c.String(nethttp.StatusOK, "original") }}
		e.Route("GET", "/things", handlers...)
		handlers[0] = func(c *gin.Context) { c.String(nethttp.StatusOK, "replaced") }
	})

	recorder := serveOnce(handler, nethttp.MethodGet, "/things")

	if got := recorder.Body.String(); got != "original" {
		t.Errorf("the route served %q, want the handler it was registered with", got)
	}
}

// TestConcurrentRegistrationIsSafe pins the mutex.
func TestConcurrentRegistrationIsSafe(t *testing.T) {
	r := testRouter("public")
	e := endpointOf(t, r, "public")
	r.open()

	const registrants = 8
	const each = 16
	var wg sync.WaitGroup
	for i := range registrants {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range each {
				e.Route("GET", fmt.Sprintf("/things/%d/%d", i, j), filler)
				e.Use(Middleware{Name: fmt.Sprintf("layer-%d-%d", i, j), Stage: StageObserve, Handler: passThrough})
			}
		}()
	}
	wg.Wait()

	inner := r.endpoints["public"]
	if len(inner.routes) != registrants*each {
		t.Errorf("%d routes were registered and %d were recorded", registrants*each, len(inner.routes))
	}
	if len(inner.layers) != registrants*each {
		t.Errorf("%d layers were registered and %d were recorded", registrants*each, len(inner.layers))
	}
	seen := make(map[string]bool, registrants*each)
	for _, l := range inner.layers {
		if seen[l.mw.Name] {
			t.Fatalf("layer %q was recorded twice", l.mw.Name)
		}
		seen[l.mw.Name] = true
	}
}

// TestAcceptingIsFalseBeforeBinding pins the read side of the surface: it is not behind the gate.
func TestAcceptingIsFalseBeforeBinding(t *testing.T) {
	r := testRouter("public")
	if endpointOf(t, r, "public").Accepting() {
		t.Error("an endpoint that never bound an address reports that it is accepting requests")
	}
}
