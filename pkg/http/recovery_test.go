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

	"github.com/gin-gonic/gin"
)

// panicking is a route handler that panics with thrown.
func panicking(thrown any) gin.HandlerFunc {
	return func(*gin.Context) { panic(thrown) }
}

// panicValue is an error a handler panics with, so that a test can tell the
// value it threw from one that merely reads the same.
type panicValue struct{ detail string }

func (p *panicValue) Error() string { return p.detail }

// assembleFallback assembles one endpoint with logger both injected into the
// request context and used for the assembly's own diagnostics, so a record the
// fallback writes through the context reaches the same handler the test reads.
func assembleFallback(t *testing.T, responder ErrorResponder, logger *slog.Logger,
	register func(Endpoint),
) nethttp.Handler {
	t.Helper()
	handler, err := assembleEndpointWith(t, testSettings("public"), logger, logger, responder, nil, register)
	if err != nil {
		t.Fatalf("assembling the endpoint: %v", err)
	}
	return handler
}

// TestAHandlerPanicIsAnsweredAsAServerError is the whole point of the fallback:
// a panic on the request path ends that one request with an error response,
// rather than leaving the connection to be dropped with nothing written. The
// response is the default shape, the one Fail writes.
func TestAHandlerPanicIsAnsweredAsAServerError(t *testing.T) {
	logger, _ := newRecordingLogger()
	handler := assembleFallback(t, nil, logger, func(e Endpoint) {
		e.Route("GET", "/things", panicking("boom"))
	})

	recorder := serveOnce(handler, nethttp.MethodGet, "/things")

	if recorder.Code != nethttp.StatusInternalServerError {
		t.Errorf("a panicking handler was answered with %d, want %d", recorder.Code,
			nethttp.StatusInternalServerError)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("the media type was %q, want %q", got, "application/problem+json")
	}
	members := problemBodyOf(t, recorder)
	if members["type"] != "about:blank" || members["title"] != "Internal Server Error" ||
		members["status"] != float64(nethttp.StatusInternalServerError) {
		t.Errorf("the body is %v, want the default shape for a 500", members)
	}
}

// TestThePanicValueStaysOutOfTheDefaultResponse pins that what a handler
// panicked with is not what a client reads. A panic value is whatever the
// failing code had to hand — an error from a driver with a connection string in
// it, a string with a token — and the default shape writes only the status.
func TestThePanicValueStaysOutOfTheDefaultResponse(t *testing.T) {
	for _, c := range []struct {
		name   string
		thrown any
	}{
		{"a string", "token=s3cret-value"},
		{"an error", errors.New("dial tcp db-7.internal:5432: password=s3cret-value")},
		{"an error with a cause", fmt.Errorf("loading: %w", &panicValue{"password=s3cret-value"})},
	} {
		t.Run(c.name, func(t *testing.T) {
			logger, _ := newRecordingLogger()
			handler := assembleFallback(t, nil, logger, func(e Endpoint) {
				e.Route("GET", "/things", panicking(c.thrown))
			})

			recorder := serveOnce(handler, nethttp.MethodGet, "/things")

			if strings.Contains(recorder.Body.String(), "s3cret-value") ||
				strings.Contains(fmt.Sprint(recorder.Header()), "s3cret-value") {
				t.Errorf("the panic value reached the client: %v %s", recorder.Header(), recorder.Body.String())
			}
		})
	}
}

// TestAPanicInARegisteredLayerIsAnswered pins that the recovery layer sits
// outside every stage that can refuse a request: it catches what a layer of one
// of those stages raises, not only what a handler does.
func TestAPanicInARegisteredLayerIsAnswered(t *testing.T) {
	logger, records := newRecordingLogger()
	handler := assembleFallback(t, nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "boom", Stage: StageGuard, Handler: panicking("raised in a layer")})
		e.Route("GET", "/things", filler)
	})

	recorder := serveOnce(handler, nethttp.MethodGet, "/things")

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
		e.Route("GET", "/things", panicking("boom"))
	})

	serveOnce(handler, nethttp.MethodGet, "/things")

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

// TestThePanicIsTheCauseOfTheProblem pins what a delivered ErrorResponder is
// handed for a panic: a 500 whose Cause carries the value that was thrown.
//
// When the value is an error it is reachable through the Cause with errors.As
// and errors.Is, and it is the very value thrown — an *panicValue the test
// holds, not a rendering of it — so a responder can map a known failure to a
// shape of its own. A value that is not an error is still a Cause that is not
// nil, and its text says what it was.
func TestThePanicIsTheCauseOfTheProblem(t *testing.T) {
	sentinel := errors.New("the store is gone")
	thrown := &panicValue{detail: "the thing is broken"}

	responder := &recordingResponder{}
	logger, _ := newRecordingLogger()
	handler := assembleFallback(t, responder, logger, func(e Endpoint) {
		e.Route("GET", "/pointer", panicking(thrown))
		e.Route("GET", "/wrapped", panicking(fmt.Errorf("reading: %w", sentinel)))
		e.Route("GET", "/text", panicking("just words"))
	})

	serveOnce(handler, nethttp.MethodGet, "/pointer")
	serveOnce(handler, nethttp.MethodGet, "/wrapped")
	serveOnce(handler, nethttp.MethodGet, "/text")

	if responder.calls() != 3 {
		t.Fatalf("the responder was asked %d times for three panics", responder.calls())
	}
	for i := range 3 {
		if got := responder.problem(t, i).Status; got != nethttp.StatusInternalServerError {
			t.Errorf("panic %d reached the responder with the status %d, want %d", i, got,
				nethttp.StatusInternalServerError)
		}
		if responder.problem(t, i).Cause == nil {
			t.Errorf("panic %d reached the responder with no Cause", i)
		}
	}

	var found *panicValue
	if cause := responder.problem(t, 0).Cause; !errors.As(cause, &found) || found != thrown {
		t.Errorf("errors.As on the Cause of a panic with a *panicValue found %v, want the value thrown", found)
	}
	for i := range 3 {
		var recovered panicError
		if cause := responder.problem(t, i).Cause; !errors.As(cause, &recovered) {
			t.Errorf("the Cause of panic %d is %T, want the panic error the fallback wraps a panic in", i, cause)
		}
		if cause := responder.problem(t, i).Cause; errors.Is(cause, errUnwound) {
			t.Errorf("the Cause of panic %d says the handler stopped without a value, and it threw one", i)
		}
	}
	if cause := responder.problem(t, 1).Cause; !errors.Is(cause, sentinel) {
		t.Errorf("errors.Is on the Cause of a panic with a wrapped sentinel is false: %v", cause)
	}
	textual := responder.problem(t, 2).Cause
	if !strings.Contains(textual.Error(), "just words") {
		t.Errorf("the Cause of a panic with a string reads %q, which does not say what was thrown", textual)
	}
	if errors.As(textual, &found) {
		t.Error("the Cause of a panic with a string is reachable as a *panicValue")
	}
}

// TestADeliveredResponderDecidesTheShapeOfAPanic pins the one seam the response
// goes through: with a module delivering an ErrorResponder, that module writes
// the response and the default shape is not written at all.
//
// The writer is the spy rather than the recorder, because the recorder ignores a
// status written after the first one and would therefore accept a fallback that
// wrote its default behind the responder's back.
func TestADeliveredResponderDecidesTheShapeOfAPanic(t *testing.T) {
	logger, records := newRecordingLogger()
	responder := &recordingResponder{}
	handler := assembleFallback(t, responder, logger, func(e Endpoint) {
		e.Route("GET", "/things", panicking("boom"))
	})

	writer := newSpyWriter()
	handler.ServeHTTP(writer, httptest.NewRequest(nethttp.MethodGet, "/things", nil))

	if writer.status != nethttp.StatusInternalServerError {
		t.Errorf("the response was written with %d, want the %d the responder writes",
			writer.status, nethttp.StatusInternalServerError)
	}
	if got, want := string(writer.body), customBody+"500"; got != want {
		t.Errorf("the body was %q, want the responder's %q, and nothing of the default shape", got, want)
	}
	if got := writer.header.Get("Content-Type"); got != customMediaType {
		t.Errorf("the media type was %q, want the responder's %q", got, customMediaType)
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

// TestALayerSeesAPanicPassThroughAndMayThrowItAgain pins the position of a
// registered layer relative to the panic. The recovery layer sits outside every
// stage that can refuse a request, so no recovery stands between a layer of one
// of those stages and the handler: a layer that wants to see a panic — to record
// it against its own subject, say — sees it as it unwinds, and a layer that
// throws what it caught again hands the same value to the recovery layer, which
// answers it and writes the record.
//
// A recovery middleware inside that stage would swallow the panic before the
// outer one saw it. The record and the Cause are what show it was not swallowed:
// both are the recovery layer's.
func TestALayerSeesAPanicPassThroughAndMayThrowItAgain(t *testing.T) {
	thrown := &panicValue{detail: "the thing is broken"}
	var seen any
	responder := &recordingResponder{}
	logger, records := newRecordingLogger()
	handler := assembleFallback(t, responder, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "witness", Stage: StageGuard, Handler: func(c *gin.Context) {
			defer func() {
				if seen = recover(); seen != nil {
					panic(seen)
				}
			}()
			c.Next()
		}})
		e.Route("GET", "/things", panicking(thrown))
	})

	recorder := serveOnce(handler, nethttp.MethodGet, "/things")

	if seen != thrown {
		t.Errorf("the layer recovered %v, want the value the handler threw", seen)
	}
	var found *panicValue
	if cause := responder.problem(t, 0).Cause; !errors.As(cause, &found) || found != thrown {
		t.Errorf("the recovery layer was handed the Cause %v, want the value thrown again", cause)
	}
	if recorder.Code != nethttp.StatusInternalServerError {
		t.Errorf("the response was %d, want the %d the recovery layer wrote", recorder.Code,
			nethttp.StatusInternalServerError)
	}
	if written := records.at(slog.LevelError); len(written) != 1 ||
		!strings.Contains(written[0], "endpoint's recovery layer caught it") {
		t.Errorf("the records were %v, want the recovery layer's one", written)
	}
}

// TestErrAbortHandlerIsNotAnswered pins the one panic the fallback lets past.
// ErrAbortHandler is a handler asking for its connection to be abandoned, not a
// defect: answered here it would become a status written to a socket the
// handler has already declared broken, and an error record for a client that
// simply went away.
//
// The panic is observed leaving the assembled handler, which is where net/http
// takes it. The connection itself is observed in
// TestAnAbortedConnectionIsNotAnsweredAsASuccess.
func TestErrAbortHandlerIsNotAnswered(t *testing.T) {
	for _, c := range []struct {
		name   string
		thrown any
	}{
		{"the sentinel", nethttp.ErrAbortHandler},
		{"the sentinel wrapped", fmt.Errorf("the reader gave up: %w", nethttp.ErrAbortHandler)},
	} {
		t.Run(c.name, func(t *testing.T) {
			logger, records := newRecordingLogger()
			responder := &recordingResponder{}
			handler := assembleFallback(t, responder, logger, func(e Endpoint) {
				e.Route("GET", "/things", panicking(c.thrown))
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
			if raised != c.thrown {
				t.Errorf("the fallback threw %#v, want the value the handler panicked with, as it was: %#v",
					raised, c.thrown)
			}
			if writer.wroteHeader {
				t.Errorf("the fallback wrote status %d for an ErrAbortHandler panic", writer.status)
			}
			if responder.calls() != 0 {
				t.Errorf("the responder was asked %d times for an ErrAbortHandler panic", responder.calls())
			}
			if written := records.at(slog.LevelError); len(written) != 0 {
				t.Errorf("the fallback wrote %v for an ErrAbortHandler panic, want nothing", written)
			}
		})
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
		e.Route("GET", "/things", panicking(nil))
	})

	recorder := serveOnce(handler, nethttp.MethodGet, "/things")

	if recorder.Code != nethttp.StatusInternalServerError {
		t.Errorf("a handler that panicked with nil was answered with %d, want %d: the fallback "+
			"reads a recovered nil as a panic unless the request returned", recorder.Code,
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
//
// The responder is asked all the same, and its Problem carries a Cause that says
// the same thing, not a nil it would have to read as "nothing happened".
func TestAGoexitIsNotRecordedAsAPanic(t *testing.T) {
	logger, records := newRecordingLogger()
	responder := &recordingResponder{}
	handler := assembleFallback(t, responder, logger, func(e Endpoint) {
		e.Route("GET", "/things", func(*gin.Context) { runtime.Goexit() })
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

	if responder.calls() != 1 {
		t.Fatalf("the responder was asked %d times for a handler that called Goexit, want once", responder.calls())
	}
	problem := responder.problem(t, 0)
	if problem.Status != nethttp.StatusInternalServerError || problem.Cause == nil {
		t.Fatalf("the responder was handed %+v, want a 500 whose Cause says the handler stopped", problem)
	}
	if !errors.Is(problem.Cause, errUnwound) {
		t.Errorf("the Cause is %v, want the error that says the handler stopped without a value", problem.Cause)
	}
	if strings.HasPrefix(problem.Cause.Error(), "panic:") {
		t.Errorf("the Cause reads %q, which is how a panic reads, and nothing panicked: it sends its "+
			"reader looking for a panic that never happened", problem.Cause)
	}
	var recovered panicError
	if errors.As(problem.Cause, &recovered) {
		t.Errorf("the Cause of a handler that stopped without a value is wrapped as the panic %v", recovered)
	}
}

// TestARequestWithoutAPanicIsLeftAlone is the other half: the fallback does not
// read the request, does not touch the response, and writes nothing, so a
// request that completes normally is a request it never had a hand in.
func TestARequestWithoutAPanicIsLeftAlone(t *testing.T) {
	const sent = "the handler's own body"
	logger, records := newRecordingLogger()
	responder := &recordingResponder{}
	handler := assembleFallback(t, responder, logger, func(e Endpoint) {
		e.Route("GET", "/things", func(c *gin.Context) {
			c.Header("X-Handler", "set")
			c.String(nethttp.StatusTeapot, sent)
		})
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
	if responder.calls() != 0 {
		t.Errorf("the responder was asked %d times for a request that did not fail", responder.calls())
	}
	if written := records.at(slog.LevelError); len(written) != 0 {
		t.Errorf("a request without a panic produced the error records %v", written)
	}
}

// TestAPanicAfterTheResponseStartedLeavesItAsWritten pins what answering a panic
// means once the response has begun to go out. The status line and the body
// already written are the response, and the fallback adds nothing to them: no
// second status, and above all no error body appended to bytes the client is
// already reading as something else. The record is written all the same, since
// with the response unchangeable it is the only signal that the request did not
// finish.
//
// A recorder takes a second status and ignores it but appends a second body, so
// the assertion that carries the difference is the body. The spy writer is what
// shows that nothing at all was written to the writer after the handler's own.
func TestAPanicAfterTheResponseStartedLeavesItAsWritten(t *testing.T) {
	logger, records := newRecordingLogger()
	responder := &recordingResponder{}
	handler := assembleFallback(t, responder, logger, func(e Endpoint) {
		e.Route("GET", "/things", func(c *gin.Context) {
			c.String(nethttp.StatusTeapot, "partial")
			panic("after the response began")
		})
	})

	recorder := serveOnce(handler, nethttp.MethodGet, "/things")
	if recorder.Code != nethttp.StatusTeapot {
		t.Errorf("the request ended with %d, want the %d the handler had already committed", recorder.Code,
			nethttp.StatusTeapot)
	}
	if got := recorder.Body.String(); got != "partial" {
		t.Errorf("the request ended with the body %q, want the %q the handler had already written: "+
			"the fallback wrote after the response had started", got, "partial")
	}

	writer := newSpyWriter()
	handler.ServeHTTP(writer, httptest.NewRequest(nethttp.MethodGet, "/things", nil))
	if writer.status != nethttp.StatusTeapot || string(writer.body) != "partial" {
		t.Errorf("the writer was left with %d %q, want the handler's own %d %q: nothing of the "+
			"fallback's belongs on it", writer.status, writer.body, nethttp.StatusTeapot, "partial")
	}
	if responder.calls() != 0 {
		t.Errorf("the responder was asked %d times for a response that had started", responder.calls())
	}
	if written := records.at(slog.LevelError); len(written) != 2 {
		t.Errorf("the fallback wrote %d error records for two panics, want one each: %v",
			len(written), written)
	}
}

// TestTheResponderIsAskedOnlyForAResponseThatHasNotStarted pins when the
// recovery layer asks the delivered ErrorResponder for a response: only when no
// part of the response has gone out. The criterion is gin's own account of the
// writer, Context.Writer.Written, which is true once a status, a body, a flush
// or a hijack has gone out and false while only a status has been set with
// c.Status.
//
// The rows are each way a response starts and the one way it has not: a handler
// that wrote nothing. Force the header out with WriteHeaderNow — an
// informational status is one way — and gin counts the response as started, so
// the interim rows are starts like any other. A response judged started when it
// is not leaves a panic unanswered, and one judged not started after it has gone
// out appends a body to bytes the client is already reading; the two wrong
// readings move a row here.
func TestTheResponderIsAskedOnlyForAResponseThatHasNotStarted(t *testing.T) {
	for _, c := range []struct {
		name    string
		handler gin.HandlerFunc
		asked   int
	}{
		{"nothing written", func(*gin.Context) { panic("before any output") }, 1},
		{"a body written and buffered", func(c *gin.Context) {
			c.String(nethttp.StatusTeapot, "partial")
			panic("after the body")
		}, 0},
		{"a body written and flushed", func(c *gin.Context) {
			c.String(nethttp.StatusTeapot, "partial")
			c.Writer.Flush()
			panic("after the flush")
		}, 0},
		{"the header committed with no body", func(c *gin.Context) {
			c.Status(nethttp.StatusAccepted)
			c.Writer.WriteHeaderNow()
			panic("after the header")
		}, 0},
		{"200 committed with no body", func(c *gin.Context) {
			c.Status(nethttp.StatusOK)
			c.Writer.WriteHeaderNow()
			panic("after the 200")
		}, 0},
		{"101 committed", func(c *gin.Context) {
			c.Status(nethttp.StatusSwitchingProtocols)
			c.Writer.WriteHeaderNow()
			panic("after the 101")
		}, 0},
		{"an interim status forced out", func(c *gin.Context) {
			c.Status(nethttp.StatusEarlyHints)
			c.Writer.WriteHeaderNow()
			panic("after the interim status")
		}, 0},
		{"an interim status and then a body", func(c *gin.Context) {
			c.Status(nethttp.StatusEarlyHints)
			c.Writer.WriteHeaderNow()
			_, _ = c.Writer.WriteString("partial")
			panic("after the body")
		}, 0},
		{"an interim status and then a flush", func(c *gin.Context) {
			c.Status(nethttp.StatusEarlyHints)
			c.Writer.WriteHeaderNow()
			c.Writer.Flush()
			panic("after the flush")
		}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			logger, records := newRecordingLogger()
			responder := &recordingResponder{}
			handler := assembleFallback(t, responder, logger, func(e Endpoint) {
				e.Route("GET", "/things", c.handler)
			})

			serveOnce(handler, nethttp.MethodGet, "/things")

			if got := responder.calls(); got != c.asked {
				t.Errorf("the responder was asked %d times, want %d", got, c.asked)
			}
			if written := records.at(slog.LevelError); len(written) != 1 {
				t.Errorf("the fallback wrote %d error records, want one whether or not it answered: %v",
					len(written), written)
			}
		})
	}
}

// TestAHandlerPanicReachesTheClientAsAProblem is the same statement over a real
// socket, which is where the difference is visible to anyone outside the
// process: with nothing over the handler the client's request fails outright,
// because the connection is closed before any status line is written.
func TestAHandlerPanicReachesTheClientAsAProblem(t *testing.T) {
	addr, records := serveRoutes(t, nil, func(e Endpoint) {
		e.Route("GET", "/boom", panicking("boom"))
	})

	got, err := getResponse(context.Background(), addr, "/boom")
	if err != nil {
		t.Fatalf("the request failed outright rather than being answered: %v. A panic with no "+
			"fallback over it leaves the client with no status at all", err)
	}
	if got.status != nethttp.StatusInternalServerError {
		t.Errorf("the client was answered %d, want %d", got.status, nethttp.StatusInternalServerError)
	}
	if got.header.Get("Content-Type") != "application/problem+json" {
		t.Errorf("the client was answered with the media type %q", got.header.Get("Content-Type"))
	}
	if strings.Contains(got.body, "boom") {
		t.Errorf("the panic value reached the client: %s", got.body)
	}
	if written := records.at(slog.LevelError); len(written) != 1 {
		t.Errorf("the endpoint wrote %d error records for one panic, want one: %v", len(written), written)
	}
}

// TestAnAbortedConnectionIsNotAnsweredAsASuccess pins what a client sees when a
// handler abandons its connection with http.ErrAbortHandler, which is what a
// reverse proxy does when forwarding fails part-way: the connection is dropped,
// and the client's request fails or its body ends in an error. It does not see a
// 200.
//
// That is the one outcome gin's own recovery middleware gets wrong — it swallows
// the panic and lets the response end as an empty 200, so a half-forwarded
// response reads as a complete one — which is why the middleware is not in the
// engine. Only a real connection can show it: a recorder has no connection to
// abandon.
//
// The handler abandons the connection before writing anything in one row and
// after a body has been flushed in the other, the two moments a proxy fails at.
func TestAnAbortedConnectionIsNotAnsweredAsASuccess(t *testing.T) {
	addr, records := serveRoutes(t, nil, func(e Endpoint) {
		e.Route("GET", "/before", panicking(nethttp.ErrAbortHandler))
		e.Route("GET", "/after", func(c *gin.Context) {
			c.String(nethttp.StatusOK, "half of the answer")
			c.Writer.Flush()
			panic(nethttp.ErrAbortHandler)
		})
	})

	before, err := getResponse(context.Background(), addr, "/before")
	if err == nil {
		t.Errorf("a connection abandoned before any output was answered %d %q, want a request that fails",
			before.status, before.body)
	}

	after, err := getResponse(context.Background(), addr, "/after")
	if err == nil && after.bodyErr == nil {
		t.Errorf("a connection abandoned after output ended as a complete response, %d %q: "+
			"the client cannot tell the half answer from a whole one", after.status, after.body)
	}

	if written := records.at(slog.LevelError); len(written) != 0 {
		t.Errorf("abandoning a connection on purpose wrote the error records %v", written)
	}
}

// TestAGoexitClosesTheConnection pins what a handler that ends its goroutine
// costs the client: the response never reaches the connection and the protocol
// layer closes it, so the request fails; and the record that says a handler
// stopped is written.
func TestAGoexitClosesTheConnection(t *testing.T) {
	addr, records := serveRoutes(t, nil, func(e Endpoint) {
		e.Route("GET", "/exit", func(*gin.Context) { runtime.Goexit() })
	})

	got, err := getResponse(context.Background(), addr, "/exit")
	if err == nil {
		t.Errorf("a handler that called Goexit was answered %d %q, want a connection closed", got.status, got.body)
	}
	waitUntil(t, "the record of the handler that stopped", func() bool {
		return len(records.at(slog.LevelError)) == 1
	})
	mustContain(t, records.at(slog.LevelError)[0], "Goexit", "the way the handler ended")
}

// TestAPanicAfterOutputOnARealConnectionLeavesTheResponseAsWritten is the
// statement of the same name over a socket, in the two states the response can
// be in when the panic comes: the output still in the server's buffer, and the
// output already flushed to the client. In both the client reads exactly what
// the handler wrote, under the status it committed, and the response ends the
// way a response ends.
func TestAPanicAfterOutputOnARealConnectionLeavesTheResponseAsWritten(t *testing.T) {
	addr, records := serveRoutes(t, nil, func(e Endpoint) {
		e.Route("GET", "/buffered", func(c *gin.Context) {
			c.String(nethttp.StatusTeapot, "partial")
			panic("after the response began")
		})
		e.Route("GET", "/flushed", func(c *gin.Context) {
			c.String(nethttp.StatusTeapot, "partial")
			c.Writer.Flush()
			panic("after the response began")
		})
	})

	for _, path := range []string{"/buffered", "/flushed"} {
		got, err := getResponse(context.Background(), addr, path)
		if err != nil {
			t.Fatalf("%s: the request failed outright: %v", path, err)
		}
		if got.status != nethttp.StatusTeapot {
			t.Errorf("%s: the client was answered %d, want the %d the handler committed", path,
				got.status, nethttp.StatusTeapot)
		}
		if got.body != "partial" || got.bodyErr != nil {
			t.Errorf("%s: the client read %q with the error %v, want exactly %q: nothing the fallback "+
				"writes belongs after bytes already sent", path, got.body, got.bodyErr, "partial")
		}
	}

	if written := records.at(slog.LevelError); len(written) != 2 {
		t.Errorf("the endpoint wrote %d error records for two panics, want one each (and not a "+
			"second status written to a response already begun): %v", len(written), written)
	}
	for _, line := range records.at(slog.LevelError) {
		if strings.Contains(line, "superfluous") {
			t.Errorf("net/http reported a write the fallback made after the response began: %s", line)
		}
	}
}

// TestAPanicAfterAnInterimResponseForcedOutIsNotAnswered pins where "the
// response has started" falls once the recovery layer reads gin's own account of
// the writer. A handler that sends an informational response — 103 Early Hints
// is the common one — has to force the header out to send it, and gin counts a
// forced header as the response having started whatever the status was. The
// panic after it is therefore not answered: the response is left as it is, and
// the client reads the interim status and then the 200 net/http supplies for a
// response that ends without a final status.
//
// This is narrower than the net/http layer this replaced, whose writer counted
// only a final status as the start. The change follows from reading gin's writer
// directly; what does not change is a response that has committed a final
// status, or one that has written no header at all.
func TestAPanicAfterAnInterimResponseForcedOutIsNotAnswered(t *testing.T) {
	addr, records := serveRoutes(t, nil, func(e Endpoint) {
		e.Route("GET", "/early", func(c *gin.Context) {
			c.Header("Link", "</style.css>; rel=preload")
			c.Status(nethttp.StatusEarlyHints)
			c.Writer.WriteHeaderNow()
			panic("after the early hints")
		})
	})

	interim, got := getTracingInterim(t, addr, "/early")

	if len(interim) != 1 || interim[0] != nethttp.StatusEarlyHints {
		t.Fatalf("the client saw the informational responses %v, want the one 103 the handler sent, "+
			"so this case did not exercise what it is about", interim)
	}
	if got.status != nethttp.StatusOK || got.body != "" {
		t.Errorf("the final answer was %d %q, want the empty 200 an interim-only response ends with: "+
			"the panic after a sent interim status is not answered", got.status, got.body)
	}
	if written := records.at(slog.LevelError); len(written) != 1 {
		t.Errorf("the recovery layer wrote %d error records for the panic, want one: the record is "+
			"the only signal that the request did not finish: %v", len(written), written)
	}
}

// TestFailAfterAnInterimResponseWritesNothing pins a limit of Fail that is
// documented in the design and is not the behaviour anyone wants.
//
// Whether Fail may still write is decided by gin's own account of the response,
// Context.Writer.Written, and gin counts the header as written once a handler
// has forced it out, an informational status included. A handler that sends a
// 103 Early Hints and then refuses the request through Fail therefore gets no
// error response: Fail writes nothing, net/http follows the 103 with an empty
// 200, and the client reads a success.
//
// The recovery layer reads the same account, so a panic after a forced interim
// status is left unanswered the same way. The test pins what happens today, so
// that a change to the criterion is noticed and made on purpose, and it is not a
// statement that an empty 200 is the right answer to a refusal.
func TestFailAfterAnInterimResponseWritesNothing(t *testing.T) {
	addr, records := serveRoutes(t, nil, func(e Endpoint) {
		early := func(c *gin.Context) {
			c.Header("Link", "</style.css>; rel=preload")
			c.Status(nethttp.StatusEarlyHints)
			c.Writer.WriteHeaderNow()
		}
		e.Route("GET", "/fail", func(c *gin.Context) {
			early(c)
			Fail(c, Problem{Status: nethttp.StatusServiceUnavailable})
		})
		e.Route("GET", "/panic", func(c *gin.Context) {
			early(c)
			panic("after the early hints")
		})
	})

	interim, failed := getTracingInterim(t, addr, "/fail")
	if len(interim) != 1 || interim[0] != nethttp.StatusEarlyHints {
		t.Fatalf("the client saw the informational responses %v, want the one 103", interim)
	}
	if failed.status != nethttp.StatusOK || failed.body != "" {
		t.Errorf("a refusal through Fail after a 103 was answered %d %q; the known limit is an empty "+
			"200, and a different answer means Fail's criterion changed", failed.status, failed.body)
	}

	_, panicked := getTracingInterim(t, addr, "/panic")
	if panicked.status != nethttp.StatusOK || panicked.body != "" {
		t.Errorf("a panic after a 103 was answered %d %q, want the same empty 200 Fail gives: the "+
			"recovery layer reads the same criterion Fail does", panicked.status, panicked.body)
	}
	if written := records.at(slog.LevelError); len(written) != 1 {
		t.Errorf("the recovery layer wrote %d error records for the panic, want one: %v", len(written), written)
	}
}

// TestAnInvalidStatusCodeIsAnsweredAsAnEmptySuccess pins what a handler that
// writes a status net/http refuses gets. net/http panics on a code outside 100
// through 999, and gin records the response as started before it hands the
// status to the underlying writer, so the recovery layer leaves the response
// alone and the panic is stopped inside it: the attempted status never reaches
// the wire, and net/http answers the handler that returned with the 200 it
// supplies for a response that ended without a final status.
//
// This differs from the net/http layer this replaced, which recorded the start
// only after the status was accepted and answered the panic with a 500. The
// difference follows from reading gin's own account of the writer; the record
// the recovery layer wrote is what still says the request did not finish.
func TestAnInvalidStatusCodeIsAnsweredAsAnEmptySuccess(t *testing.T) {
	addr, records := serveRoutes(t, nil, func(e Endpoint) {
		e.Route("GET", "/above", func(c *gin.Context) { c.String(1000, "hello") })
		e.Route("GET", "/below", func(c *gin.Context) { c.String(99, "hello") })
	})

	for _, path := range []string{"/above", "/below"} {
		got, err := getResponse(context.Background(), addr, path)
		if err != nil {
			t.Fatalf("%s: the request failed outright: %v", path, err)
		}
		if got.status != nethttp.StatusOK || got.body != "" {
			t.Errorf("%s: a handler that wrote an invalid status was answered %d %q, want the empty "+
				"200 the refused status leaves", path, got.status, got.body)
		}
	}
	written := records.at(slog.LevelError)
	if len(written) != 2 {
		t.Fatalf("the endpoint wrote %d error records for two panics, want one each: %v", len(written), written)
	}
	for _, line := range written {
		mustContain(t, line, "invalid WriteHeader code", "the panic net/http raised for the status")
	}
}

// TestAWrappedAbortHandlerAbandonsTheConnection is the statement of
// TestAnAbortedConnectionIsNotAnsweredAsASuccess for the sentinel wrapped, which
// is how a library that adds context to it throws it. net/http matches the sentinel by identity, so a
// wrapper reaches it as an ordinary panic; the fallback lets the wrapper through
// all the same, so the connection is abandoned as the handler asked and no error
// response is written for it. The fallback writes no record of its own either —
// net/http logs the panic it did not recognise, which is its record, not this
// one's.
func TestAWrappedAbortHandlerAbandonsTheConnection(t *testing.T) {
	wrapped := func() error { return fmt.Errorf("proxy: the upstream went away: %w", nethttp.ErrAbortHandler) }
	addr, records := serveRoutes(t, nil, func(e Endpoint) {
		e.Route("GET", "/before", func(*gin.Context) { panic(wrapped()) })
		e.Route("GET", "/after", func(c *gin.Context) {
			c.String(nethttp.StatusOK, "half of the answer")
			c.Writer.Flush()
			panic(wrapped())
		})
	})

	before, err := getResponse(context.Background(), addr, "/before")
	if err == nil {
		t.Errorf("a connection abandoned with a wrapped ErrAbortHandler before any output was answered "+
			"%d %q, want a request that fails", before.status, before.body)
	}
	after, err := getResponse(context.Background(), addr, "/after")
	if err == nil && after.bodyErr == nil {
		t.Errorf("a connection abandoned with a wrapped ErrAbortHandler after output ended as a complete "+
			"response, %d %q", after.status, after.body)
	}

	for _, line := range records.at(slog.LevelError) {
		if strings.Contains(line, "the endpoint's recovery layer caught it") {
			t.Errorf("the recovery layer wrote a record for a panic it lets through: %s", line)
		}
	}
}

// TestABareStatusThenAPanicIsAnswered pins the criterion of "the response has
// started" at the edge it turns on. A status set with c.Status alone is not on
// the wire — gin keeps it in its writer until a header has to go out — so a
// handler that sets one and then panics is answered with the error response, and
// the 201 it never sent is not what the client reads. A criterion that counted
// the set status as the start would leave the client that 201.
func TestABareStatusThenAPanicIsAnswered(t *testing.T) {
	logger, _ := newRecordingLogger()
	handler := assembleFallback(t, nil, logger, func(e Endpoint) {
		e.Route("GET", "/things", func(c *gin.Context) {
			c.Status(nethttp.StatusCreated)
			panic("after the bare status")
		})
	})

	recorder := serveOnce(handler, nethttp.MethodGet, "/things")

	if recorder.Code != nethttp.StatusInternalServerError {
		t.Errorf("a handler that set a 201 with c.Status and then panicked was answered %d, want %d: a "+
			"status the writer is holding is not a response that has started", recorder.Code,
			nethttp.StatusInternalServerError)
	}
}
