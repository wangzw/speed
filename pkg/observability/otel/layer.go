package otel

import (
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

// attrEndpointName is the one attribute the server span carries that the
// upstream instrumentation does not: the endpoint name the configuration lists,
// the only place the span records which endpoint a request arrived on.
var attrEndpointName = attribute.Key("speed.endpoint.name")

// layers describes the two layers this module registers for one endpoint, in the
// order they are registered: the instrumentation, then the guard inside it.
//
// The two are separate because the guard binds fields from the span the
// instrumentation started, so it has to run inside it, and because the
// instrumentation stands for the two capabilities this module delivers while the
// guard must stand for nothing: a second layer standing for them would split
// what another layer's After or Before points at.
func (p *providers) layers(endpoint string) []http.Middleware {
	return []http.Middleware{p.instrumentation(), p.guard(endpoint)}
}

// attach registers both layers on the endpoint.
func (p *providers) attach(ep http.Endpoint, endpoint string) {
	for _, layer := range p.layers(endpoint) {
		ep.Use(layer)
	}
}

// instrumentation is the outer layer: the upstream gin instrumentation on its
// own. It opens the request's span, runs the rest of the chain, and on the way
// back reads the writer's status and the request's error list to set the span's
// status, attributes and duration and to record the duration histogram.
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
//
// It sits in the observing stage at OrderOutermost, so it is the outermost
// layer of that stage: every other observing layer runs inside it and the time
// they spend is in the span. The stage places every observing layer outside
// every layer that can refuse a request, which is what lets the span carry the
// response a refusal produced.
func (p *providers) instrumentation() http.Middleware {
	return http.Middleware{
		Name: ModuleName,
		Provides: []core.Token{
			(*observability.TracerProvider)(nil),
			(*observability.MeterProvider)(nil),
		},
		Stage: http.StageObserve,
		Order: http.OrderOutermost,
		Handler: otelgin.Middleware("",
			otelgin.WithTracerProvider(p.tp),
			otelgin.WithMeterProvider(p.mp),
			otelgin.WithPropagators(observability.Propagator()),
		),
	}
}

// guard is the inner layer. It stands between the upstream instrumentation and
// the rest of the chain and does the two things the instrumentation does not.
//
// It belongs to the same observing stage as the instrumentation and sits inside
// it by declaring After(TracerProvider), which is a constraint within the stage:
// a cross-stage constraint would be redundant, because the stage order already
// places every observer outside every layer that can refuse a request.
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
// It does not touch a panic, and a recover here would be wrong twice over. The
// entry module's own recovery runs inside this layer and inside every other
// observing layer, so a handler's panic is caught there and never reaches the
// guard: a status or an error the guard wrote for it would be describing a
// request the recovery had already answered, and one it wrote after the
// recovery had answered could not change the answer. The only panic the guard
// can still see is one the recovery deliberately lets through — net/http's
// ErrAbortHandler, with which a handler asks for its connection to be dropped —
// and recovering that one would swallow it, leaving the protocol layer nothing
// to close the connection with. A chain that stops without returning normally,
// by that sentinel or by a runtime.Goexit, therefore leaves the span as the
// instrumentation's deferred span.End leaves it: ended, with no status this
// module set, and in no histogram.
func (p *providers) guard(endpoint string) http.Middleware {
	return http.Middleware{
		Name:  ModuleName + ":guard",
		Stage: http.StageObserve,
		After: []core.Token{(*observability.TracerProvider)(nil)},
		Handler: func(c *gin.Context) {
			ctx := c.Request.Context()
			span := trace.SpanFromContext(ctx)
			span.SetAttributes(attrEndpointName.String(endpoint))
			ctx = log.WithAttrs(ctx,
				observability.TraceIDKey, span.SpanContext().TraceID().String(),
				observability.SpanIDKey, span.SpanContext().SpanID().String(),
			)
			c.Request = c.Request.WithContext(ctx)

			c.Next()
		},
	}
}
