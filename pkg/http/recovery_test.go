package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// panicking is a handler that panics with thrown.
func panicking(thrown any) nethttp.Handler {
	return nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {
		panic(thrown)
	})
}

// assembleFallback assembles one endpoint with logger both injected into the
// request context and used for the assembly's own diagnostics, so a record the
// fallback writes through the context reaches the same handler the test reads.
func assembleFallback(t *testing.T, responder PanicResponder, logger *slog.Logger,
	register func(Endpoint),
) nethttp.Handler {
	t.Helper()
	handler, err := assembleEndpointOn(t, nethttp.NewServeMux(), testSettings("public"),
		logger, logger, responder, register)
	if err != nil {
		t.Fatalf("assembling the endpoint: %v", err)
	}
	return handler
}

// TestAHandlerPanicIsAnsweredAsAServerError is the whole point of the fallback:
// a panic on the request path ends that one request with a status, rather than
// leaving the connection to be dropped with nothing written.
//
// The alternative is what this same request did before there was a fallback —
// the panic travelled out of the handler, past every middleware, and reached
// net/http, which closes the connection and writes no status at all.
func TestAHandlerPanicIsAnsweredAsAServerError(t *testing.T) {
	logger, _ := newRecordingLogger()
	handler := assembleFallback(t, nil, logger, func(e Endpoint) {
		e.Route("GET /things", panicking("boom"))
	})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodGet, "/things", nil))

	if recorder.Code != nethttp.StatusInternalServerError {
		t.Errorf("a panicking handler was answered with %d, want %d", recorder.Code,
			nethttp.StatusInternalServerError)
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("the default response carried a body of %d byte(s); it claims no API's error "+
			"shape, and a body here would be this module inventing one", recorder.Body.Len())
	}
}

// TestAPanicAfterTheHeaderIsCommittedKeepsTheCommittedStatus pins the other
// half of what answering a panic means.
//
// Two things are asserted, and they are different things. What the caller keeps
// is the status the handler had already committed: the standard library's
// writer ignores a status written after the first, so nothing the fallback
// writes afterwards can replace it. What the fallback does is still write its
// own status to the writer it was handed, because nothing on a plain
// ResponseWriter says whether the header is already out — the spy writer is
// what makes that visible, since the recorder hides it.
func TestAPanicAfterTheHeaderIsCommittedKeepsTheCommittedStatus(t *testing.T) {
	logger, records := newRecordingLogger()
	handler := assembleFallback(t, nil, logger, func(e Endpoint) {
		e.Route("GET /things", nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			w.WriteHeader(nethttp.StatusTeapot)
			panic("after the header")
		}))
	})

	writer := newSpyWriter()
	handler.ServeHTTP(writer, httptest.NewRequest(nethttp.MethodGet, "/things", nil))

	if writer.status != nethttp.StatusInternalServerError {
		t.Errorf("the fallback last wrote status %d, want %d: it answers the panic with its own "+
			"status and lets the writer decide what that means", writer.status,
			nethttp.StatusInternalServerError)
	}
	if written := records.at(slog.LevelError); len(written) != 1 {
		t.Errorf("the fallback wrote %d error records, want one: with the status unrewritable the "+
			"record is the only signal that the request did not finish: %v", len(written), written)
	}

	// The same request again, this time through a writer with the standard
	// library's semantics, for what the caller ends up with.
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodGet, "/things", nil))

	if recorder.Code != nethttp.StatusTeapot {
		t.Errorf("the request ended with %d, want the %d the handler had already committed: "+
			"nothing written after the status line is on the wire can replace it", recorder.Code,
			nethttp.StatusTeapot)
	}
}

// TestAPanicInARegisteredLayerIsAnswered pins that the fallback sits outside
// every registered layer rather than between the engine and the handler: it
// catches what a middleware raises, not only what a handler does.
func TestAPanicInARegisteredLayerIsAnswered(t *testing.T) {
	logger, records := newRecordingLogger()
	handler := assembleFallback(t, nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "boom", Order: 0, Wrap: func(nethttp.Handler) nethttp.Handler {
			return nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {
				panic("raised in a layer")
			})
		}})
		e.Route("GET /things", nethttp.NotFoundHandler())
	})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodGet, "/things", nil))

	if recorder.Code != nethttp.StatusInternalServerError {
		t.Errorf("a panicking middleware was answered with %d, want %d", recorder.Code,
			nethttp.StatusInternalServerError)
	}
	if written := records.at(slog.LevelError); len(written) != 1 {
		t.Errorf("the fallback wrote %d error records, want one: %v", len(written), written)
	}
}

// TestTheFallbackWritesTheEndpointAndThePanic pins what the record has to carry
// to be worth writing. The endpoint and the panic value are what locate the
// defect; the stack is what a reader needs to see where it came from.
//
// Each attribute is asserted on its value rather than on its key being
// present, because a record rendered from an empty stack still contains the
// text "stack=" and would satisfy the weaker reading.
func TestTheFallbackWritesTheEndpointAndThePanic(t *testing.T) {
	logger, records := newRecordingLogger()
	handler := assembleFallback(t, nil, logger, func(e Endpoint) {
		e.Route("GET /things", panicking("boom"))
	})

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(nethttp.MethodGet, "/things", nil))

	written := records.at(slog.LevelError)
	if len(written) != 1 {
		t.Fatalf("the fallback wrote %d error records, want exactly one: %v", len(written), written)
	}
	mustContain(t, written[0], "endpoint=public", "the endpoint the panic happened on")
	mustContain(t, written[0], "panic=boom", "the value the handler panicked with")
	if _, stack, found := strings.Cut(written[0], "stack="); !found || strings.TrimSpace(stack) == "" {
		t.Errorf("the record carries no stack, so it says nothing about where the panic came from: %q",
			written[0])
	}
}

// TestADeliveredResponderDecidesTheShape pins the one seam the response shape
// goes through: with a module delivering a PanicResponder, that module writes
// the response and the default status is not written at all.
//
// The writer is the spy rather than the recorder, because the recorder ignores
// a status written after the first one and would therefore accept a fallback
// that wrote its default 500 behind the responder's back.
func TestADeliveredResponderDecidesTheShape(t *testing.T) {
	logger, records := newRecordingLogger()
	responder := &fixedResponder{status: nethttp.StatusServiceUnavailable}
	handler := assembleFallback(t, responder, logger, func(e Endpoint) {
		e.Route("GET /things", panicking("boom"))
	})

	writer := newSpyWriter()
	handler.ServeHTTP(writer, httptest.NewRequest(nethttp.MethodGet, "/things", nil))

	if writer.status != nethttp.StatusServiceUnavailable {
		t.Errorf("the response was answered with %d, want the %d the delivered responder writes",
			writer.status, nethttp.StatusServiceUnavailable)
	}
	if responder.seen != "boom" {
		t.Errorf("the responder was handed %v, want the value the handler panicked with", responder.seen)
	}
	written := records.at(slog.LevelError)
	if len(written) != 1 {
		t.Fatalf("the fallback wrote %d error records, want one: taking the response shape over "+
			"does not take the record over, which is the whole of what a missing responder costs: %v",
			len(written), written)
	}
	mustContain(t, written[0], "endpoint=public", "the endpoint the panic happened on")
	mustContain(t, written[0], "panic=boom", "the value the handler panicked with")
}

// fixedResponder is a PanicResponder a test holds: it writes one status and
// keeps the value it was handed.
type fixedResponder struct {
	status int
	seen   any
}

func (f *fixedResponder) RespondPanic(w nethttp.ResponseWriter, _ *nethttp.Request, thrown any) {
	f.seen = thrown
	w.WriteHeader(f.status)
}

// TestErrAbortHandlerIsNotAnswered pins the one panic the fallback lets past.
// ErrAbortHandler is a handler asking for its connection to be abandoned, not a
// defect: answered here it would become a status written to a socket the
// handler has already declared broken, and an error record for a client that
// simply went away.
//
// This case and TestARequestWithoutAPanicIsLeftAlone both assert that nothing
// happens, so neither of them fails if the fallback is removed from the
// assembly altogether. What carries that is the chain listing: the assembled
// chain names the fallback, and TestAssembledChainIsListed pins the listing.
func TestErrAbortHandlerIsNotAnswered(t *testing.T) {
	logger, records := newRecordingLogger()
	handler := assembleFallback(t, nil, logger, func(e Endpoint) {
		e.Route("GET /things", panicking(nethttp.ErrAbortHandler))
	})

	writer := newSpyWriter()
	var raised any
	func() {
		defer func() { raised = recover() }()
		handler.ServeHTTP(writer, httptest.NewRequest(nethttp.MethodGet, "/things", nil))
	}()

	abort, isErr := raised.(error)
	if !isErr || !errors.Is(abort, nethttp.ErrAbortHandler) {
		t.Errorf("the fallback answered an ErrAbortHandler panic with %v; it has to let it "+
			"through so net/http abandons the connection the way the handler asked", raised)
	}
	if writer.wroteHeader {
		t.Errorf("the fallback wrote status %d for an ErrAbortHandler panic", writer.status)
	}
	if written := records.at(slog.LevelError); len(written) != 0 {
		t.Errorf("the fallback wrote %v for an ErrAbortHandler panic, want nothing", written)
	}
}

// TestAWrappedErrAbortHandlerIsNotAnswered pins the width of that check. A
// library that wraps the sentinel before panicking is asking for the same
// abandonment, and net/http matches it by identity, so a wrapper arriving there
// would be an ordinary panic: logged, with the connection closed rather than a
// status written to a socket the handler gave up on.
func TestAWrappedErrAbortHandlerIsNotAnswered(t *testing.T) {
	logger, records := newRecordingLogger()
	handler := assembleFallback(t, nil, logger, func(e Endpoint) {
		e.Route("GET /things", panicking(fmt.Errorf("the reader gave up: %w", nethttp.ErrAbortHandler)))
	})

	writer := newSpyWriter()
	var raised any
	func() {
		defer func() { raised = recover() }()
		handler.ServeHTTP(writer, httptest.NewRequest(nethttp.MethodGet, "/things", nil))
	}()

	abort, isErr := raised.(error)
	if !isErr || !errors.Is(abort, nethttp.ErrAbortHandler) {
		t.Errorf("the fallback answered a wrapped ErrAbortHandler with %v; the wrapper is a "+
			"handler asking for the same abandonment", raised)
	}
	if writer.wroteHeader {
		t.Errorf("the fallback wrote status %d for a wrapped ErrAbortHandler", writer.status)
	}
	if written := records.at(slog.LevelError); len(written) != 0 {
		t.Errorf("the fallback wrote %v for a wrapped ErrAbortHandler, want nothing", written)
	}
}

// TestAPanicWithANilValueIsAnswered pins that panic(nil) is answered like any
// other panic. The runtime hands it to the fallback as a
// *runtime.PanicNilError, and under GODEBUG=panicnil=1 as a recovered nil;
// read as a request that returned, either would be an empty 200 with no
// record at all.
func TestAPanicWithANilValueIsAnswered(t *testing.T) {
	logger, records := newRecordingLogger()
	handler := assembleFallback(t, nil, logger, func(e Endpoint) {
		e.Route("GET /things", panicking(nil))
	})

	writer := newSpyWriter()
	handler.ServeHTTP(writer, httptest.NewRequest(nethttp.MethodGet, "/things", nil))

	if writer.status != nethttp.StatusInternalServerError {
		t.Errorf("a handler that panicked with nil was answered with %d, want %d: the fallback "+
			"reads a recovered nil as a panic unless the request returned", writer.status,
			nethttp.StatusInternalServerError)
	}
	if written := records.at(slog.LevelError); len(written) != 1 {
		t.Errorf("the fallback wrote %d error records for a nil-valued panic, want one: %v",
			len(written), written)
	}
}

// TestAGoexitIsNotRecordedAsAPanic pins what the fallback says about a handler
// that ended its goroutine: runtime.Goexit unwinds the stack without a value,
// so the deferred function recovers nil while the handler never returned. It
// is a defect worth a record, but nothing panicked, and a record saying a
// request panicked sends its reader looking for a panic that never happened.
func TestAGoexitIsNotRecordedAsAPanic(t *testing.T) {
	logger, records := newRecordingLogger()
	handler := assembleFallback(t, nil, logger, func(e Endpoint) {
		e.Route("GET /things", nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {
			runtime.Goexit()
		}))
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(newSpyWriter(), httptest.NewRequest(nethttp.MethodGet, "/things", nil))
	}()
	waitFor(t, done, "the goroutine the handler ended")

	written := records.at(slog.LevelError)
	if len(written) != 1 {
		t.Fatalf("the fallback wrote %d error records for a handler that called Goexit, want one: %v",
			len(written), written)
	}
	if strings.Contains(written[0], "panicked") {
		t.Errorf("the record says the request panicked, and nothing did: %s", written[0])
	}
	mustContain(t, written[0], "Goexit", "the way the handler ended")
}

// TestARequestWithoutAPanicIsLeftAlone is the other half: the fallback does not
// read the request, does not touch the response, and writes nothing, so a
// request that completes normally is a request it never had a hand in.
func TestARequestWithoutAPanicIsLeftAlone(t *testing.T) {
	const sent = "the handler's own body"
	logger, records := newRecordingLogger()
	handler := assembleFallback(t, nil, logger, func(e Endpoint) {
		e.Route("GET /things", nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			w.Header().Set("X-Handler", "set")
			w.WriteHeader(nethttp.StatusTeapot)
			_, _ = w.Write([]byte(sent))
		}))
	})

	writer := newSpyWriter()
	handler.ServeHTTP(writer, httptest.NewRequest(nethttp.MethodGet, "/things", nil))

	if writer.status != nethttp.StatusTeapot {
		t.Errorf("the handler's own status %d did not reach the caller; %d did", nethttp.StatusTeapot,
			writer.status)
	}
	if got := string(writer.body); got != sent {
		t.Errorf("the caller received the body %q, want the handler's own %q", got, sent)
	}
	if got := writer.header.Get("X-Handler"); got != "set" {
		t.Errorf("the caller received X-Handler %q, want the handler's own %q", got, "set")
	}
	if written := records.at(slog.LevelError); len(written) != 0 {
		t.Errorf("a request without a panic produced the error records %v", written)
	}
}

// TestAHandlerPanicReachesTheClientAsAStatus is the same statement over a real
// socket, which is where the difference is visible to anyone outside the
// process: before the fallback the client's request failed outright, because
// the connection was closed before any status line was written.
func TestAHandlerPanicReachesTheClientAsAStatus(t *testing.T) {
	serving := make(chan struct{})
	registrar := &stageProbe{
		name:     "registrar",
		requires: requiresRouter(),
		onInit:   routeOn("public", "GET /boom", panicking("boom")),
		onServe: func(*core.Registry) error {
			close(serving)
			return nil
		},
	}
	a := startAssembly(t, oneEndpoint("public"), testModule(), registrar.module())
	waitFor(t, serving, "the Serve callback of the module that requires the Router")

	r, err := routerFrom(a.reg)
	if err != nil {
		t.Fatalf("taking this module's product out of the registry: %v", err)
	}
	addr, err := boundAddr(r, "public")
	if err != nil {
		t.Fatalf("finding the address the endpoint bound: %v", err)
	}

	status, err := getStatus(context.Background(), addr, "/boom")
	if err != nil {
		t.Fatalf("the request failed outright rather than being answered: %v. A panic with no "+
			"fallback over it leaves the client with no status at all", err)
	}
	if status != nethttp.StatusInternalServerError {
		t.Errorf("the client was answered %d, want %d", status, nethttp.StatusInternalServerError)
	}
}
