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

// testRouter builds a router over the named endpoints, each on a listener of
// its own named the same, with the gate still shut, which is the state New
// leaves it in.
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

// endpointOf takes an endpoint out, failing the test if it is not there.
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

// TestRouteBeforeGateOpensPanics pins the closed half of the gate. A
// registration made before the gate opens is a wiring mistake, and the surface
// returns nothing, so the panic is the only way it can be reported at all.
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

// TestRegistrationInsideInitSucceeds is the open half: the window the gate
// leaves open really does accept both kinds of registration.
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

// TestRegistrationAfterSealPanics pins the other end of the window. A
// registration arriving once the chains are due to be assembled reaches no
// chain, and being told so is the whole difference between a route that is
// missing and a route that is missing silently.
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

// TestUnknownEndpointNamesAvailableEndpoints pins the failure a misspelled
// endpoint name produces. It is an error rather than a panic because the
// endpoint set is configuration, and the text lists the names that do exist,
// which is the fixing action.
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

// TestUnknownEndpointWithNoEndpointsSaysSo pins the empty case: "the endpoints
// are" followed by nothing reads as a truncated message rather than as the
// state it describes.
func TestUnknownEndpointWithNoEndpointsSaysSo(t *testing.T) {
	_, err := testRouter().Endpoint("public")
	if !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("the error is not ErrUnknownEndpoint: %v", err)
	}
	mustContain(t, err.Error(), "declares no endpoint at all", "the state the message describes")
}

// TestEndpointsListsEveryDeclaredNameInAscendingOrder pins the set and its
// order. The router is built from names declared out of order, so an answer in
// declaration order is told apart from one a reader can rely on; and every name
// listed resolves through Endpoint, which ties the list to the set a registrant
// can actually take an endpoint from.
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

// TestEndpointsReturnsASliceOfItsOwn pins that a caller changing the list it was
// handed changes nothing in the router. The list is read to decide which
// endpoints to register on, and one caller overwriting an element must not turn
// the next caller's answer into something else.
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

// TestEndpointsOfARouterWithNoEndpointsListsNone pins the empty set as a plain
// answer rather than a failure: a router built from a configuration that
// declares nothing lists no name.
func TestEndpointsOfARouterWithNoEndpointsListsNone(t *testing.T) {
	if got := testRouter().Endpoints(); len(got) != 0 {
		t.Errorf("a router with no endpoint lists %v", got)
	}
}

// TestEndpointsIsReadableInEveryStage pins that the list, like Endpoint and
// unlike Route and Use, is not behind the registration gate. A registrant may
// ask which endpoints exist before the gate opens, inside the window and after
// it has closed, and gets the same set each time; a read that panicked outside
// the window would turn a harmless question into a startup failure.
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

// TestEndpointsReachesARegistrantThroughTheCapability pins the list on the path
// a registrant takes: the Router taken up through the registry, inside Init,
// answers with the names configuration declared.
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

// TestNilHandlerFieldPanics pins the layer that would do nothing. Handing back
// an error instead would let a registrant drop it and carry on believing the
// layer is in the chain, and gin itself accepts a nil handler and crashes when a
// request reaches it.
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

// TestRouteWithoutAHandlerPanics pins a route that binds nothing. The call is
// legal as far as the signature goes, and a route that was registered without a
// handler would answer nothing for ever without a word.
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

// TestRouteWithANilHandlerPanics pins the nil handler gin accepts and crashes on
// at request time, wherever in the list it stands: a check on the first position
// alone passes the second row.
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

// TestRouteWithAPathNotInGinsFormPanics pins the path forms gin rewrites. The
// engine joins a registered path onto its root and cleans it, so Context.FullPath
// reports the cleaned form; a layer comparing (method, FullPath) with the
// registered (method, path) would never match a route registered in any other
// writing, and nothing would say so.
//
// Each row is a path with a different thing wrong with it, and the message has
// to carry the form to write instead.
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

// TestRouteWithAMethodOrPathGinRefusesPanics pins that what gin rejects on its
// own is the call-site mistake it is, named where it was written.
//
// It is knowable from this one registration, with nothing else mounted, which
// is what separates it from a conflict: a conflict is a property of the set and
// only exists once the chain is assembled. Left to be discovered in Serve, a
// malformed registration comes back as a clash between two registrations, so a
// caller reading ErrRouteConflict goes looking for a second registration that is
// not there.
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

// TestRouteAcceptsGinsWritings pins the other side of those checks: the forms a
// route is legitimately written in are accepted and recorded exactly as written.
// A trailing slash is kept, because "/x/" and "/x" are two different routes on
// an engine that does not redirect, and a parameter and a catch-all are gin's
// own spelling of a path segment.
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

// TestIllegalConstraintTokenPanicsAtTheRegistration pins where an illegal
// capability token is reported. Checked when the chain is ordered instead, the
// panic would name the endpoint but not the call that wrote the declaration,
// and Serve runs long after the registration returned.
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

// TestIllegalBeforeTokenPanicsAtTheRegistration is the same statement for
// Before: it is read at the registration like After and Provides, naming the
// declaration, and the layer that wrote it is not recorded. A layer registered
// with a Before that designates nothing would otherwise be placed against a
// capability nobody can name, with the panic coming from Serve long after the
// call that wrote it.
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

// TestIllegalProvidesTokenPanicsNamingTheDeclaration pins that Provides is
// checked at the call, alongside After and Before, and with the same locating
// text: which endpoint, which layer, which position in which field.
//
// It is the field that decides what every other layer's constraints resolve
// against, so a token in it that designates nothing does not merely fail to
// place this layer — it silently removes the target of everybody else's After.
// An implementation that checks After and Before and leaves Provides alone
// panics on none of the four rows below.
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

// TestProvidesIsRecordedOnTheLayer pins the field reaching the graph node the
// ordering reads. Dropped between Use and the record, every constraint on the
// endpoint would find no provider, and the chain would come out ordered by
// registration order alone with nothing saying so.
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

// TestRouteKeepsItsHandlersWhenTheCallersSliceChanges pins that a registration
// records the handlers it was given and not the caller's slice. A registrant may
// build the list once and change it afterwards, or pass the same backing array
// to a second registration; the route is what was registered, whatever happens
// to the slice after the call returns.
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

// TestConcurrentRegistrationIsSafe pins the mutex. The design does not assume
// registrations come from one goroutine: the stages are driven one module at a
// time, but a registrant may start goroutines of its own inside its callback.
//
// Run under -race, an unsynchronised append shows up here; the count assertion
// catches the lost update that an unsynchronised append also produces without
// the race detector.
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

// TestAcceptingIsFalseBeforeBinding pins the read side of the surface: it is
// not behind the gate, and an endpoint that has not bound its address yet
// reports that it is not accepting rather than panicking or claiming it is.
func TestAcceptingIsFalseBeforeBinding(t *testing.T) {
	r := testRouter("public")
	if endpointOf(t, r, "public").Accepting() {
		t.Error("an endpoint that never bound an address reports that it is accepting requests")
	}
}
