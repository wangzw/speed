package http

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"runtime"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
)

// recoveryName is the panic recovery layer as it appears in the assembled
// chain's description.
const recoveryName = "http:recovery"

// recovery is the built-in layer that catches what no other layer caught: a
// panic raised anywhere further in the chain, in a registered middleware or in a
// handler.
//
// It is a gin handler on the engine, not a net/http layer around it, and its
// place is inside the observe stage and outside every layer that can refuse a
// request. Inside the observe stage, so an observer — the tracing layer among
// them — sees a recovered request as a normal return whose response is a 500,
// with the status and the error list the metrics and the span are built from. A
// recovery outside the observers would let the panic pass through them first,
// and a tracing layer whose c.Next never returns ends its span with no status
// and skips everything it measures after the call.
//
// It lets http.ErrAbortHandler past instead of answering it. That sentinel is a
// handler asking for its connection to be abandoned, which net/http matches by
// identity and answers by closing the connection; answered here it would become
// a 500 written to a socket the handler gave up on, and a record for a client
// that simply went away. gin's own recovery middleware is not used for the same
// reason: it turns the sentinel into an empty 200. A wrapped sentinel reaches
// net/http as an ordinary panic, so a wrapper is let past as well and the
// connection is still closed, by the other path.
//
// One goroutine's panic is all it covers. A module that panics in a goroutine of
// its own takes that process down whatever is wrapped around the request path.
func recovery(endpoint string, localizer i18n.Localizer) gin.HandlerFunc {
	return func(c *gin.Context) {
		returned := false
		defer func() {
			thrown := recover()
			if thrown == nil {
				if returned {
					return
				}
				// Nothing was recovered and the handler never returned. It
				// called runtime.Goexit, which unwinds the stack with no value,
				// or it panicked with nil under GODEBUG=panicnil=1, which the
				// runtime otherwise rewrites to a *runtime.PanicNilError. The
				// two cannot be told apart here. Either is a defect that gets
				// its record, and neither is a request that finished; the record
				// says which kind of defect it is rather than calling it a
				// panic. The response is written as for a panic: after a Goexit
				// it never reaches the connection, and after a nil panic it is
				// the answer.
				log.FromContext(c.Request.Context()).Error("a request's handler stopped without returning "+
					"and without a panic value, by runtime.Goexit or by panic(nil) under "+
					"GODEBUG=panicnil=1, and the endpoint's recovery layer answered it",
					"endpoint", endpoint,
					"stack", stackTrace(),
				)
				answerDefect(c, errUnwound, localizer)
				return
			}
			// ErrAbortHandler is let past untouched, so the connection is closed
			// the way the handler asked and the stack is not written for a
			// client that went away. A wrapped one is let past too; see the
			// layer's own comment.
			if err, isErr := thrown.(error); isErr && errors.Is(err, nethttp.ErrAbortHandler) {
				panic(thrown)
			}
			// A handler may refuse by panicking with a fault it declared; that is
			// a deliberate answer, not a defect, so it is written without a
			// stack and only when the operation declared its status. A fault on
			// a route with no declaration, or a status it did not declare, is a
			// defect like any other panic.
			if fault, ok := thrown.(interface{ Problem() Problem }); ok {
				if statuses, declared := c.Get(declaredFailuresContextKey); declared {
					set, _ := statuses.(map[int]struct{})
					if _, ok := set[fault.Problem().Status]; ok {
						c.Abort()
						if responderFrom(c.Request.Context()) == nil {
							discardRepresentationHeaders(c.Writer.Header())
						}
						Fail(c, fault.Problem())
						return
					}
				}
			}
			// The stack is taken here, at the recover point, so it is the stack
			// of this request rather than of a rethrow further out.
			log.FromContext(c.Request.Context()).Error("a request panicked and the endpoint's recovery layer caught it",
				"endpoint", endpoint,
				"panic", thrown,
				"stack", stackTrace(),
			)
			answerDefect(c, panicError{value: thrown}, localizer)
		}()
		c.Next()
		returned = true
	}
}

// errUnwound is what the Cause of a defect's problem wraps for a handler that
// stopped without returning and without a panic value, so that the Cause is
// never a nil an ErrorResponder would have to read as "nothing happened".
var errUnwound = errors.New("the handler stopped without returning and without a panic value")

// panicError is the Cause of the problem the recovery layer writes for a panic.
// It carries the recovered value; when that value is itself an error it is
// reachable through Unwrap, so an ErrorResponder can match it with errors.As.
type panicError struct{ value any }

func (p panicError) Error() string { return fmt.Sprintf("panic: %v", p.value) }

func (p panicError) Unwrap() error {
	err, _ := p.value.(error)
	return err
}

// handlerDefect is the error the recovery layer records in the gin context's
// error list for a request it answered.
//
// Its type is what otelgin reads as the span's error.type, so it is a closed
// type that carries no panic value. otelgin copies the error list's text into
// the span's status description, and a panic value is whatever the failing code
// had to hand — an error from a driver with a connection string in it, a string
// with a token — so putting it there would spread it across the traces. It
// carries more of the cause than the type name alone for the same reason the
// Cause of the problem does, and that is where the panic value is kept.
type handlerDefect struct{}

func (handlerDefect) Error() string { return "the request handler did not return normally" }

// answerDefect ends the chain and writes the one error response the recovery
// layer writes on its own: a 500 whose Cause is what went wrong, in the shape
// the delivered ErrorResponder decides, or in the default shape when none is
// delivered. The default does not write the Cause into the body, so a panic
// value never reaches a client.
//
// The answer is written only when the response has not started to go out, which
// gin's writer reports through Context.Writer.Written: a status written with
// c.Status alone does not count, so a handler that set a status and then
// panicked is still answered, while a response whose header has been forced out
// or whose body has begun is left as it is — its status cannot change, and a
// body written now would follow the one already on the wire. The record is
// written either way, since with the response unchangeable it is the only signal
// that the request did not finish.
func answerDefect(c *gin.Context, cause error, localizer i18n.Localizer) {
	c.Abort()
	// A fixed-type error goes into the error list so that the observability
	// layer marks the span failed and names an error.type; see handlerDefect.
	// Its return is the entry gin appended and nothing here reads it back.
	_ = c.Error(handlerDefect{})
	if c.Writer.Written() {
		return
	}
	// A panic is a handler that failed without having decided anything about its
	// response, so the headers it set describe a response it did not send; the
	// default shape drops the ones that describe content. A delivered responder
	// decides its own body and is handed the writer as the caller left it.
	if responderFrom(c.Request.Context()) == nil {
		discardRepresentationHeaders(c.Writer.Header())
	}
	Fail(c, Problem{
		Status: nethttp.StatusInternalServerError,
		Title:  refusalTitle(c.Request.Context(), localizer, nethttp.StatusInternalServerError),
		Cause:  cause,
	})
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

// stackTrace is the calling goroutine's stack as a string, bounded the way
// net/http bounds the one it writes for a panic it catches itself.
func stackTrace() string {
	const size = 64 << 10
	buf := make([]byte, size)
	return string(buf[:runtime.Stack(buf, false)])
}
