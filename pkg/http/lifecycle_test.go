package http

import (
	"context"
	"errors"
	"fmt"
	"net"
	nethttp "net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// The tests in this file drive the module through core.New() and Run, so that
// what they observe is the assembly a host gets rather than a callback called
// by hand. The pieces they are built out of are in testsupport_test.go.
//
// Two rules hold throughout. Wherever a probe has to be on a definite side of
// this module, the side is given by an explicit dependency declaration: core
// promises dependency order and nothing more, and how it linearises what that
// leaves open is an implementation choice of its own, not a contract to pin.
// And every wait carries its own deadline, because go test's global timeout
// turns one blocked assertion into a whole-binary panic minutes later with
// nothing saying which.

// TestServeDoesNotBlockRun pins the half of the Serve contract that only an
// assembly can show: the callback returns, so the driver reaches the modules
// behind it and then the running state.
//
// The probe declares a requirement on the Router, which is what places it
// after this module; it is not "the probe whose name sorts later". A Serve
// that ran its accept loop in the calling goroutine would never let the probe
// run at all, and the wait below would end at its deadline.
func TestServeDoesNotBlockRun(t *testing.T) {
	served := make(chan struct{}, 1)
	var (
		mu        sync.Mutex
		accepting bool
		status    int
	)

	after := &stageProbe{
		name:     "probe.requires-the-router",
		requires: requiresRouter(),
		onInit: routeOn("public", "GET", "/things", func(c *gin.Context) {
			c.Status(nethttp.StatusTeapot)
		}),
		onServe: func(reg *core.Registry) error {
			r, err := routerFrom(reg)
			if err != nil {
				return err
			}
			addr, err := boundAddr(r, "public")
			if err != nil {
				return err
			}
			code, err := getStatus(context.Background(), addr, "/things")
			if err != nil {
				return fmt.Errorf("the endpoint refused a request from the Serve callback "+
					"of the module behind it: %w", err)
			}
			mu.Lock()
			accepting, status = r.endpoints["public"].Accepting(), code
			mu.Unlock()
			served <- struct{}{}
			return nil
		},
	}

	a := startAssembly(t, oneEndpoint("public"), Module(), after.module())
	waitFor(t, served, "the Serve callback of the module that requires the Router")

	mu.Lock()
	gotAccepting, gotStatus := accepting, status
	mu.Unlock()
	if !gotAccepting {
		t.Error("the endpoint was not accepting by the time the next module served, so the " +
			"address was not bound in the synchronous part of Serve")
	}
	if gotStatus != nethttp.StatusTeapot {
		t.Errorf("the endpoint answered %d from the next module's Serve, not the %d the "+
			"registered handler writes", gotStatus, nethttp.StatusTeapot)
	}
	if err := a.stop(t); err != nil {
		t.Errorf("a run that served and was then cancelled came back with %v", err)
	}
}

// TestNoModuleIsReleasedUnderAnInFlightRequest pins what the drain is for: a
// request that was in flight when the shutdown began finishes against a whole
// assembly. The drain has to be over before any module stopped after this one
// receives its Stop, and before any module at all receives its Close — an
// in-flight request can reach any module in the process, and one that was
// stopped refuses its work while one that was closed has released what the
// request is using.
//
// Two probes watch from the two sides. The one delivering the Logger
// capability this module optionally requires is constructed before it and
// therefore stopped after it; the one requiring the Router is constructed after
// it and closed with every other module once the Stop stage is over. Each
// records whether the held request had finished by the time its own callback
// ran. The request is let go a moment after the endpoint stops accepting, so a
// module released while the drain is still running sees it unfinished.
func TestNoModuleIsReleasedUnderAnInFlightRequest(t *testing.T) {
	gate := newGateHandler(t)
	serving := make(chan *router, 1)

	var (
		mu                        sync.Mutex
		finishedAtStop, stopRan   bool
		finishedAtClose, closeRan bool
	)
	requestFinished := func() bool { return len(gate.finished) > 0 }

	logger, _ := newRecordingLogger()
	stoppedAfter := &stageProbe{
		name:     "probe.delivers-the-logger",
		provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		product:  stubLogger{logger: logger},
		onStop: func(*core.Registry) error {
			mu.Lock()
			defer mu.Unlock()
			stopRan, finishedAtStop = true, requestFinished()
			return nil
		},
	}
	constructedAfter := &stageProbe{
		name:     "probe.requires-the-router",
		requires: requiresRouter(),
		onInit:   routeOn("public", "GET", "/things", gate.gin),
		onServe: func(reg *core.Registry) error {
			r, err := routerFrom(reg)
			if err != nil {
				return err
			}
			serving <- r
			return nil
		},
		onClose: func(*core.Registry) error {
			mu.Lock()
			defer mu.Unlock()
			closeRan, finishedAtClose = true, requestFinished()
			return nil
		},
	}

	a := startAssembly(t,
		endpointsAt(waitDeadline, "public"),
		Module(), stoppedAfter.module(), constructedAfter.module())

	var r *router
	select {
	case r = <-serving:
	case <-time.After(waitDeadline):
		t.Fatalf("timed out after %s waiting for the endpoint to bind", waitDeadline)
	}
	addr, err := boundAddr(r, "public")
	if err != nil {
		t.Fatal(err)
	}
	answered := make(chan int, 1)
	go func() {
		status, _ := getStatus(context.Background(), addr, "/things")
		answered <- status
	}()
	waitFor(t, gate.entered, "the held request to reach the handler")

	a.cancel()
	waitUntil(t, "the endpoint to stop accepting", func() bool { return !r.endpoints["public"].Accepting() })
	// Long enough that a module released while the drain is still running is
	// released inside it; the modules behind the endpoint are otherwise
	// reached within microseconds of its Stop.
	time.Sleep(200 * time.Millisecond)
	gate.letGo()

	if err := a.wait(t); err != nil {
		t.Fatalf("a run whose in-flight request was released came back with %v", err)
	}
	select {
	case status := <-answered:
		if status != nethttp.StatusNoContent {
			t.Errorf("the in-flight request was answered %d, not the %d its handler writes",
				status, nethttp.StatusNoContent)
		}
	case <-time.After(waitDeadline):
		t.Fatal("the in-flight request was never answered")
	}

	mu.Lock()
	defer mu.Unlock()
	if !stopRan || !closeRan {
		t.Fatalf("a probe callback did not run (Stop %v, Close %v), so the order this test is built on did not hold",
			stopRan, closeRan)
	}
	if !finishedAtStop {
		t.Error("a module stopped after this one received its Stop while a request was still in " +
			"flight; the drain has to be over before any module behind the entry point stops " +
			"taking work, or the request loses what it reaches")
	}
	if !finishedAtClose {
		t.Error("a module received its Close while a request was still in flight; the drain has " +
			"to be over before any module releases what the request may be using")
	}
}

// TestRegistrationOutsideInitPanicsInAssembly pins the gate from inside a real
// assembly, on both of its sides at once.
//
// The probe requires the Router, so its New runs after this module's New and
// before any Migrate, and its Start runs after this module's Start. Those are
// exactly the two moments the gate is shut, and each panic has to say which
// side it is and what to do about it: a registration that only panicked with
// "not allowed" would leave the registrant to guess the stage.
func TestRegistrationOutsideInitPanicsInAssembly(t *testing.T) {
	var (
		mu        sync.Mutex
		fromNew   string
		fromStart string
	)
	// registerLate makes one registration and hands back the panic text
	// instead of letting it take the assembly down.
	//
	// The two calls use paths of their own, so that an implementation
	// letting both through is caught by the two assertions below rather
	// than by the route conflict one repeated path would raise.
	registerLate := func(reg *core.Registry, path string) (text string, err error) {
		e, err := endpointFrom(reg, "public")
		if err != nil {
			return "", err
		}
		defer func() {
			if raised := recover(); raised != nil {
				text = fmt.Sprint(raised)
			}
		}()
		e.Route("GET", path, filler)
		return "", nil
	}

	outside := &stageProbe{
		name:     "probe.registers-outside-init",
		requires: requiresRouter(),
		onNew: func(reg *core.Registry) error {
			text, err := registerLate(reg, "/from-new")
			if err != nil {
				return err
			}
			mu.Lock()
			fromNew = text
			mu.Unlock()
			return nil
		},
		onStart: func(reg *core.Registry) error {
			text, err := registerLate(reg, "/from-start")
			if err != nil {
				return err
			}
			mu.Lock()
			fromStart = text
			mu.Unlock()
			return nil
		},
	}

	a := startAssembly(t, oneEndpoint("public"), Module(), outside.module())
	if err := a.stop(t); err != nil {
		t.Fatalf("the assembly failed instead of carrying on past the two recovered "+
			"registrations: %v", err)
	}

	mu.Lock()
	early, late := fromNew, fromStart
	mu.Unlock()
	// Each refusal is read in two parts: the call site and the side of the
	// gate, then the wording that belongs to this module's gate. That second
	// part pairs the sentence naming what the surface's open window frames
	// with the action the registrant is handed; the action phrase reads the
	// same on both sides, so the window sentence is what ties it to the side
	// asserted here.
	if early == "" {
		t.Error("registering from New, before the gate opened, did not panic: the " +
			"surface returns nothing, so a registration it drops is a route nobody can reach")
	} else {
		assertNames(t, early, "\"public\"", "Route", "before the registration gate opened",
			"contains the whole Init stage", "Move the call into the module's Init callback")
	}
	if late == "" {
		t.Error("registering from Start, after the gate had closed, did not panic: the " +
			"chains are assembled in Serve, so that registration would reach no chain")
	} else {
		assertNames(t, late, "\"public\"", "Route", "after the registration gate closed",
			"contains the whole Init stage", "Move the call into the module's Init callback")
	}
}

// TestBindFailureRollsBackBoundEndpoints pins that a bind failure ends the
// startup and that the rollback really gives the ports back. All the endpoints
// share one module lifecycle, so one of them failing has to release the ones
// already listening, or a restart fails on an address the dead process holds.
func TestBindFailureRollsBackBoundEndpoints(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("taking a port to collide with: %v", err)
	}
	defer taken.Close()

	var (
		mu       sync.Mutex
		captured *router
	)
	watch := &stageProbe{
		name:     "probe.captures-the-router",
		requires: requiresRouter(),
		onInit: func(reg *core.Registry) error {
			r, rerr := routerFrom(reg)
			if rerr != nil {
				return rerr
			}
			mu.Lock()
			captured = r
			mu.Unlock()
			return nil
		},
	}

	// The endpoints are bound in name order, so "a" comes up and "b" is the
	// one that collides.
	cfg := moduleConfig{Endpoints: map[string]endpointConfig{
		"a": {Address: "127.0.0.1:0"},
		"b": {Address: taken.Addr().String()},
	}}
	a := startAssembly(t, cfg, Module(), watch.module())

	// stop rather than wait: a startup that fails comes back on its own, so
	// the cancellation changes nothing here, while an implementation that
	// let this configuration come up reports what it returned instead of
	// sitting out the deadline with nothing to say.
	runErr := a.stop(t)
	if !errors.Is(runErr, ErrListen) {
		t.Fatalf("a startup whose second endpoint cannot bind came back with %v, which is "+
			"not a bind failure", runErr)
	}
	mustContain(t, runErr.Error(), "\"b\"", "the endpoint that could not bind")
	mustContain(t, runErr.Error(), taken.Addr().String(), "the address it could not take")

	mu.Lock()
	r := captured
	mu.Unlock()
	if r == nil {
		t.Fatal("the probe never captured the router, so nothing below was observed")
	}
	addr, err := boundAddr(r, "a")
	if err != nil {
		t.Fatalf("the endpoint bound before the failing one: %v", err)
	}
	if r.endpoints["a"].Accepting() {
		t.Error("the endpoint that did bind is still accepting after the rollback")
	}
	reopened, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the port endpoint %q had bound was not released by the rollback: %v", "a", err)
	}
	reopened.Close()
}

// TestShutdownAggregatesPerEndpointDrainFailures pins that the shutdown waits
// for every endpoint and reports all of them, once each. One endpoint timing
// out neither shortens another's wait nor hides it: a shutdown report naming
// one of two stuck endpoints sends its reader after the wrong configuration
// key, and one naming the same endpoint twice reads like two failures.
func TestShutdownAggregatesPerEndpointDrainFailures(t *testing.T) {
	gateA, gateB := newGateHandler(t), newGateHandler(t)
	serving := make(chan struct{}, 1)
	var (
		mu           sync.Mutex
		addrA, addrB string
	)

	hold := &stageProbe{
		name:     "probe.holds-a-request-on-each-endpoint",
		requires: requiresRouter(),
		onInit: inOrder(
			routeOn("a", "GET", "/things", gateA.gin),
			routeOn("b", "GET", "/things", gateB.gin),
		),
		onServe: func(reg *core.Registry) error {
			r, err := routerFrom(reg)
			if err != nil {
				return err
			}
			gotA, err := boundAddr(r, "a")
			if err != nil {
				return err
			}
			gotB, err := boundAddr(r, "b")
			if err != nil {
				return err
			}
			mu.Lock()
			addrA, addrB = gotA, gotB
			mu.Unlock()
			serving <- struct{}{}
			return nil
		},
	}

	a := startAssembly(t,
		endpointsAt(100*time.Millisecond, "a", "b"),
		Module(), hold.module())
	waitFor(t, serving, "both endpoints to bind")

	mu.Lock()
	atA, atB := addrA, addrB
	mu.Unlock()
	go func() { _, _ = getStatus(context.Background(), atA, "/things") }()
	go func() { _, _ = getStatus(context.Background(), atB, "/things") }()
	waitFor(t, gateA.entered, "a request in flight on endpoint \"a\"")
	waitFor(t, gateB.entered, "a request in flight on endpoint \"b\"")

	a.cancel()
	runErr := a.wait(t)
	if !errors.Is(runErr, ErrDrainTimeout) {
		t.Fatalf("a shutdown with a stuck request on each endpoint came back with %v, which "+
			"is not a drain timeout", runErr)
	}
	mustContain(t, runErr.Error(), "endpoint \"a\"", "the first endpoint that did not drain")
	mustContain(t, runErr.Error(), "endpoint \"b\"", "the second endpoint that did not drain")
	mustContain(t, runErr.Error(), "drain-timeout", "the key whose value the reader has to raise")
	for _, name := range []string{"a", "b"} {
		reported := fmt.Sprintf("endpoint %q still had requests in flight", name)
		if got := strings.Count(runErr.Error(), reported); got != 1 {
			t.Errorf("the drain timeout of endpoint %q is reported %d times, want once: %v",
				name, got, runErr)
		}
	}
}

// TestMiddlewareCycleFailsARealAssembly pins that a cycle between two layers
// registered by two modules ends the startup.
//
// It is the whole path rather than the solver: each probe really delivers the
// capability its layer stands for, each registers through the public Use, and
// each points at the other's capability. With Provides absent from the
// registration surface, neither constraint reaches a provider, both are
// dropped, the graph has no edge at all and this assembly starts up clean —
// which is the state this test exists to keep from coming back.
func TestMiddlewareCycleFailsARealAssembly(t *testing.T) {
	first := &stageProbe{
		name:     "probe.delivers-a",
		requires: requiresRouter(),
		provides: []core.Provision{{Token: (*capA)(nil)}},
		product:  deliveredA{},
		onInit: useLayer("public", Middleware{
			Name:     "layer-a",
			Provides: []core.Token{(*capA)(nil)},
			After:    []core.Token{(*capB)(nil)},
			Handler:  passThrough,
		}),
	}
	second := &stageProbe{
		name:     "probe.delivers-b",
		requires: requiresRouter(),
		provides: []core.Provision{{Token: (*capB)(nil)}},
		product:  deliveredB{},
		onInit: useLayer("public", Middleware{
			Name:     "layer-b",
			Provides: []core.Token{(*capB)(nil)},
			After:    []core.Token{(*capA)(nil)},
			Handler:  passThrough,
		}),
	}

	a := startAssembly(t, oneEndpoint("public"),
		Module(), first.module(), second.module())

	// stop rather than wait, for the reason given in the bind failure test:
	// an assembly that wrongly comes up then reports the nil it returned.
	runErr := a.stop(t)
	if !errors.Is(runErr, ErrMiddlewareCycle) {
		t.Fatalf("two layers each declared inside the other started up with %v; the "+
			"constraints are hard, so no order satisfies them and the startup has to end",
			runErr)
	}
	mustContain(t, runErr.Error(), "\"layer-a\"", "one member of the cycle")
	mustContain(t, runErr.Error(), "\"layer-b\"", "the other member of the cycle")
	mustContain(t, runErr.Error(), "\"public\"", "the endpoint the cycle is on")
}

// TestConstraintLandsOnItsProviderInARealAssembly pins the opposite direction:
// a constraint that does land decides the order, and Order does not get to
// overrule it.
//
// The two layers are given Order values pointing the other way, so a chain
// built by sorting on Order alone comes out reversed. That is what an
// assembly whose registration surface cannot say what a layer stands for
// falls back to, and it is the failure this pins against. Which of the two
// probes registers first does not enter into it: the constraint fixes the
// order either way, which is why no dependency is declared between them.
func TestConstraintLandsOnItsProviderInARealAssembly(t *testing.T) {
	tr := &trace{}
	serving := make(chan struct{}, 1)
	var (
		mu   sync.Mutex
		addr string
	)

	provider := &stageProbe{
		name:     "probe.delivers-a",
		requires: requiresRouter(),
		provides: []core.Provision{{Token: (*capA)(nil)}},
		product:  deliveredA{},
		onInit: inOrder(
			routeOn("public", "GET", "/things", filler),
			useLayer("public", Middleware{
				Name:     "outer-by-constraint",
				Provides: []core.Token{(*capA)(nil)},
				// A large Order asks for the innermost
				// position, which the constraint denies it.
				Order:   100,
				Handler: noteLayer(tr, "outer-by-constraint"),
			}),
		),
	}
	constrained := &stageProbe{
		name:     "probe.delivers-b",
		requires: requiresRouter(),
		provides: []core.Provision{{Token: (*capB)(nil)}},
		product:  deliveredB{},
		onInit: useLayer("public", Middleware{
			Name:     "inner-by-constraint",
			Provides: []core.Token{(*capB)(nil)},
			After:    []core.Token{(*capA)(nil)},
			// A small Order asks for the outermost position.
			Order:   -100,
			Handler: noteLayer(tr, "inner-by-constraint"),
		}),
		onServe: func(reg *core.Registry) error {
			r, err := routerFrom(reg)
			if err != nil {
				return err
			}
			got, err := boundAddr(r, "public")
			if err != nil {
				return err
			}
			mu.Lock()
			addr = got
			mu.Unlock()
			serving <- struct{}{}
			return nil
		},
	}

	a := startAssembly(t, oneEndpoint("public"),
		Module(), provider.module(), constrained.module())
	waitFor(t, serving, "the endpoint to bind")

	mu.Lock()
	at := addr
	mu.Unlock()
	code, err := getStatus(context.Background(), at, "/things")
	if err != nil {
		t.Fatalf("the bound endpoint refused a request: %v", err)
	}
	if code != nethttp.StatusNoContent {
		t.Errorf("the chain answered %d, not the %d the handler behind it writes",
			code, nethttp.StatusNoContent)
	}

	want := []string{"outer-by-constraint", "inner-by-constraint"}
	if got := tr.seen(); !equalStrings(got, want) {
		t.Errorf("the request went through %v; After places the second layer inside the "+
			"layer standing for the capability it names, so it has to be %v whatever the "+
			"two Order values ask for", got, want)
	}
	if err := a.stop(t); err != nil {
		t.Errorf("a run that served and was then cancelled came back with %v", err)
	}
}

// TestAGinHandlerLearnsTheDrainHasBegun pins that the drain signal reaches a
// handler through the gin context. The handler passes its *gin.Context to
// Draining as the context it is, and the signal is there because the gin
// context falls back to the request's context for values; a stream that waits
// on it ends when the shutdown begins, and the shutdown does not run out its
// drain-timeout waiting for a stream that never heard.
//
// The endpoint's drain-timeout is the wait deadline, so a handler that is never
// told leaves the run reporting a drain that ran out of time, or not returning
// before the deadline, and either is what this fails on.
func TestAGinHandlerLearnsTheDrainHasBegun(t *testing.T) {
	entered := make(chan struct{}, 1)
	told := make(chan bool, 1)
	serving := make(chan struct{})
	stream := &stageProbe{
		name:     "probe.registers-a-stream",
		requires: requiresRouter(),
		onInit: routeOn("public", "GET", "/stream", func(c *gin.Context) {
			signal := Draining(c)
			c.Status(nethttp.StatusOK)
			c.Writer.WriteHeaderNow()
			c.Writer.Flush()
			entered <- struct{}{}
			select {
			case <-signal:
				told <- signal != nil
			case <-time.After(waitDeadline):
				told <- false
			}
		}),
		onServe: func(*core.Registry) error {
			close(serving)
			return nil
		},
	}

	a := startAssembly(t, endpointsAt(waitDeadline, "public"), Module(), stream.module())
	a.waitForOrEnd(t, serving, "the endpoint to be served")
	r, err := routerFrom(a.reg)
	if err != nil {
		t.Fatal(err)
	}
	addr, err := boundAddr(r, "public")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = getStatus(context.Background(), addr, "/stream") }()
	a.waitForOrEnd(t, entered, "the stream to begin")

	started := time.Now()
	if err := a.stop(t); err != nil {
		t.Fatalf("a run whose stream ends on the drain signal came back with %v", err)
	}
	if !<-told {
		t.Error("the handler was never told that the drain had begun")
	}
	if waited := time.Since(started); waited >= waitDeadline {
		t.Errorf("the shutdown took %s, as long as the stream's own deadline, so the signal did not end it", waited)
	}
}

// TestRouteConflictFailsARealAssembly pins that a clash between two routes
// registered by two modules ends the startup. Each probe registers its own
// route through the public surface; neither registration fails, since a clash
// is not knowable from one of them, and the assembly's Serve is where gin
// refuses the second and the refusal becomes a startup failure that names both
// routes.
func TestRouteConflictFailsARealAssembly(t *testing.T) {
	first := &stageProbe{
		name:     "probe.registers-by-id",
		requires: requiresRouter(),
		onInit:   routeOn("public", "GET", "/things/:id", filler),
	}
	second := &stageProbe{
		name:     "probe.registers-by-name",
		requires: requiresRouter(),
		onInit:   routeOn("public", "GET", "/things/:name", filler),
	}

	a := startAssembly(t, oneEndpoint("public"), Module(), first.module(), second.module())

	// stop rather than wait: an assembly that wrongly comes up reports the nil it
	// returned instead of sitting out the deadline with nothing to say.
	runErr := a.stop(t)
	if !errors.Is(runErr, ErrRouteConflict) {
		t.Fatalf("two routes gin cannot mount together started up with %v; the startup has to end", runErr)
	}
	mustContain(t, runErr.Error(), `"GET /things/:id"`, "one of the two routes")
	mustContain(t, runErr.Error(), `"GET /things/:name"`, "the other route")
	mustContain(t, runErr.Error(), `endpoint "public"`, "the endpoint they are on")
}
