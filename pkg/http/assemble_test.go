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
	"github.com/vislake/speed/pkg/log"
)

// assembleEndpoint registers through the real surface and assembles the chain
// the way Serve does, so what these tests exercise is the path a run takes. It
// delivers no ErrorResponder.
func assembleEndpoint(t *testing.T, s endpointSettings, injected, logger *slog.Logger,
	register func(Endpoint),
) (nethttp.Handler, error) {
	t.Helper()
	return assembleEndpointWith(t, s, injected, logger, nil, register)
}

// assembleEndpointWith is assembleEndpoint with the ErrorResponder under the
// test's control, for the tests whose subject is the shape of an error response.
func assembleEndpointWith(t *testing.T, s endpointSettings, injected, logger *slog.Logger,
	responder ErrorResponder, register func(Endpoint),
) (nethttp.Handler, error) {
	t.Helper()
	r := newRouter([]endpointSettings{s})
	r.open()
	register(endpointOf(t, r, s.name))
	r.seal()
	return r.endpoints[s.name].assemble(injected, logger, responder)
}

// mustAssemble is assembleEndpoint for the tests whose subject is the chain
// rather than a failure to build one.
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

// trace records what each layer saw, in the order the layers ran.
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

// noteLayer builds a layer that records that it ran and passes the request on.
func noteLayer(tr *trace, name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tr.note(name)
		c.Next()
	}
}

// noteHandler builds a route handler that records that it ran.
func noteHandler(tr *trace, name string) gin.HandlerFunc {
	return func(*gin.Context) { tr.note(name) }
}

// TestRouteConflictIsStartupFailure pins the conversion at the mounting point.
// gin reports a clash by panicking, and left alone that takes the process down
// with a stack trace during a stage whose failures are supposed to end the
// startup in an orderly way.
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

// TestEveryClashGinRefusesIsARouteConflict pins the classes of clash the design
// lists, each as a startup failure of its own: the same position given two
// parameter names, a method and path registered twice, and a catch-all beside a
// static segment or a parameter, in whichever order the two were registered.
//
// The two routes in a row are different strings wherever the clash allows it,
// which is what makes the naming assertion discriminating: with one route
// registered twice, naming only the one being mounted would read the same as
// naming both. The fragment asserted is the pair standing together in this
// module's own sentence, not merely both appearing somewhere in a message that
// ends with gin's own words.
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

// TestRoutesGinAcceptsBesideEachOtherAreServed pins the three pairings that are
// not conflicts, by what the assembled endpoint answers for each: a static
// segment beside a parameter, and one path under two methods, with the parameter
// named differently under each. A check that refused any two routes on the same
// path would turn all of these into startup failures.
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

// TestARouteRunsItsHandlersInOrder pins that a route's several handlers run one
// after another, in the order they were given and after the layers, and that the
// last one is what serves the request.
func TestARouteRunsItsHandlersInOrder(t *testing.T) {
	tr := &trace{}
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "layer", Handler: noteLayer(tr, "layer")})
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

// TestAbortInARouteHandlerStopsTheHandlersAfterIt pins what c.Abort does to a
// route's own chain, which is what lets the first handler of a route act as a
// guard for the rest.
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

// TestALayerReadsTheOutcomeAfterNext pins what the surface offers a layer that
// wants to see how the request ended: the status, whether anything was written
// and how many bytes, read off the context's writer once the rest of the chain
// has run. No layer has to put a writer of its own around the response for it.
func TestALayerReadsTheOutcomeAfterNext(t *testing.T) {
	var status, size int
	var written bool
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "observer", Handler: func(c *gin.Context) {
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

// TestConstraintThroughUseOrdersAgainstItsProvider pins that a constraint
// declared the only way a registrant can declare one — through Use — reaches
// the layer it points at.
//
// The two Order values are deliberately the wrong way round: left to Order
// alone the chain is "tenant" then "auth", and that is exactly what an
// implementation whose provider index stays empty produces, whatever anyone
// registers. Provides is what the After resolves against, so the constraint
// wins and "auth" comes out first.
func TestConstraintThroughUseOrdersAgainstItsProvider(t *testing.T) {
	tr := &trace{}
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{
			Name:     "auth",
			Order:    10,
			Provides: []core.Token{(*capAuth)(nil)},
			Handler:  noteLayer(tr, "auth"),
		})
		e.Use(Middleware{
			Name:    "tenant",
			Order:   0,
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

// TestEmptyProvidesStaysLegal pins the design's word that a layer standing for
// nothing is a legal registration: it is ordered with the rest and it is in the
// chain. Making Provides required — the obvious way to make every constraint
// land — would fail here, and it is a change to the design rather than a fix.
func TestEmptyProvidesStaysLegal(t *testing.T) {
	tr := &trace{}
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "anonymous", Order: 0, Handler: noteLayer(tr, "anonymous")})
		e.Use(Middleware{
			Name:     "auth",
			Order:    10,
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

// TestUnlandedConstraintIsReported pins the diagnostic the design requires.
//
// One assembly carries three shapes: "tenant" points at a capability a layer on
// this endpoint stands for, "audit" points at one nobody stands for, and
// "recover" stands for the capability it names and is the only layer that does.
// The assembly carries on through all three — a capability absent from the
// process is a legal configuration — so each of the two unplaced constraints
// has to be visible, located, and told apart from the other, because the two
// are answered by different edits.
//
// The two are written at different levels, and the level is part of what is
// pinned. A capability nobody on the endpoint stands for is what a legal
// assembly produces whenever a module is absent or does not guard that
// endpoint, so it is written at Info; a warning there would be one every such
// assembly carries for as long as it runs. A layer that is the only one
// standing for the capability it names has a declaration that is wrong in
// every assembly, so it is written at Warn.
func TestUnlandedConstraintIsReported(t *testing.T) {
	logger, records := newRecordingLogger()
	_, err := assembleEndpoint(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{
			Name:     "auth",
			Provides: []core.Token{(*capAuth)(nil)},
			Handler:  passThrough,
		})
		e.Use(Middleware{
			Name:    "tenant",
			After:   []core.Token{(*capAuth)(nil)},
			Handler: passThrough,
		})
		e.Use(Middleware{
			Name:    "audit",
			After:   []core.Token{(*capQuota)(nil)},
			Handler: passThrough,
		})
		e.Use(Middleware{
			Name:     "recover",
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
			"that declared them run where Order puts them with no signal at all")
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

// TestALegalAssemblyStartsWithoutAWarning pins the shape a host with an
// unguarded endpoint produces: a capturing layer declares itself inside
// authentication and tenant resolution on every endpoint it guards, and one of
// those endpoints carries authentication but no tenant resolution, because the
// host resolves no tenant there. Nothing in that assembly is wrong. The
// constraint on the absent capability placed nothing, and that is still
// written down, located and with its cause, beside the chain listing it
// explains; but a warning that every such assembly carries from its first
// start is one an operator learns to skip, and the one a wrong declaration
// raises would then be skipped with it.
func TestALegalAssemblyStartsWithoutAWarning(t *testing.T) {
	logger, records := newRecordingLogger()
	_, err := assembleEndpoint(t, testSettings("admin"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{
			Name:     "gate",
			Provides: []core.Token{(*capAuth)(nil)},
			Handler:  passThrough,
		})
		e.Use(Middleware{
			Name:    "journal",
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

// unlandedLines keeps the lines that report a constraint placing nothing.
func unlandedLines(lines []string) []string {
	var out []string
	for _, line := range lines {
		if strings.HasPrefix(line, unlandedMsg) {
			out = append(out, line)
		}
	}
	return out
}

// lineFor returns the first line carrying want, or "" when none does.
func lineFor(lines []string, want string) string {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return line
		}
	}
	return ""
}

// TestCycleThroughUseFailsTheAssembly pins that a cycle is reachable through
// the public surface at all, and that the ordering's verdict reaches the
// assembly rather than being swallowed into a chain with layers missing.
//
// Both layers are registered with Use, as a registrant would. With the
// provider index empty neither After binds to anything, the graph has no edges
// and the assembly succeeds — which is the observation this replaces.
func TestCycleThroughUseFailsTheAssembly(t *testing.T) {
	logger, _ := newRecordingLogger()
	_, err := assembleEndpoint(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{
			Name:     "a",
			Provides: []core.Token{(*capAuth)(nil)},
			After:    []core.Token{(*capTenant)(nil)},
			Handler:  passThrough,
		})
		e.Use(Middleware{
			Name:     "b",
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

// TestChainOrderIsOutermostFirst pins that the chain runs in the order the
// ordering gives, rather than in registration order or its reverse.
func TestChainOrderIsOutermostFirst(t *testing.T) {
	tr := &trace{}
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		// Registered innermost-first and with the preferences deciding, so
		// an implementation running them in registration order gets the
		// opposite of the right answer.
		e.Use(Middleware{Name: "inner", Order: 10, Handler: noteLayer(tr, "inner")})
		e.Use(Middleware{Name: "middle", Order: 0, Handler: noteLayer(tr, "middle")})
		e.Use(Middleware{Name: "outer", Order: -10, Handler: noteLayer(tr, "outer")})
		e.Route("GET", "/things", noteHandler(tr, "handler"))
	})

	serveOnce(handler, nethttp.MethodGet, "/things")

	want := []string{"outer", "middle", "inner", "handler"}
	if got := tr.seen(); !slices.Equal(got, want) {
		t.Errorf("the chain ran in the order %v, and the ordering gives %v", got, want)
	}
}

// TestALayerRegisteredAfterTheRoutesStillRunsForThem pins the order the engine
// is built in. gin combines the engine's handlers into a route's chain when the
// route is added, so a layer handed to the engine after the routes would reach
// none of them; the registration order here puts every Use after every Route.
func TestALayerRegisteredAfterTheRoutesStillRunsForThem(t *testing.T) {
	tr := &trace{}
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Route("GET", "/things", noteHandler(tr, "handler"))
		e.Use(Middleware{Name: "late", Handler: noteLayer(tr, "late")})
	})

	serveOnce(handler, nethttp.MethodGet, "/things")

	if got, want := tr.seen(), []string{"late", "handler"}; !slices.Equal(got, want) {
		t.Errorf("the request went through %v, want %v: a layer applies to every route of the "+
			"endpoint whatever order the registrations arrived in", got, want)
	}
}

// TestBodyLimitAppliedOutsideEveryLayer pins where the limit is imposed. Put in
// a handler's own reading, it would not hold for a layer reading the body, nor
// for a handler that parses the body itself.
func TestBodyLimitAppliedOutsideEveryLayer(t *testing.T) {
	logger, _ := newRecordingLogger()
	s := testSettings("public")
	s.maxBodyBytes = 8

	var readErr error
	var read []byte
	handler := mustAssemble(t, s, nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "outermost", Order: -100, Handler: func(c *gin.Context) {
			read, readErr = io.ReadAll(c.Request.Body)
			c.Next()
		}})
		e.Route("POST", "/things", filler)
	})

	body := strings.NewReader(strings.Repeat("x", 64))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(nethttp.MethodPost, "/things", body))

	var tooLarge *nethttp.MaxBytesError
	if !errors.As(readErr, &tooLarge) {
		t.Fatalf("the outermost layer read %d bytes with error %v; the limit was not imposed outside it",
			len(read), readErr)
	}
	if tooLarge.Limit != s.maxBodyBytes {
		t.Errorf("the limit imposed was %d, and the endpoint is configured with %d",
			tooLarge.Limit, s.maxBodyBytes)
	}
}

// TestAnOversizedBodyBindsAsAMaxBytesError pins what a handler sees when it
// binds a body through gin: the limit the endpoint imposes comes back from
// ShouldBindJSON as an *http.MaxBytesError, which errors.As reaches, and not as
// a decoding error that would read as a malformed body. A caller that wants to
// answer 413 tells the two apart this way.
//
// The body is valid JSON that is longer than the limit, so the only thing wrong
// with it is its size; and a body inside the limit binds, which is what keeps
// the first half from being a binder that fails on everything.
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
			httptest.NewRequest(nethttp.MethodPost, "/things", strings.NewReader(body)))
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

// TestNoBodyLimitIsLeftUnlimited pins the switch-off: an explicit 0 means no
// limit, so the body arrives whole.
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

// TestLoggerInjectedOutsideEveryLayer pins the injection point. Injected by a
// middleware instead, the layers outside it would take the process default
// logger out of the context and write somewhere else entirely.
func TestLoggerInjectedOutsideEveryLayer(t *testing.T) {
	logger, _ := newRecordingLogger()
	injected, records := newRecordingLogger()

	var seen *slog.Logger
	handler := mustAssemble(t, testSettings("public"), injected, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "outermost", Order: -100, Handler: func(c *gin.Context) {
			seen = log.FromContext(c.Request.Context())
			c.Next()
		}})
		e.Route("GET", "/things", filler)
	})

	serveOnce(handler, nethttp.MethodGet, "/things")

	if seen != injected {
		t.Fatal("the outermost layer did not take the injected logger out of the request context")
	}
	seen.Info("written from the outermost layer")
	if len(records.at(slog.LevelInfo)) != 1 {
		t.Error("what the layer took out of the context does not write to the injected destination")
	}
}

// TestNoLoggerModuleLeavesContextUntouched pins the accepted cost of the
// dependency being optional: with no Logger capability nothing is injected and
// nothing fails. Turning this into a startup failure, or into a fallback that
// injects the default logger, is a change to the design, not a fix.
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

// TestAssembledChainIsListed pins that the chain a run really serves is written
// down at startup, in the order it runs, with the capabilities each registered
// layer stands for. A process whose layer order can only be worked out by
// sending a request through it is one where a mis-ordered chain is found by the
// request that goes wrong, and a chain listing with the names alone cannot say
// that a capability has no representative on the endpoint.
//
// The two layers this module puts around the registered ones are in the line as
// well, by name: they are on no registration surface and stand for no
// capability, and a reader comparing the line with the code would otherwise find
// the body limit, the logger and the panic fallback applied by something the
// line does not mention.
func TestAssembledChainIsListed(t *testing.T) {
	logger, records := newRecordingLogger()
	mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "inner", Order: 10, Handler: passThrough})
		e.Use(Middleware{
			Name:     "outer",
			Order:    -10,
			Provides: []core.Token{(*capAuth)(nil), (*capTenant)(nil)},
			Handler:  passThrough,
		})
		e.Route("GET", "/things", filler)
	})

	outer := "outer(" + typeName(reflect.TypeFor[capAuth]()) + ", " + typeName(reflect.TypeFor[capTenant]()) + ")"
	written := strings.Join(records.at(slog.LevelInfo), "\n")
	mustContain(t, written, "chain=http:base -> http:fallback -> "+outer+" -> inner",
		"the chain in the order it runs, each registered layer with the capabilities it stands for")
	mustContain(t, written, "endpoint=public", "the endpoint the chain belongs to")
	mustContain(t, written, "routes=1", "the number of routes mounted")
}

// equalStrings compares two sequences element by element.
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
