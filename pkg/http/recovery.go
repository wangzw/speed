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

func recovery(endpoint string, localizer i18n.Localizer) gin.HandlerFunc {
	return func(c *gin.Context) {
		returned := false
		defer func() {
			thrown := recover()
			if thrown == nil {
				if returned {
					return
				}
				log.FromContext(c.Request.Context()).Error("a request's handler stopped without returning "+
					"and without a panic value, by runtime.Goexit or by panic(nil) under "+
					"GODEBUG=panicnil=1, and the endpoint's recovery layer answered it",
					"endpoint", endpoint,
					"stack", stackTrace(),
				)
				answerDefect(c, errUnwound, localizer)
				return
			}
			// ErrAbortHandler is let past untouched, so the connection is closed the way the
			// handler asked and the stack is not written for a client that went away.
			if err, isErr := thrown.(error); isErr && errors.Is(err, nethttp.ErrAbortHandler) {
				panic(thrown)
			}
			// A handler may refuse by panicking with a fault it declared; that is a deliberate answer, not a defect, so it is written without a stack and only when the operation declared its status. A fault on a route with no declaration, or a status it did not declare, is a defect like any other panic, as is a fault asking for an undeclared header.
			if fault, ok := thrown.(interface{ Problem() Problem }); ok {
				if declared, ok := c.Get(declaredFailuresContextKey); ok {
					if responses, ok := declared.(map[int]Response); ok {
						if response, found := responses[fault.Problem().Status]; found {
							headers := faultHeadersOf(thrown)
							if reason := checkDynamicHeaders(headers, response); reason == "" {
								c.Abort()
								if responderFrom(c.Request.Context()) == nil {
									discardRepresentationHeaders(c.Writer.Header())
								}
								for _, header := range headers {
									c.Header(header.Name, header.Value)
								}
								Fail(c, fault.Problem())
								return
							}
						}
					}
				}
			}
			// The stack is taken here, at the recover point, so it is the stack of this request
			// rather than of a rethrow further out.
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

func faultHeadersOf(thrown any) []HeaderValue {
	switch fault := thrown.(type) {
	case Fault:
		return fault.Headers
	case *Fault:
		if fault != nil {
			return fault.Headers
		}
	}
	return nil
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

type handlerDefect struct{}

func (handlerDefect) Error() string { return "the request handler did not return normally" }

func answerDefect(c *gin.Context, cause error, localizer i18n.Localizer) {
	c.Abort()
	_ = c.Error(handlerDefect{})
	if c.Writer.Written() {
		return
	}
	// A panic is a handler that failed without deciding anything about its response, so the headers it set describe a response it did not send; the default shape drops the ones that describe content. A delivered responder decides its own body and is handed the writer as the caller left it.
	if responderFrom(c.Request.Context()) == nil {
		discardRepresentationHeaders(c.Writer.Header())
	}
	Fail(c, Problem{
		Status: nethttp.StatusInternalServerError,
		Title:  refusalTitle(c.Request.Context(), localizer, nethttp.StatusInternalServerError),
		Cause:  cause,
	})
}

// representationHeaders describe the content a handler meant to send: a validator, a range, a download name, and the cache directives. An error response written in the handler's place is not that content, so the default shape does not inherit them; a 500 carrying the handler's "Cache-Control: public" would be kept by a cache as the answer.
var representationHeaders = []string{
	"Content-Disposition", "ETag", "Last-Modified", "Content-Range", "Accept-Ranges",
	"Cache-Control", "Expires",
}

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
