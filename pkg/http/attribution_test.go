package http

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
)

// TestAClashIsAttributedToTheMountedRouteItClashesWith pins the pair a conflict
// names. The routes mounted before the refused one include several that have
// nothing to do with it, and one that does; the failure has to name that one.
//
// The refused route is "GET /things/:name", and "GET /things/:id" is the one it
// clashes with. "POST /things/:id" is on the same path under another method and
// "GET /a" and "GET /b" are elsewhere, so a pair picked by the first or the last
// route mounted, or by the path alone, comes out different from the right one.
func TestAClashIsAttributedToTheMountedRouteItClashesWith(t *testing.T) {
	logger, _ := newRecordingLogger()
	_, err := assembleEndpoint(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Route("GET", "/a", filler)
		e.Route("POST", "/things/:id", filler)
		e.Route("GET", "/things/:id", filler)
		e.Route("GET", "/b", filler)
		e.Route("GET", "/things/:name", filler)
	})
	if !errors.Is(err, ErrRouteConflict) {
		t.Fatalf("the clash did not report ErrRouteConflict: %v", err)
	}
	mustContain(t, err.Error(), `the routes "GET /things/:id" and "GET /things/:name" cannot both be mounted`,
		"the pair that reproduces the refusal on an engine of its own")
	mustContain(t, err.Error(), `endpoint "public"`, "the endpoint the clash is on")
	for _, other := range []string{`"GET /a"`, `"GET /b"`, `"POST /things/:id"`} {
		if strings.Contains(err.Error(), other) {
			t.Errorf("the failure names %s, which the refused route does not clash with.\nThe message was: %s",
				other, err.Error())
		}
	}
}

// TestAClashUnderOneMethodIsNotAttributedToAnotherMethod is the same statement
// the other way round: the refused route is under POST, and the route with the
// same path under GET was mounted first. The two do not clash, because gin keeps
// a tree per method, and the partner is the POST route.
func TestAClashUnderOneMethodIsNotAttributedToAnotherMethod(t *testing.T) {
	logger, _ := newRecordingLogger()
	_, err := assembleEndpoint(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Route("GET", "/things/:id", filler)
		e.Route("POST", "/things/:id", filler)
		e.Route("POST", "/things/:name", filler)
	})
	if !errors.Is(err, ErrRouteConflict) {
		t.Fatalf("the clash did not report ErrRouteConflict: %v", err)
	}
	mustContain(t, err.Error(), `the routes "POST /things/:id" and "POST /things/:name" cannot both be mounted`,
		"the pair under the one method both of them are registered for")
}

// handlersOf is n route handlers.
func handlersOf(n int) []gin.HandlerFunc {
	handlers := make([]gin.HandlerFunc, n)
	for i := range handlers {
		handlers[i] = filler
	}
	return handlers
}

// ginHandlerLimit is the most handlers gin takes on one route registered on an
// engine of its own, found by trying rather than written down: the limit is
// gin's, and the test only needs a route that sits at it.
func ginHandlerLimit(t *testing.T) int {
	t.Helper()
	// The routes are tried on engines the test builds, and gin's debug output
	// for each of them goes to standard output unless the mode is settled.
	gin.SetMode(gin.ReleaseMode)
	for n := 1; n < 1000; n++ {
		if routeRefusal("GET", "/probe", handlersOf(n)) != nil {
			return n - 1
		}
	}
	t.Fatal("gin took a route of a thousand handlers, so there is no limit to sit a route at")
	return 0
}

// TestARefusalNoPairReproducesSaysSo pins the failure for a refusal that cannot
// be pinned on a pair of routes, which has to say so rather than name the one
// route this module is sure about as though that were the verdict.
//
// gin refuses a route whose handlers, with the endpoint's layers in front of
// them, exceed its limit. The route is accepted on an engine of its own, as it
// is when it is registered, and it is refused only in the company of the layer,
// so offering it with each mounted route in turn on an engine with no layers
// reproduces nothing. The route is the third mounted and the two before it are
// ordinary, which makes the count in the message meaningful.
//
// The discriminating assertions are the sentence that says the refusal was not
// attributed and the absence of the pair sentence: an implementation that falls
// back to pairing the refused route with the first or the last mounted one
// writes the pair instead.
func TestARefusalNoPairReproducesSaysSo(t *testing.T) {
	atTheLimit := handlersOf(ginHandlerLimit(t))

	logger, _ := newRecordingLogger()
	_, err := assembleEndpoint(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "layer", Handler: passThrough})
		e.Route("GET", "/one", filler)
		e.Route("GET", "/two", filler)
		e.Route("GET", "/heavy", atTheLimit...)
	})
	if !errors.Is(err, ErrRouteConflict) {
		t.Fatalf("a refused mount did not report ErrRouteConflict: %v", err)
	}
	mustContain(t, err.Error(), "could not be attributed to a pair of routes",
		"that no pair was named for this refusal")
	mustContain(t, err.Error(), `"GET /heavy"`,
		"the route gin refused, which is the one thing the mounting point knows")
	mustContain(t, err.Error(), "beside the 2 already mounted", "how many routes the refusal was weighed against")
	mustContain(t, err.Error(), `endpoint "public"`, "the endpoint the refusal is on")

	if strings.Contains(err.Error(), "cannot both be mounted") {
		t.Errorf("the failure states a pair, and no pair reproduces this refusal: naming one would "+
			"present a guess as the verdict.\nThe message was: %s", err.Error())
	}
	for _, partner := range []string{`"GET /one"`, `"GET /two"`} {
		if strings.Contains(err.Error(), partner) {
			t.Errorf("the failure names %s, and no mounted route is the refused route's partner.\n"+
				"The message was: %s", partner, err.Error())
		}
	}
}

// TestTheRefusalNoPairReproducesIsAMatterOfTheHandlerCount is the control for the
// test above: the same route with fewer handlers assembles under the same layer.
// What made the refusal is the number of handlers in the chain, the route's own
// and the endpoint's in front of them, and not the route's path or its method,
// which is what puts the refusal outside every pair of routes.
func TestTheRefusalNoPairReproducesIsAMatterOfTheHandlerCount(t *testing.T) {
	limit := ginHandlerLimit(t)

	assembles := func(handlers int) bool {
		logger, _ := newRecordingLogger()
		_, err := assembleEndpoint(t, testSettings("public"), nil, logger, func(e Endpoint) {
			e.Use(Middleware{Name: "layer", Handler: passThrough})
			e.Route("GET", "/heavy", handlersOf(handlers)...)
		})
		return err == nil
	}

	if !assembles(1) {
		t.Fatal("a route of one handler under one layer failed to assemble")
	}
	if assembles(limit) {
		t.Fatalf("a route of %d handlers, the most gin takes on a route alone, assembled under a layer: "+
			"the refusal the test above is built on does not happen", limit)
	}
}

// assembleWithCounts assembles an endpoint with that many layers and one route of
// that many handlers, and returns what assembling answered. A panic that gets out
// of the assembly fails the test where it is raised: in Serve a panic skips the
// registry's rollback and strands every endpoint already bound, so a chain gin
// has no room for has to come back as an error.
func assembleWithCounts(t *testing.T, layers, handlers int) error {
	t.Helper()
	defer func() {
		if raised := recover(); raised != nil {
			t.Fatalf("assembling %d layers and a route of %d handlers panicked with %v; in Serve a "+
				"panic skips the registry's rollback and strands the endpoints already bound",
				layers, handlers, raised)
		}
	}()
	logger, _ := newRecordingLogger()
	_, err := assembleEndpoint(t, testSettings("public"), nil, logger, func(e Endpoint) {
		for i := range layers {
			e.Use(Middleware{Name: fmt.Sprintf("layer-%d", i), Handler: passThrough})
		}
		e.Route("GET", "/things", handlersOf(handlers)...)
	})
	return err
}

// layerLimit is the most layers an endpoint takes with a route of one handler,
// found by trying: what gin holds in a chain is gin's, and what the endpoint's own
// engine puts in it is this module's, so the number is read off the assembly
// rather than written down.
func layerLimit(t *testing.T) int {
	t.Helper()
	for n := 1; n < 500; n++ {
		if err := assembleWithCounts(t, n, 1); err != nil {
			if !errors.Is(err, ErrRouteConflict) {
				t.Fatalf("%d layers failed the assembly with %v, which is not ErrRouteConflict", n, err)
			}
			return n - 1
		}
	}
	t.Fatal("an endpoint took five hundred layers, so there is no limit to go over")
	return 0
}

// TestTooManyLayersFailTheAssembly pins what an endpoint does with more layers
// than a chain holds. gin refuses them by panicking as they are handed to its
// Use, which happens in Serve; the refusal comes back as a startup failure
// carrying ErrRouteConflict, naming the endpoint and the number of layers, and
// the fix: fewer layers on the endpoint, or some of them on another one.
//
// It is read in a whole assembly as well, where a panic in Serve would take the
// process down with every endpoint already bound still listening.
func TestTooManyLayersFailTheAssembly(t *testing.T) {
	limit := layerLimit(t)

	err := assembleWithCounts(t, limit+1, 1)
	if !errors.Is(err, ErrRouteConflict) {
		t.Fatalf("%d layers, one more than the endpoint takes, came back with %v, want a failure "+
			"wrapping ErrRouteConflict", limit+1, err)
	}
	mustContain(t, err.Error(), fmt.Sprintf("%d registered layers", limit+1), "how many layers were handed over")
	mustContain(t, err.Error(), `endpoint "public"`, "the endpoint that holds them")
	mustContain(t, err.Error(), "register fewer layers", "the fixing action")

	layers := make([]func(*core.Registry) error, 0, limit+1)
	for i := range limit + 1 {
		layers = append(layers, useLayer("public", Middleware{Name: fmt.Sprintf("layer-%d", i), Handler: passThrough}))
	}
	probe := &stageProbe{name: "probe.registers-every-layer", requires: requiresRouter(), onInit: inOrder(layers...)}
	a := startAssembly(t, oneEndpoint("public"), Module(), probe.module())
	if runErr := a.stop(t); !errors.Is(runErr, ErrRouteConflict) {
		t.Errorf("an assembly whose registrants handed one more layer than the endpoint takes came "+
			"back with %v, want a startup failure wrapping ErrRouteConflict", runErr)
	}
}

// TestLayersAndARouteTogetherOverTheLimitFailTheAssembly pins the other way a
// chain outgrows what gin holds: every layer fits and the route fits alone, and
// the two together do not. The route is the one that is refused, in the company
// of the layers, which no pair of routes reproduces, so the failure says the
// refusal could not be attributed to a pair, and it is again an error and not a
// panic.
//
// The route's size is found by trying, with one layer fewer than the limit:
// the largest route that assembles there is the control, and the route one
// handler longer is the case.
func TestLayersAndARouteTogetherOverTheLimitFailTheAssembly(t *testing.T) {
	layers := layerLimit(t) - 1

	largest := 0
	for handlers := 1; handlers <= ginHandlerLimit(t); handlers++ {
		if err := assembleWithCounts(t, layers, handlers); err != nil {
			break
		}
		largest = handlers
	}
	if largest < 1 || largest >= ginHandlerLimit(t) {
		t.Fatalf("with %d layers the largest route that assembles has %d handlers, so there is no route "+
			"the layers leave too little room for", layers, largest)
	}

	err := assembleWithCounts(t, layers, largest+1)
	if !errors.Is(err, ErrRouteConflict) {
		t.Fatalf("a route of %d handlers under %d layers came back with %v, want a failure wrapping "+
			"ErrRouteConflict", largest+1, layers, err)
	}
	mustContain(t, err.Error(), "could not be attributed to a pair of routes", "that no pair was named")
	mustContain(t, err.Error(), `"GET /things"`, "the route gin refused")
}
