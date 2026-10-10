package http

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	nethttp "net/http"
	"runtime"
	"sync/atomic"

	"github.com/vislake/speed/pkg/log"
)

// The layers this module puts around the registered chain itself, as they
// appear in the assembled chain's description. Both sit outside every
// registered layer and neither is on the registration surface, so a reader
// comparing the line with the code would otherwise find work applied by
// something the line does not mention.
const (
	baseName     = "http:base"
	fallbackName = "http:fallback"
)

// fallback is the layer that catches what no registered layer caught.
//
// It is a net/http layer around the endpoint's gin engine, not a gin
// middleware, and it sits inside the base and outside every registered layer.
// Inside the base, because the base is what puts the logger into the request
// context and a record written here has to reach the configured destinations
// rather than the process default. Outside the engine, because gin's own
// recovery swallows http.ErrAbortHandler and answers it with an empty 200,
// which this layer must not do, and because a panic raised anywhere on the
// rest of the request path — in a handler, in a middleware, in the router —
// has to reach it.
//
// It holds the request as the base passed it, so the fields a registered layer
// binds further in are not on the record it writes: those are bound to a
// context derived from this one. What it writes therefore names the endpoint
// and the panic, not the subject or the tenant.
//
// It hands the rest of the path a writer that notes when the response starts
// to go out. Once it has, the status code and whatever body was written stand,
// and a response written after them would only be appended to that body, so
// the layer answers only a request whose response had not started.
//
// One goroutine's panic is all it covers. A module that panics in a goroutine
// of its own takes that process down whatever is wrapped around the request
// path, and this layer is no exception.
func fallback(next nethttp.Handler, endpoint string, responder ErrorResponder) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		sw := &startedWriter{ResponseWriter: w}
		// The rest of the path is handed the writer as a Pusher only when the
		// one underneath is one. gin hands a handler a nil Pusher for a writer
		// that is not, and handlers test for it: a writer that always answered
		// would turn "skip" into "try and fail" on every HTTP/1.1 request.
		var handed nethttp.ResponseWriter = sw
		if _, pushes := w.(nethttp.Pusher); pushes {
			handed = startedPusher{sw}
		}
		returned := false
		defer func() {
			thrown := recover()
			if thrown == nil {
				if returned {
					return
				}
				// Nothing was recovered and the handler never returned. It
				// called runtime.Goexit, which unwinds the stack with no
				// value, or it panicked with nil under GODEBUG=panicnil=1,
				// which the runtime otherwise rewrites to a
				// *runtime.PanicNilError. The two cannot be told apart here.
				// Either is a defect that gets its record, and neither is a
				// request that finished; the record says which kind of
				// defect it is rather than calling it a panic. The response
				// is written as for a panic: after a Goexit it never reaches
				// the connection, and after a nil panic it is the answer.
				log.FromContext(r.Context()).Error("a request's handler stopped without returning "+
					"and without a panic value, by runtime.Goexit or by panic(nil) under "+
					"GODEBUG=panicnil=1, and the endpoint's fallback answered it",
					"endpoint", endpoint,
					"stack", stackTrace(),
				)
				writeDefectResponse(sw, r, errUnwound, responder)
				return
			}
			// ErrAbortHandler is a handler asking for its connection to be
			// abandoned, not a defect: net/http suppresses the stack for it
			// and closes the connection. Answered here, a client that went
			// away mid-response would instead be handed a 500 written to a
			// broken socket, and the log would carry an error for it.
			//
			// A wrapped one is let through as well. net/http matches the
			// sentinel by identity, so a wrapper around it reaches net/http
			// as an ordinary panic — which logs and closes the connection,
			// not a status written to the socket the handler gave up on.
			if err, isErr := thrown.(error); isErr && errors.Is(err, nethttp.ErrAbortHandler) {
				panic(thrown)
			}
			log.FromContext(r.Context()).Error("a request panicked and the endpoint's fallback caught it",
				"endpoint", endpoint,
				"panic", thrown,
				"stack", stackTrace(),
			)
			writeDefectResponse(sw, r, panicError{value: thrown}, responder)
		}()
		next.ServeHTTP(handed, r)
		returned = true
	})
}

// errUnwound is what the Cause of a defect's problem wraps for a handler that
// stopped without returning and without a panic value, so that the Cause is
// never a nil an ErrorResponder would have to read as "nothing happened".
var errUnwound = errors.New("the handler stopped without returning and without a panic value")

// panicError is the Cause of the problem the fallback writes for a panic. It
// carries the recovered value; when that value is itself an error it is
// reachable through Unwrap, so an ErrorResponder can match it with errors.As.
type panicError struct{ value any }

func (p panicError) Error() string { return fmt.Sprintf("panic: %v", p.value) }

func (p panicError) Unwrap() error {
	err, _ := p.value.(error)
	return err
}

// writeDefectResponse writes the one error response the fallback writes on its
// own: a 500 whose Cause is what went wrong, in the shape the delivered
// ErrorResponder decides, or in the default shape when none is delivered. The
// default does not write the Cause into the body, so a panic value never
// reaches a client.
//
// A response that has already started to go out is left as it is: its status
// code cannot change, and a body written now would follow the one already on
// the wire. The request ends in the shape it had, and the record the fallback
// wrote before calling this is what tells the defect happened.
func writeDefectResponse(w *startedWriter, r *nethttp.Request, cause error, responder ErrorResponder) {
	if w.started.Load() {
		return
	}
	if responder == nil {
		discardRepresentationHeaders(w.Header())
	}
	respondProblem(w, r, Problem{
		Status: nethttp.StatusInternalServerError,
		Cause:  cause,
	}, responder)
}

// startedWriter is the writer the fallback hands to the rest of the request
// path. It records whether the response has started to go out, and forwards
// what the writer underneath carries beyond http.ResponseWriter: gin asserts
// http.Flusher, http.Hijacker, http.CloseNotifier and http.Pusher on the
// writer it wraps, and a handler reaches the rest through
// http.NewResponseController, which finds them by Unwrap.
//
// A response has started once a status of 200 or above is written — an
// informational 1xx other than 101 leaves the final status open — or a byte of
// body, a flush or a hijack happens: the header is on its way or the
// connection is no longer the server's.
type startedWriter struct {
	nethttp.ResponseWriter
	started atomic.Bool
}

// WriteHeader records the start only once the writer underneath has taken the
// status: net/http panics on a code it does not accept, and a response whose
// status was refused has not started.
func (s *startedWriter) WriteHeader(code int) {
	s.ResponseWriter.WriteHeader(code)
	if code >= nethttp.StatusOK || code == nethttp.StatusSwitchingProtocols {
		s.started.Store(true)
	}
}

func (s *startedWriter) Write(b []byte) (int, error) {
	s.started.Store(true)
	return s.ResponseWriter.Write(b)
}

// Flush flushes the writer underneath, which makes the response start. It
// satisfies http.Flusher, which gin asserts on the writer it wraps; a writer
// underneath that cannot flush is flushed to no effect, the way gin's own
// Flush treats it.
func (s *startedWriter) Flush() {
	s.started.Store(true)
	_ = nethttp.NewResponseController(s.ResponseWriter).Flush()
}

// Hijack hands the connection over, after which nothing more can be written to
// it as a response. A hijack the writer underneath refuses, as an HTTP/2 one
// does, leaves the response as it was.
func (s *startedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := nethttp.NewResponseController(s.ResponseWriter).Hijack()
	if err == nil {
		s.started.Store(true)
	}
	return conn, rw, err
}

// CloseNotify forwards the notification when the writer underneath has it.
// http.CloseNotifier has been deprecated since Go 1.11 in favour of the
// request's context, but gin's writer asserts it on the writer it wraps and
// panics without it, so a handler calling it through gin would crash behind
// this layer. The assertion names the method rather than the deprecated
// interface. A writer without it yields a channel that never becomes ready.
func (s *startedWriter) CloseNotify() <-chan bool {
	if notifier, ok := s.ResponseWriter.(interface{ CloseNotify() <-chan bool }); ok {
		return notifier.CloseNotify()
	}
	return nil
}

// startedPusher is the writer handed down when the one underneath can push: it
// carries everything startedWriter does and forwards the HTTP/2 server push.
type startedPusher struct{ *startedWriter }

// Push forwards the server push to the writer underneath, which implements it
// whenever a startedPusher was made.
func (s startedPusher) Push(target string, opts *nethttp.PushOptions) error {
	if pusher, ok := s.ResponseWriter.(nethttp.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return nethttp.ErrNotSupported
}

// representationHeaders are the headers that describe the content a handler
// meant to send: a validator, a range, a download name, and the directives that
// let a cache keep it. An error response written in the handler's place is not
// that content, so the default shape does not inherit them; a 500 carrying the
// handler's "Cache-Control: public" would be kept by a cache as the answer.
var representationHeaders = []string{
	"Content-Disposition", "ETag", "Last-Modified", "Content-Range", "Accept-Ranges",
	"Cache-Control", "Expires",
}

// discardRepresentationHeaders removes the headers describing a response that
// is not going to be sent. Every other header the handler set stays.
func discardRepresentationHeaders(h nethttp.Header) {
	for _, name := range representationHeaders {
		h.Del(name)
	}
}

// Unwrap exposes the writer underneath, which is how
// http.NewResponseController reaches what a plain http.ResponseWriter does not
// carry: read and write deadlines among them.
func (s *startedWriter) Unwrap() nethttp.ResponseWriter { return s.ResponseWriter }

// stackTrace is the calling goroutine's stack as a string, bounded the way
// net/http bounds the one it writes for a panic it catches itself.
func stackTrace() string {
	const size = 64 << 10
	buf := make([]byte, size)
	return string(buf[:runtime.Stack(buf, false)])
}
