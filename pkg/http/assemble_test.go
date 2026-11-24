package http

import (
	"errors"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
)

func assembleEndpoint(t *testing.T, s endpointSettings, injected, logger *slog.Logger,
	register func(Endpoint),
) (nethttp.Handler, error) {
	t.Helper()
	return assembleEndpointWith(t, s, injected, logger, nil, nil, register)
}

func assembleEndpointWith(t *testing.T, s endpointSettings, injected, logger *slog.Logger,
	responder ErrorResponder, localizer i18n.Localizer, register func(Endpoint),
) (nethttp.Handler, error) {
	t.Helper()
	return assembleEndpointWithResolver(t, s, injected, logger, responder, localizer, nil, register)
}

func assembleEndpointWithResolver(t *testing.T, s endpointSettings, injected, logger *slog.Logger,
	responder ErrorResponder, localizer i18n.Localizer, resolver i18n.Resolver, register func(Endpoint),
) (nethttp.Handler, error) {
	t.Helper()
	r := newRouter([]listenerSettings{testListener(s.listenerName)}, []endpointSettings{s})
	r.open()
	register(endpointOf(t, r, s.name))
	r.seal()
	return r.endpoints[s.name].assemble(injected, logger, responder, localizer, resolver)
}

func mustAssemble(t *testing.T, s endpointSettings, injected, logger *slog.Logger,
	register func(Endpoint),
) nethttp.Handler {
	t.Helper()
	handler, err := assembleEndpoint(t, s, injected, logger, register)
	if err != nil {
		t.Fatalf("assembling endpoint %q: %v", s.name, err)
	}
	return handler
}

type trace struct {
	mu     sync.Mutex
	visits []string
}

func (tr *trace) note(name string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.visits = append(tr.visits, name)
}

func (tr *trace) seen() []string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]string(nil), tr.visits...)
}

func noteLayer(tr *trace, name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tr.note(name)
		c.Next()
	}
}

func noteHandler(tr *trace, name string) gin.HandlerFunc {
	return func(*gin.Context) { tr.note(name) }
}

// TestRouteConflictIsStartupFailure pins the conversion at the mounting point.
func TestRouteConflictIsStartupFailure(t *testing.T) {
	logger, _ := newRecordingLogger()
	_, err := assembleEndpoint(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Route("GET", "/things", filler)
		e.Route("GET", "/things", filler)
	})
	if !errors.Is(err, ErrRouteConflict) {
		t.Fatalf("two registrations of one method and path did not report ErrRouteConflict: %v", err)
	}
	mustContain(t, err.Error(), `"GET /things" and "GET /things"`, "the registration the second one repeats")
	mustContain(t, err.Error(), `endpoint "public"`, "the endpoint the clash is on")
}

// TestEveryClashGinRefusesIsARouteConflict pins each class of route clash as a
// startup failure of its own: the same position given two parameter names.
func TestEveryClashGinRefusesIsARouteConflict(t *testing.T) {
	for _, c := range []struct {
		name          string
		first, second [2]string
	}{
		{"a parameter named differently", [2]string{"GET", "/things/:id"}, [2]string{"GET", "/things/:name"}},
		{"a method and path twice", [2]string{"POST", "/things"}, [2]string{"POST", "/things"}},
		{"a catch-all after a static segment", [2]string{"GET", "/files/a"}, [2]string{"GET", "/files/*rest"}},
		{"a static segment after a catch-all", [2]string{"GET", "/files/*rest"}, [2]string{"GET", "/files/a"}},
		{"a catch-all after a parameter", [2]string{"GET", "/files/:name"}, [2]string{"GET", "/files/*rest"}},
		{"a parameter after a catch-all", [2]string{"GET", "/files/*rest"}, [2]string{"GET", "/files/:name"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			logger, _ := newRecordingLogger()
			_, err := assembleEndpoint(t, testSettings("public"), nil, logger, func(e Endpoint) {
				e.Route(c.first[0], c.first[1], filler)
				e.Route(c.second[0], c.second[1], filler)
			})
			if !errors.Is(err, ErrRouteConflict) {
				t.Fatalf("the clash did not report ErrRouteConflict: %v", err)
			}
			pair := `"` + c.first[0] + " " + c.first[1] + `" and "` + c.second[0] + " " + c.second[1] + `"`
			mustContain(t, err.Error(), pair, "both routes, named together, which is the whole of the way "+
				"back to the two calls")
			mustContain(t, err.Error(), `endpoint "public"`, "the endpoint the clash is on")
		})
	}
}

// TestRoutesGinAcceptsBesideEachOtherAreServed pins the three pairings that are not conflicts, by
// what the assembled endpoint answers for each: a static segment beside a parameter.
func TestRoutesGinAcceptsBesideEachOtherAreServed(t *testing.T) {
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Route("GET", "/files/a", func(c *gin.Context) { c.String(nethttp.StatusOK, "static") })
		e.Route("GET", "/files/:name", func(c *gin.Context) { c.String(nethttp.StatusOK, "param:"+c.Param("name")) })
		e.Route("POST", "/files/:id", func(c *gin.Context) { c.String(nethttp.StatusOK, "post:"+c.Param("id")) })
	})

	for _, c := range []struct{ method, target, want string }{
		{nethttp.MethodGet, "/files/a", "static"},
		{nethttp.MethodGet, "/files/b", "param:b"},
		{nethttp.MethodPost, "/files/a", "post:a"},
	} {
		recorder := serveOnce(handler, c.method, c.target)
		if got := recorder.Body.String(); got != c.want {
			t.Errorf("%s %s answered %q, want %q", c.method, c.target, got, c.want)
		}
	}
}

// TestARouteRunsItsHandlersInOrder pins that a route's several handlers run one after another, in
// the order they were given and after the layers, and that the last one is what serves the request.
func TestARouteRunsItsHandlersInOrder(t *testing.T) {
	tr := &trace{}
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "layer", Stage: StageObserve, Handler: noteLayer(tr, "layer")})
		e.Route("GET", "/things",
			func(c *gin.Context) { tr.note("first"); c.Next() },
			func(c *gin.Context) { tr.note("second"); c.Next() },
			func(c *gin.Context) { tr.note("third"); c.String(nethttp.StatusTeapot, "served") },
		)
	})

	recorder := serveOnce(handler, nethttp.MethodGet, "/things")

	if got, want := tr.seen(), []string{"layer", "first", "second", "third"}; !slices.Equal(got, want) {
		t.Errorf("the request went through %v, want %v", got, want)
	}
	if recorder.Code != nethttp.StatusTeapot || recorder.Body.String() != "served" {
		t.Errorf("the last handler's answer was %d %q, want %d %q", recorder.Code,
			recorder.Body.String(), nethttp.StatusTeapot, "served")
	}
}

// TestAbortInARouteHandlerStopsTheHandlersAfterIt pins what c.Abort does to a route's own chain,
// which is what lets the first handler of a route act as a guard for the rest.
func TestAbortInARouteHandlerStopsTheHandlersAfterIt(t *testing.T) {
	tr := &trace{}
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Route("GET", "/things",
			func(c *gin.Context) { tr.note("guard"); c.AbortWithStatus(nethttp.StatusForbidden) },
			noteHandler(tr, "guarded"),
		)
	})

	recorder := serveOnce(handler, nethttp.MethodGet, "/things")

	if got, want := tr.seen(), []string{"guard"}; !slices.Equal(got, want) {
		t.Errorf("the handlers that ran were %v, want only %v: Abort ends the chain", got, want)
	}
	if recorder.Code != nethttp.StatusForbidden {
		t.Errorf("the guard's status was %d, want %d", recorder.Code, nethttp.StatusForbidden)
	}
}

// TestALayerReadsTheOutcomeAfterNext pins what the surface offers a layer that wants to see how the
// request ended: the status, whether anything was written and how many bytes.
func TestALayerReadsTheOutcomeAfterNext(t *testing.T) {
	var status, size int
	var written bool
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "observer", Stage: StageObserve, Handler: func(c *gin.Context) {
			c.Next()
			status, size, written = c.Writer.Status(), c.Writer.Size(), c.Writer.Written()
		}})
		e.Route("GET", "/things", func(c *gin.Context) { c.String(nethttp.StatusCreated, "abc") })
	})

	serveOnce(handler, nethttp.MethodGet, "/things")

	if status != nethttp.StatusCreated || size != 3 || !written {
		t.Errorf("the layer read status %d, size %d, written %v after Next; the handler wrote "+
			"%d and 3 bytes", status, size, written, nethttp.StatusCreated)
	}
}

// TestConstraintThroughUseOrdersAgainstItsProvider pins that a constraint declared the only way a
// registrant can declare one — through Use — reaches the layer it points at.
func TestConstraintThroughUseOrdersAgainstItsProvider(t *testing.T) {
	tr := &trace{}
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{
			Name:     "auth",
			Stage:    StageIdentify,
			Order:    100,
			Provides: []core.Token{(*capAuth)(nil)},
			Handler:  noteLayer(tr, "auth"),
		})
		e.Use(Middleware{
			Name:    "tenant",
			Stage:   StageIdentify,
			Order:   -100,
			After:   []core.Token{(*capAuth)(nil)},
			Handler: noteLayer(tr, "tenant"),
		})
		e.Route("GET", "/things", filler)
	})

	serveOnce(handler, nethttp.MethodGet, "/things")

	want := []string{"auth", "tenant"}
	if got := tr.seen(); !slices.Equal(got, want) {
		t.Errorf("the chain ran in the order %v, want %v: After outranks Order, and %v is what "+
			"comes out when the constraint finds nothing to bind to", got, want,
			[]string{"tenant", "auth"})
	}
}

// TestEmptyProvidesStaysLegal pins that a layer standing for nothing is a legal
// registration: it is ordered with the rest and it is in the chain.
func TestEmptyProvidesStaysLegal(t *testing.T) {
	tr := &trace{}
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "anonymous", Stage: StageObserve, Handler: noteLayer(tr, "anonymous")})
		e.Use(Middleware{
			Name:     "auth",
			Stage:    StageObserve,
			Provides: []core.Token{(*capAuth)(nil)},
			Handler:  noteLayer(tr, "auth"),
		})
		e.Route("GET", "/things", filler)
	})

	serveOnce(handler, nethttp.MethodGet, "/things")

	want := []string{"anonymous", "auth"}
	if got := tr.seen(); !slices.Equal(got, want) {
		t.Errorf("the chain ran in the order %v, want %v: a layer that names no capability of "+
			"its own is still ordered and still runs", got, want)
	}
}

// TestUnlandedConstraintIsReported pins that a constraint no layer lands is reported.
func TestUnlandedConstraintIsReported(t *testing.T) {
	logger, records := newRecordingLogger()
	_, err := assembleEndpoint(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{
			Name:     "auth",
			Stage:    StageIdentify,
			Provides: []core.Token{(*capAuth)(nil)},
			Handler:  passThrough,
		})
		e.Use(Middleware{
			Name:    "tenant",
			Stage:   StageIdentify,
			After:   []core.Token{(*capAuth)(nil)},
			Handler: passThrough,
		})
		e.Use(Middleware{
			Name:    "audit",
			Stage:   StageIdentify,
			After:   []core.Token{(*capQuota)(nil)},
			Handler: passThrough,
		})
		e.Use(Middleware{
			Name:     "recover",
			Stage:    StageIdentify,
			Provides: []core.Token{(*capRecovery)(nil)},
			After:    []core.Token{(*capRecovery)(nil)},
			Handler:  passThrough,
		})
		e.Route("GET", "/things", filler)
	})
	if err != nil {
		t.Fatalf("a constraint that placed nothing must not fail the assembly: %v", err)
	}

	informed := unlandedLines(records.at(slog.LevelInfo))
	warned := unlandedLines(records.at(slog.LevelWarn))
	written := strings.Join(append(slices.Clone(informed), warned...), "\n")
	if len(informed)+len(warned) == 0 {
		t.Fatal("nothing was written about the constraints that placed nothing, so the layers " +
			"that declared them run where their stage and position put them with no signal at all")
	}
	if strings.Contains(written, "middleware=tenant") {
		t.Errorf("the report lists tenant, whose constraint found a provider and placed it. "+
			"Listing the constraints that worked buries the ones that did not:\n%s", written)
	}

	absent := lineFor(informed, "middleware=audit")
	self := lineFor(warned, "middleware=recover")
	if absent == "" || self == "" {
		t.Fatalf("the diagnostics were\nInfo:\n%s\nWarn:\n%s\nwant audit's at Info, since a "+
			"capability nobody on the endpoint stands for is a legal configuration, and recover's "+
			"at Warn, since a declaration naming only its own layer is wrong in every assembly",
			strings.Join(informed, "\n"), strings.Join(warned, "\n"))
	}
	if len(warned) != 1 {
		t.Errorf("the warnings were\n%s\nwant only recover's: the other unplaced constraint is one "+
			"a legal assembly produces", strings.Join(warned, "\n"))
	}

	mustContain(t, absent, typeName(reflect.TypeOf((*capQuota)(nil)).Elem()),
		"the capability nobody stands for")
	mustContain(t, absent, "After[0]", "which declaration on that layer")
	mustContain(t, absent, `endpoint=public`, "the endpoint the declaration was made on")
	mustContain(t, absent, "cause="+string(causeNoProvider),
		"the cause of a capability no layer on this endpoint stands for")

	mustContain(t, self, typeName(reflect.TypeOf((*capRecovery)(nil)).Elem()),
		"the capability the declaring layer stands for on its own")
	mustContain(t, self, "cause="+string(causeSelfOnly),
		"the cause of a layer standing only for the capability it names: nobody is absent here, "+
			"the declaration is what is wrong")
}

// TestOrderingRemarksAreWrittenAtTheirOwnLevels pins that a remark reaches the log with the words
// and the level its cause calls for.
func TestOrderingRemarksAreWrittenAtTheirOwnLevels(t *testing.T) {
	logger, records := newRecordingLogger()
	_, err := assembleEndpoint(t, testSettings("public"), nil, logger, func(e Endpoint) {
		// A constraint that crosses stages in the direction the stages already give: throttle sits inside identify, so this After repeats the boundary rather than asking for a place the boundary does not already give.
		e.Use(Middleware{
			Name:     "gate",
			Stage:    StageIdentify,
			Provides: []core.Token{(*capAuth)(nil)},
			Handler:  passThrough,
		})
		e.Use(Middleware{
			Name:    "quota",
			Stage:   StageThrottle,
			After:   []core.Token{(*capAuth)(nil)},
			Handler: passThrough,
		})
		e.Route("GET", "/things", filler)
	})
	if err != nil {
		t.Fatalf("assembly failed: %v", err)
	}

	satisfied := lineFor(records.at(slog.LevelDebug), "middleware=quota")
	if satisfied == "" {
		t.Fatalf("the cross-stage constraint the stage order already gives was not written at "+
			"Debug; Debug carries\n%s", strings.Join(records.at(slog.LevelDebug), "\n"))
	}
	mustContain(t, satisfied, "already given by the stage order",
		"the words of a constraint the boundary already satisfies, not the words of one that landed "+
			"on no layer")
	mustContain(t, satisfied, "cause="+string(causeStageSatisfied), "the cause of the remark")
}

// TestALegalAssemblyStartsWithoutAWarning pins the shape a host with an unguarded endpoint
// produces.
func TestALegalAssemblyStartsWithoutAWarning(t *testing.T) {
	logger, records := newRecordingLogger()
	_, err := assembleEndpoint(t, testSettings("admin"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{
			Name:     "gate",
			Stage:    StageIdentify,
			Provides: []core.Token{(*capAuth)(nil)},
			Handler:  passThrough,
		})
		e.Use(Middleware{
			Name:    "journal",
			Stage:   StageIdentify,
			After:   []core.Token{(*capAuth)(nil), (*capTenant)(nil)},
			Handler: passThrough,
		})
		e.Route("GET", "/tenants", filler)
	})
	if err != nil {
		t.Fatalf("a legal assembly failed: %v", err)
	}

	if warned := records.at(slog.LevelWarn); len(warned) != 0 {
		t.Errorf("a legal assembly wrote warnings at startup:\n%s", strings.Join(warned, "\n"))
	}
	informed := unlandedLines(records.at(slog.LevelInfo))
	if len(informed) != 1 {
		t.Fatalf("the unplaced constraints written at Info were\n%s\nwant exactly journal's After[1]",
			strings.Join(informed, "\n"))
	}
	line := informed[0]
	mustContain(t, line, "middleware=journal", "the layer that declared the constraint")
	mustContain(t, line, "declaration=After[1]", "which declaration on that layer placed nothing")
	mustContain(t, line, "endpoint=admin", "the endpoint the declaration was made on")
	mustContain(t, line, typeName(reflect.TypeOf((*capTenant)(nil)).Elem()),
		"the capability no layer on this endpoint stands for")
	mustContain(t, line, "cause="+string(causeNoProvider), "why the constraint placed nothing")
}

func unlandedLines(lines []string) []string {
	var out []string
	for _, line := range lines {
		if strings.HasPrefix(line, unlandedMsg) {
			out = append(out, line)
		}
	}
	return out
}

func lineFor(lines []string, want string) string {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return line
		}
	}
	return ""
}

// TestCycleThroughUseFailsTheAssembly pins that a cycle is reachable through the public surface at
// all.
func TestCycleThroughUseFailsTheAssembly(t *testing.T) {
	logger, _ := newRecordingLogger()
	_, err := assembleEndpoint(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{
			Name:     "a",
			Stage:    StageIdentify,
			Provides: []core.Token{(*capAuth)(nil)},
			After:    []core.Token{(*capTenant)(nil)},
			Handler:  passThrough,
		})
		e.Use(Middleware{
			Name:     "b",
			Stage:    StageIdentify,
			Provides: []core.Token{(*capTenant)(nil)},
			After:    []core.Token{(*capAuth)(nil)},
			Handler:  passThrough,
		})
		e.Route("GET", "/things", filler)
	})
	if !errors.Is(err, ErrMiddlewareCycle) {
		t.Fatalf("two layers each declaring itself inside the other did not fail the assembly: %v", err)
	}
	mustContain(t, err.Error(), `"a"`, "one member of the cycle")
	mustContain(t, err.Error(), `"b"`, "the other member of the cycle")
	mustContain(t, err.Error(), `endpoint "public"`, "the endpoint the cycle is on")
}

// TestChainOrderIsOutermostFirst pins that the chain runs in the order the ordering gives, rather
// than in registration order or its reverse.
func TestChainOrderIsOutermostFirst(t *testing.T) {
	tr := &trace{}
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "inner", Stage: StageObserve, Order: 100, Handler: noteLayer(tr, "inner")})
		e.Use(Middleware{Name: "middle", Stage: StageObserve, Handler: noteLayer(tr, "middle")})
		e.Use(Middleware{Name: "outer", Stage: StageObserve, Order: -100, Handler: noteLayer(tr, "outer")})
		e.Route("GET", "/things", noteHandler(tr, "handler"))
	})

	serveOnce(handler, nethttp.MethodGet, "/things")

	want := []string{"outer", "middle", "inner", "handler"}
	if got := tr.seen(); !slices.Equal(got, want) {
		t.Errorf("the chain ran in the order %v, and the ordering gives %v", got, want)
	}
}

// TestALayerRegisteredAfterTheRoutesStillRunsForThem pins the order the engine is built in.
func TestALayerRegisteredAfterTheRoutesStillRunsForThem(t *testing.T) {
	tr := &trace{}
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Route("GET", "/things", noteHandler(tr, "handler"))
		e.Use(Middleware{Name: "late", Stage: StageObserve, Handler: noteLayer(tr, "late")})
	})

	serveOnce(handler, nethttp.MethodGet, "/things")

	if got, want := tr.seen(), []string{"late", "handler"}; !slices.Equal(got, want) {
		t.Errorf("the request went through %v, want %v: a layer applies to every route of the "+
			"endpoint whatever order the registrations arrived in", got, want)
	}
}

// TestBodyLimitIsAppliedFromTheIdentifyStageInwards checks that the first identify-stage layer and
// every layer below it read a bounded body.
func TestBodyLimitIsAppliedFromTheIdentifyStageInwards(t *testing.T) {
	logger, _ := newRecordingLogger()
	s := testSettings("public")
	s.maxBodyBytes = 8

	var readErr error
	var read []byte
	handler := mustAssemble(t, s, nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "identified", Stage: StageIdentify, Handler: func(c *gin.Context) {
			read, readErr = io.ReadAll(c.Request.Body)
			c.Next()
		}})
		e.Route("POST", "/things", filler)
	})

	body := io.NopCloser(strings.NewReader(strings.Repeat("x", 64)))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(nethttp.MethodPost, "/things", body))

	var tooLarge *nethttp.MaxBytesError
	if !errors.As(readErr, &tooLarge) {
		t.Fatalf("an identify-stage layer read %d bytes with error %v; the limit was not imposed "+
			"outside it", len(read), readErr)
	}
	if tooLarge.Limit != s.maxBodyBytes {
		t.Errorf("the limit imposed was %d, and the endpoint is configured with %d",
			tooLarge.Limit, s.maxBodyBytes)
	}
}

// TestGuardStageIsOutsideTheBodyLimit pins the place the body limit was moved to.
func TestGuardStageIsOutsideTheBodyLimit(t *testing.T) {
	logger, _ := newRecordingLogger()
	s := testSettings("public")
	s.maxBodyBytes = 8

	var corsRan, limitRan bool
	handler := mustAssemble(t, s, nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "cors", Stage: StageGuard, Order: OrderOutermost, Handler: func(c *gin.Context) {
			corsRan = true
			c.Header("Access-Control-Allow-Origin", "https://example.test")
			c.Next()
		}})
		e.Use(Middleware{Name: "address-limit", Stage: StageGuard, Order: OrderInnermost, Handler: func(c *gin.Context) {
			limitRan = true
			if c.Request.URL.Path == "/limited" {
				Fail(c, Problem{Status: nethttp.StatusTooManyRequests})
				return
			}
			c.Next()
		}})
		e.Route("POST", "/things", filler)
		e.Route("GET", "/limited", filler)
	})

	limited := serveOnce(handler, nethttp.MethodGet, "/limited")
	if limited.Code != nethttp.StatusTooManyRequests {
		t.Fatalf("the address limit answered %d, want %d", limited.Code, nethttp.StatusTooManyRequests)
	}
	if limited.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Errorf("the 429 carries no Access-Control-Allow-Origin, so CORS was not in front of the " +
			"address limit")
	}

	corsRan, limitRan = false, false
	body := strings.NewReader(strings.Repeat("x", 64))
	oversized := httptest.NewRecorder()
	handler.ServeHTTP(oversized, httptest.NewRequest(nethttp.MethodPost, "/things", body))
	if oversized.Code != nethttp.StatusRequestEntityTooLarge {
		t.Fatalf("an oversize declared body answered %d, want %d",
			oversized.Code, nethttp.StatusRequestEntityTooLarge)
	}
	if oversized.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Errorf("the 413 carries no Access-Control-Allow-Origin, so the body limit was not placed " +
			"inside the guard stage: outside CORS a browser cannot read the refusal")
	}
	if !corsRan {
		t.Errorf("the cors layer did not run before the body limit refused, which is the same thing " +
			"seen from the other side")
	}
	if !limitRan {
		t.Errorf("the address limit did not run before the body limit refused, so an oversize request " +
			"escapes the address limit; the body limit is in front of the guard stage")
	}
}

// TestAnOversizedBodyBindsAsAMaxBytesError pins what a handler sees when it binds a body through
// gin: the limit the endpoint imposes comes back from ShouldBindJSON as an *http.MaxBytesError.
func TestAnOversizedBodyBindsAsAMaxBytesError(t *testing.T) {
	logger, _ := newRecordingLogger()
	s := testSettings("public")
	s.maxBodyBytes = 32

	var bound struct {
		Name string `json:"name"`
	}
	var bindErr error
	handler := mustAssemble(t, s, nil, logger, func(e Endpoint) {
		e.Route("POST", "/things", func(c *gin.Context) { bindErr = c.ShouldBindJSON(&bound) })
	})

	send := func(body string) {
		bound.Name, bindErr = "", nil
		handler.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(nethttp.MethodPost, "/things", io.NopCloser(strings.NewReader(body))))
	}

	send(`{"name":"` + strings.Repeat("x", 64) + `"}`)
	var tooLarge *nethttp.MaxBytesError
	if !errors.As(bindErr, &tooLarge) {
		t.Fatalf("binding a body over the limit returned %v, want an error errors.As reaches as "+
			"*http.MaxBytesError", bindErr)
	}
	if tooLarge.Limit != s.maxBodyBytes {
		t.Errorf("the limit the error carries is %d, and the endpoint is configured with %d",
			tooLarge.Limit, s.maxBodyBytes)
	}

	send(`{"name":"within"}`)
	if bindErr != nil || bound.Name != "within" {
		t.Errorf("a body inside the limit bound as %q with error %v", bound.Name, bindErr)
	}
}

// TestNoBodyLimitIsLeftUnlimited pins the switch-off: an explicit 0 means no limit, so the body
// arrives whole.
func TestNoBodyLimitIsLeftUnlimited(t *testing.T) {
	logger, _ := newRecordingLogger()
	s := testSettings("public")
	s.maxBodyBytes = 0

	var read []byte
	var readErr error
	handler := mustAssemble(t, s, nil, logger, func(e Endpoint) {
		e.Route("POST", "/things", func(c *gin.Context) { read, readErr = io.ReadAll(c.Request.Body) })
	})

	body := strings.NewReader(strings.Repeat("x", 64))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(nethttp.MethodPost, "/things", body))

	if readErr != nil || len(read) != 64 {
		t.Errorf("a body of 64 bytes arrived as %d bytes with error %v, and the endpoint sets no limit",
			len(read), readErr)
	}
}

// TestLoggerInjectedOutsideEveryLayer pins the injection point.
func TestLoggerInjectedOutsideEveryLayer(t *testing.T) {
	logger, _ := newRecordingLogger()
	injected, records := newRecordingLogger()

	var seen *slog.Logger
	handler := mustAssemble(t, testSettings("public"), injected, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "outermost", Stage: StageObserve, Handler: func(c *gin.Context) {
			seen = log.FromContext(c.Request.Context())
			c.Next()
		}})
		e.Route("GET", "/things", filler)
	})

	serveOnce(handler, nethttp.MethodGet, "/things")

	if seen != injected {
		t.Fatal("the outermost layer did not take the injected logger out of the request context")
	}
	const layerMsg = "written from the outermost layer"
	seen.Info(layerMsg)
	if _, written := records.first(layerMsg); !written {
		t.Error("what the layer took out of the context does not write to the injected destination")
	}
}

// TestNoLoggerModuleLeavesContextUntouched pins the accepted cost of the dependency being optional:
// with no Logger capability nothing is injected and nothing fails.
func TestNoLoggerModuleLeavesContextUntouched(t *testing.T) {
	logger, _ := newRecordingLogger()

	var seen *slog.Logger
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Route("GET", "/things", func(c *gin.Context) { seen = log.FromContext(c.Request.Context()) })
	})

	serveOnce(handler, nethttp.MethodGet, "/things")

	if seen != log.Default() {
		t.Error("something was injected into the request context although no module delivers Logger")
	}
}

// TestAssembledChainIsListed pins that the chain a run really serves is written down at startup, in
// the order it runs.
func TestAssembledChainIsListed(t *testing.T) {
	logger, records := newRecordingLogger()
	mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "inner", Stage: StageObserve, Handler: passThrough})
		e.Use(Middleware{
			Name:     "outer",
			Stage:    StageObserve,
			Order:    -100,
			Provides: []core.Token{(*capAuth)(nil), (*capTenant)(nil)},
			Handler:  passThrough,
		})
		e.Route("GET", "/things", filler)
	})

	outer := "outer(" + typeName(reflect.TypeFor[capAuth]()) + ", " + typeName(reflect.TypeFor[capTenant]()) + ")"
	wantChain := "chain=http:context -> holdAllow -> http:access-log -> [observe] " + outer +
		" -> inner -> http:recovery -> http:body-limit"
	written := strings.Join(records.at(slog.LevelInfo), "\n")
	mustContain(t, written, wantChain,
		"the chain in the order it runs, the stage marker before each stage's layers and each "+
			"registered layer with the capabilities it stands for")
	mustContain(t, written, "endpoint=public", "the endpoint the chain belongs to")
	mustContain(t, written, "routes=1", "the number of routes mounted")
}

// TestAnObserveLayerSeesARecoveredPanicAsANormalReturn names the property the chain's order exists
// for.
func TestAnObserveLayerSeesARecoveredPanicAsANormalReturn(t *testing.T) {
	logger, _ := newRecordingLogger()
	var returned bool
	var status int
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "observer", Stage: StageObserve, Handler: func(c *gin.Context) {
			c.Next()
			returned, status = true, c.Writer.Status()
		}})
		e.Route("GET", "/things", panicking("boom"))
	})

	recorder := serveOnce(handler, nethttp.MethodGet, "/things")

	if recorder.Code != nethttp.StatusInternalServerError {
		t.Errorf("the client was answered %d, want %d", recorder.Code, nethttp.StatusInternalServerError)
	}
	if !returned || status != nethttp.StatusInternalServerError {
		t.Errorf("the observe layer saw returned=%v status=%d, want a normal return with %d: the "+
			"recovery layer has to sit inside the observe stage", returned, status,
			nethttp.StatusInternalServerError)
	}
}

// TestATrustedProxyMakesClientIPTakeTheForwardedHeader pins the other half of the proxy
// configuration: with a network named.
func TestATrustedProxyMakesClientIPTakeTheForwardedHeader(t *testing.T) {
	var reported string
	logger, _ := newRecordingLogger()
	s := testSettings("public")
	s.trustedProxies = []string{"192.0.2.0/24"}
	handler := mustAssemble(t, s, nil, logger, func(e Endpoint) {
		e.Route("GET", "/things", func(c *gin.Context) { reported = c.ClientIP() })
	})

	request := httptest.NewRequest(nethttp.MethodGet, "/things", nil)
	request.RemoteAddr = "192.0.2.10:4711"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if reported != "203.0.113.9" {
		t.Errorf("ClientIP reported %q for a request from a trusted proxy carrying a forwarded header, "+
			"want the forwarded address", reported)
	}
}

// TestFlyClientIPIsTakenFromATrustedFlyProxy pins the deployment this repository's demo runs on.
func TestFlyClientIPIsTakenFromATrustedFlyProxy(t *testing.T) {
	got, decodeErr, runErr := readThrough(t, `{
		"http": {
			"listeners": {"backend": {"address": ":8080"}},
			"trusted-proxies": ["172.16.0.0/16"],
			"remote-ip-headers": ["Fly-Client-IP"],
			"endpoints": {"public": {"listener": "backend"}}
		}
	}`, schema())
	if runErr != nil || decodeErr != nil {
		t.Fatalf("the section did not decode: %v", errors.Join(runErr, decodeErr))
	}

	_, endpoints, err := got.resolve()
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("the document declares one endpoint and %d resolved: %+v", len(endpoints), endpoints)
	}

	var reported string
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, endpoints[0], nil, logger, func(e Endpoint) {
		e.Route("GET", "/things", func(c *gin.Context) { reported = c.ClientIP() })
	})

	request := httptest.NewRequest(nethttp.MethodGet, "/things", nil)
	request.RemoteAddr = "172.16.4.20:4711"
	request.Header.Set("Fly-Client-IP", "203.0.113.7")
	request.Header.Set("X-Forwarded-For", "198.51.100.4, 172.16.4.20")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if reported != "203.0.113.7" {
		t.Errorf("ClientIP reported %q for a request from Fly's proxy carrying Fly-Client-IP, "+
			"want the address in that header", reported)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
