package http

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
)

// TestAClashIsAttributedToTheMountedRouteItClashesWith pins the pair a conflict names.
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

// TestAClashUnderOneMethodIsNotAttributedToAnotherMethod is the same statement the other way round:
// the refused route is under POST, and the route with the same path under GET was mounted first.
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

func handlersOf(n int) []gin.HandlerFunc {
	handlers := make([]gin.HandlerFunc, n)
	for i := range handlers {
		handlers[i] = filler
	}
	return handlers
}

func ginHandlerLimit(t *testing.T) int {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	for n := 1; n < 1000; n++ {
		if routeRefusal("GET", "/probe", handlersOf(n)) != nil {
			return n - 1
		}
	}
	t.Fatal("gin took a route of a thousand handlers, so there is no limit to sit a route at")
	return 0
}

// TestARefusalNoPairReproducesSaysSo pins the failure for a refusal that cannot be pinned on a pair
// of routes.
func TestARefusalNoPairReproducesSaysSo(t *testing.T) {
	atTheLimit := handlersOf(ginHandlerLimit(t))

	logger, _ := newRecordingLogger()
	_, err := assembleEndpoint(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "layer", Stage: StageObserve, Handler: passThrough})
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

// TestTheRefusalNoPairReproducesIsAMatterOfTheHandlerCount is the control for the test above: the
// same route with fewer handlers assembles under the same layer.
func TestTheRefusalNoPairReproducesIsAMatterOfTheHandlerCount(t *testing.T) {
	limit := ginHandlerLimit(t)

	assembles := func(handlers int) bool {
		logger, _ := newRecordingLogger()
		_, err := assembleEndpoint(t, testSettings("public"), nil, logger, func(e Endpoint) {
			e.Use(Middleware{Name: "layer", Stage: StageObserve, Handler: passThrough})
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

// assembleWithCounts assembles an endpoint with that many layers and one route of that many handlers, and returns what assembling answered. A panic out of the assembly fails the test where it is raised: in Serve a panic skips the registry's rollback, so a chain gin has no room for has to come back as an error.
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
			e.Use(Middleware{Name: fmt.Sprintf("layer-%d", i), Stage: StageObserve, Handler: passThrough})
		}
		e.Route("GET", "/things", handlersOf(handlers)...)
	})
	return err
}

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

// TestTooManyLayersFailTheAssembly pins what an endpoint does with more layers than a chain holds.
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
		layers = append(layers, useLayer("public", Middleware{Name: fmt.Sprintf("layer-%d", i), Stage: StageObserve, Handler: passThrough}))
	}
	probe := &stageProbe{name: "probe.registers-every-layer", requires: requiresRouter(), onInit: inOrder(layers...)}
	a := startAssembly(t, oneEndpoint("public"), Module(), probe.module())
	if runErr := a.stop(t); !errors.Is(runErr, ErrRouteConflict) {
		t.Errorf("an assembly whose registrants handed one more layer than the endpoint takes came "+
			"back with %v, want a startup failure wrapping ErrRouteConflict", runErr)
	}
}

// TestLayersAndARouteTogetherOverTheLimitFailTheAssembly pins the other way a chain outgrows what
// gin holds: every layer fits and the route fits alone, and the two together do not.
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
