package otel

import (
	"errors"
	"fmt"
	nethttp "net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

const (
	// layerOrder places the layer outside the layers it should time. It is a
	// preference, not a guarantee: a layer registered with a smaller order, or
	// one nothing points at, ends up outside this one, and the time it spends
	// is then not in the span.
	layerOrder = -10

	// raisedKey is the gin context key under which the guard hands a recovered
	// panic value to the outer layer, which throws it again once the upstream
	// instrumentation has recorded the request.
	raisedKey = "speed.observability.raised"
)

// attrEndpointName is the one attribute the server span carries that the
// upstream instrumentation does not: the endpoint name the configuration lists,
// the only place the span records which endpoint a request arrived on.
var attrEndpointName = attribute.Key("speed.endpoint.name")

// handlerPanic is the error the guard files against a request whose handler
// panicked. The upstream instrumentation reads the request's errors to mark the
// span failed and to set its error type, so the type's name is what an operator
// filters on. The panic value is not carried: it reaches the span as an event and
// the log through the entry module's fallback, and a status description would
// copy it into every system that renders one.
type handlerPanic struct{}

func (handlerPanic) Error() string { return "handler panicked" }

// errUnwound is the text the span's exception event carries for a chain that
// stopped without a panic value.
var errUnwound = errors.New("the handler stopped without returning and without a panic value")

// layers describes the two layers this module registers for one endpoint, in the
// order they are registered: the instrumentation, then the guard inside it.
//
// The two are separate because the upstream instrumentation ends its span as a
// panic passes through it and records nothing on the way. Whatever has to stop
// the panic before it reaches the instrumentation has to sit inside it, and the
// handler that binds the log fields needs the span the instrumentation started,
// so it sits inside as well.
func (p *providers) layers(endpoint string) []http.Middleware {
	return []http.Middleware{p.instrumentation(), p.guard(endpoint)}
}

// attach registers both layers on the endpoint.
func (p *providers) attach(ep http.Endpoint, endpoint string) {
	for _, layer := range p.layers(endpoint) {
		ep.Use(layer)
	}
}

// instrumentation is the outer layer: the upstream gin instrumentation, plus the
// throw that gives the panic back once the request has been recorded.
//
// Provides names the capabilities this module actually delivers rather than a
// name invented for the layer: another layer's After or Before has to match
// what a module really hands out, or the constraint lands nowhere and only a
// warning says so.
//
// The providers and the propagator are handed over rather than left to the
// upstream's global defaults: the globals are whatever the process set last, and
// this module's product is the one the registry delivered. The server name is
// empty, which the upstream reads as "take it from the request's host".
func (p *providers) instrumentation() http.Middleware {
	record := otelgin.Middleware("",
		otelgin.WithTracerProvider(p.tp),
		otelgin.WithMeterProvider(p.mp),
		otelgin.WithPropagators(observability.Propagator()),
	)
	return http.Middleware{
		Name: ModuleName,
		Provides: []core.Token{
			(*observability.TracerProvider)(nil),
			(*observability.MeterProvider)(nil),
		},
		Order: layerOrder,
		Handler: func(c *gin.Context) {
			record(c)

			// The panic goes on to the entry module's own fallback, which
			// sits outside the engine this layer runs in. Swallowing it here
			// would take over a promise that is not this module's, and
			// writing a response is not this layer's job either.
			if raised, ok := c.Get(raisedKey); ok {
				// A recovered nil is stored as a nil and so thrown as one: the
				// fallback tells that from a request that returned and gives
				// it its own record. After a Goexit this line is never reached.
				panic(raised)
			}
		},
	}
}

// guard is the inner layer. It stands between the upstream instrumentation and
// the rest of the chain and does three things the instrumentation does not.
//
// It binds the span's identifiers into the request context for the log module.
// The attribute keys are this module's, bound here and read by the log module
// without it knowing what they mean. The logger itself is injected upstream, by
// whoever built this context.
//
// It names the endpoint on the span. The endpoint name is captured here because
// it is the only place that knows it: the registration surface hands out one
// endpoint at a time, and nothing downstream can recover which one a request
// arrived on.
//
// It stops a panic from the handlers inside it, so the instrumentation returns
// normally and records the request. The status the instrumentation reads is the
// one the writer holds, and while nothing has been written that is no answer yet:
// it is whatever the handler had set or the 200 the writer starts from, and
// reporting it would put every panicking request into the 200 bucket, which is the
// one place an operator most needs it not to be. The status the entry module's
// fallback writes for a panic is an internal error, so that is what the writer is
// given. A module that delivered an ErrorResponder can write another status, and
// this layer cannot see that decision — the module's documentation states it as
// one of the things it does not guarantee.
//
// The recovered value goes to the outer layer, which throws it again, and the
// chain is aborted so that no handler after the one that panicked runs.
//
// A chain that stops without returning and without a value is handled as a
// panic too. recover returns nil for a panic(nil) under GODEBUG=panicnil=1, and
// reading that as a request that returned would stop the panic here: the handlers
// after it would run, the client would get a 200, and the fallback would never
// write its record. A runtime.Goexit also reads as nil, but it keeps unwinding
// after the deferred function returns, so the outer layer never runs and the
// instrumentation records nothing on its way out. The guard marks the span failed
// on the still-open span; the request is then in no histogram, which is the
// instrumentation's own behaviour.
func (p *providers) guard(endpoint string) http.Middleware {
	return http.Middleware{
		Name:  ModuleName + ":guard",
		After: []core.Token{(*observability.TracerProvider)(nil)},
		Order: layerOrder,
		Handler: func(c *gin.Context) {
			finished := false
			ctx := c.Request.Context()
			span := trace.SpanFromContext(ctx)
			span.SetAttributes(attrEndpointName.String(endpoint))
			ctx = log.WithAttrs(ctx,
				observability.TraceIDKey, span.SpanContext().TraceID().String(),
				observability.SpanIDKey, span.SpanContext().SpanID().String(),
			)
			c.Request = c.Request.WithContext(ctx)

			defer func() {
				raised := recover()
				cause := error(nil)
				if raised == nil {
					if finished {
						return
					}
					cause = errUnwound
					span.SetStatus(codes.Error, handlerPanic{}.Error())
				} else {
					cause = fmt.Errorf("%v", raised)
				}
				// The stack is taken here, while the frames of the panic are
				// still on it: once the value is thrown again from the outer
				// layer, the stack the entry module's fallback records starts
				// there. The span is where the origin survives.
				span.RecordError(cause, trace.WithStackTrace(true))
				c.Set(raisedKey, raised)
				_ = c.Error(handlerPanic{})
				if !c.Writer.Written() {
					c.Status(nethttp.StatusInternalServerError)
				}
				c.Abort()
			}()

			c.Next()
			finished = true
		},
	}
}
