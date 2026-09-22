package http

import (
	"errors"
	nethttp "net/http"
	"runtime"

	"github.com/vislake/speed/pkg/log"
)

// PanicResponder decides what an endpoint's fallback writes when it catches a
// panic. A module that delivers it takes that response over; with none
// delivered the fallback writes the status alone and no body.
//
// It is taken up as an optional dependency, the way Logger is, and absence is
// the ordinary case: the response to a defect is not something this module has
// an opinion about, so the interface exists for the module that owns an API's
// error shape to put that shape on the one response written here.
type PanicResponder interface {
	// RespondPanic writes the response for a request whose handler, or one of
	// the middleware layers registered on its endpoint, panicked. thrown is
	// the value recovered from the panic.
	//
	// The header may already have been committed by whatever panicked:
	// writing it again changes nothing, and net/http reports the write as
	// superfluous to the server's own log.
	RespondPanic(w nethttp.ResponseWriter, r *nethttp.Request, thrown any)
}

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
// It sits inside the base and outside every registered layer. Inside, because
// the base is what puts the logger into the request context and a record
// written here has to reach the configured destinations rather than the
// process default. Outside, because a panic raised anywhere on the rest of the
// request path — in a handler, in a middleware, in the routing engine — has to
// reach it.
//
// It holds the request as the base passed it, so the fields a registered layer
// binds further in are not on the record it writes: those are bound to a
// context derived from this one. What it writes therefore names the endpoint
// and the panic, not the subject or the tenant.
//
// One goroutine's panic is all it covers. A module that panics in a goroutine
// of its own takes that process down whatever is wrapped around the request
// path, and this layer is no exception.
func fallback(next nethttp.Handler, endpoint string, responder PanicResponder) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		returned := false
		defer func() {
			thrown := recover()
			if thrown == nil {
				if returned {
					return
				}
				// A recovered nil is a panic(nil), not a request that
				// finished: the runtime rewrites panic(nil) to a
				// *runtime.PanicNilError, but that rests on the current
				// semantics, and reading the two alike would answer such a
				// panic with an empty 200 and no record at all.
				thrown = errNilPanic
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
			writePanicResponse(w, r, thrown, responder)
		}()
		next.ServeHTTP(w, r)
		returned = true
	})
}

// errNilPanic stands for a panic whose recovered value is nil, which the
// fallback reports as a defect like any other. It exists so that a recovered
// nil and a request that returned are not read as the same thing.
var errNilPanic = errors.New("a request panicked with a nil value")

// writePanicResponse writes the one response this module writes on its own.
//
// Which status a defect deserves is not a question with a second answer, so
// the default carries no body: it does not claim any API's error shape. A
// module that does own one delivers a PanicResponder and writes it itself.
func writePanicResponse(w nethttp.ResponseWriter, r *nethttp.Request, thrown any, responder PanicResponder) {
	if responder != nil {
		responder.RespondPanic(w, r, thrown)
		return
	}
	w.WriteHeader(nethttp.StatusInternalServerError)
}

// stackTrace is the calling goroutine's stack as a string, bounded the way
// net/http bounds the one it writes for a panic it catches itself.
func stackTrace() string {
	const size = 64 << 10
	buf := make([]byte, size)
	return string(buf[:runtime.Stack(buf, false)])
}
