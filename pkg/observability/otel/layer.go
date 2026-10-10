package otel

import (
	"fmt"
	nethttp "net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

const (
	// instrumentationName identifies this module as the source of the
	// instrumentation, in the sense the upstream API gives that argument.
	instrumentationName = ModuleName

	// requestDurationName is the one instrument this module owns. Its name and
	// its unit are part of the surface a dashboard or an alert reads, so they
	// are fixed here rather than derived.
	requestDurationName = "http.server.request.duration"
	requestDurationUnit = "s"

	// layerOrder places the layer outside the layers it should time. It is a
	// preference, not a guarantee: a layer registered with a smaller order, or
	// one nothing points at, ends up outside this one, and the time it spends
	// is then not in the span.
	layerOrder = -10

	// otherMethod is where request methods outside the standard set are folded,
	// keeping the method dimension a closed set.
	otherMethod = "_OTHER"
)

// The two attributes the request histogram and the server span carry. Both are
// closed sets — the standard methods plus otherMethod, and the status codes —
// so neither grows with traffic. A route attribute is not among them: the routes
// are whatever the hosts register, so the dimension would grow with them and a
// dashboard that reads it could no longer be changed.
var (
	attrRequestMethod = attribute.Key("http.request.method")
	attrResponseCode  = attribute.Key("http.response.status_code")
)

// standardMethods is the closed set the method attribute is drawn from.
var standardMethods = map[string]struct{}{
	nethttp.MethodConnect: {},
	nethttp.MethodDelete:  {},
	nethttp.MethodGet:     {},
	nethttp.MethodHead:    {},
	nethttp.MethodOptions: {},
	nethttp.MethodPatch:   {},
	nethttp.MethodPost:    {},
	nethttp.MethodPut:     {},
	nethttp.MethodTrace:   {},
}

// methodOf folds a request method into the closed set.
func methodOf(method string) string {
	if _, ok := standardMethods[method]; ok {
		return method
	}
	return otherMethod
}

// requestDurationBounds are the bucket boundaries the request histogram counts
// into, in seconds: the ones the upstream semantic conventions give for this
// instrument.
//
// They are written out because the upstream SDK's default boundaries are meant
// for milliseconds — the first finite bucket ends at 5 — and a histogram
// recorded in seconds would put every request under five seconds into one
// bucket, leaving no percentile or latency objective to read from it. The
// boundaries are part of the surface a dashboard reads, like the name and the
// unit.
var requestDurationBounds = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}

// newRequestDuration builds the instrument the layer records on.
//
// The name, the unit, the description and the boundaries are constants of this
// package, so the error the upstream returns for an unusable instrument is a
// programming fault rather than anything a host can act on; it is handed back
// rather than panicked so that it reaches the startup failure like every other
// judgement this module makes.
func newRequestDuration(mp metric.MeterProvider) (metric.Float64Histogram, error) {
	return mp.Meter(instrumentationName).Float64Histogram(
		requestDurationName,
		metric.WithUnit(requestDurationUnit),
		metric.WithDescription("Duration of an inbound HTTP request, in seconds"),
		metric.WithExplicitBucketBoundaries(requestDurationBounds...),
	)
}

// layer describes the one layer this module registers, for one endpoint.
//
// Provides names the capabilities this module actually delivers rather than a
// name invented for the layer: another layer's After or Before has to match
// what a module really hands out, or the constraint lands nowhere and only a
// warning says so.
func (p *providers) layer(endpoint string) http.Middleware {
	return http.Middleware{
		Name: ModuleName,
		Provides: []core.Token{
			(*observability.TracerProvider)(nil),
			(*observability.MeterProvider)(nil),
		},
		Order:   layerOrder,
		Handler: p.instrument(endpoint),
	}
}

// instrument builds the gin handler for one endpoint.
//
// The endpoint name is captured here because it is the only place that knows
// it: the registration surface hands out one endpoint at a time, and nothing
// downstream can recover which one a request arrived on.
func (p *providers) instrument(endpoint string) gin.HandlerFunc {
	tracer := p.tp.Tracer(instrumentationName)
	duration := p.duration
	return func(c *gin.Context) {
		method := methodOf(c.Request.Method)

		// Extraction first: the span this request continues, if the caller
		// sent one, is what decides sampling and what the trace looks like
		// from the other side.
		ctx := observability.Propagator().Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))
		ctx, span := tracer.Start(ctx, endpoint+" "+method, trace.WithSpanKind(trace.SpanKindServer))

		// The attribute keys are this module's, bound here and read by the
		// log module without it knowing what they mean. The logger itself is
		// injected upstream, by whoever built this context.
		ctx = log.WithAttrs(ctx,
			observability.TraceIDKey, span.SpanContext().TraceID().String(),
			observability.SpanIDKey, span.SpanContext().SpanID().String(),
		)
		c.Request = c.Request.WithContext(ctx)

		start := time.Now()
		defer func() {
			raised := recover()
			elapsed := time.Since(start)

			// The writer is gin's own and this layer leaves it in place, so
			// what it reports is what the caller is answered with: gin keeps
			// the status it will commit, and a second WriteHeader before the
			// commit replaces it while one after the commit is ignored.
			code := c.Writer.Status()
			if raised != nil {
				span.RecordError(fmt.Errorf("%v", raised))
				span.SetStatus(codes.Error, "handler panicked")
				// While nothing has been written, the status the caller will
				// see is the one the entry module's fallback writes, and the
				// default of that response is an internal error. The status the
				// writer holds is no answer yet — it is whatever the handler
				// had set or the 200 the writer starts from — and reporting it
				// would put every panicking request into the 200 bucket, which
				// is the one place an operator most needs it not to be. A module
				// that delivered an ErrorResponder can write another status, and
				// this layer cannot see that decision — the module's
				// documentation states it as one of the things it does not
				// guarantee.
				if !c.Writer.Written() {
					code = nethttp.StatusInternalServerError
				}
			}
			code = answeredWith(code)
			outcome := []attribute.KeyValue{
				attrRequestMethod.String(method),
				attrResponseCode.Int(code),
			}
			duration.Record(ctx, elapsed.Seconds(), metric.WithAttributes(outcome...))

			// The span carries the same outcome the histogram does, and a
			// server error marks it failed whether or not the handler
			// panicked: a 500 written on purpose is as much this endpoint's
			// failure as one written by the fallback, and a span left
			// unmarked files it with the successes wherever traces are
			// filtered by status. A client error is the caller's, and the
			// status stays unset for it, which is the upstream convention
			// for a server span.
			span.SetAttributes(outcome...)
			if raised == nil && code >= nethttp.StatusInternalServerError {
				span.SetStatus(codes.Error, "")
			}
			span.End()

			// The panic goes on to the entry module's own fallback, which
			// sits outside the engine this layer runs in. Swallowing it here
			// would take over a promise that is not this module's, and
			// writing a response is not this layer's job either.
			if raised != nil {
				panic(raised)
			}
		}()

		c.Next()
	}
}

// answeredWith is the final status a caller is answered with, given the one the
// writer holds.
//
// An informational code the handler set — 103 Early Hints, say — is not a final
// one: net/http sends it ahead of the response and answers with the status that
// follows. The writer holds the code until the header is committed and then hands
// it on as an interim response, so a handler that goes on to write a body is
// answered with the 200 net/http supplies while the writer still reports the
// informational code. Recording that would put a status that ends no request into
// a closed set it is not part of. A status the handler writes before the commit
// replaces the code in the writer, and is the one reported. 101 Switching
// Protocols is the exception the server makes too: it is the last thing written
// on the connection before it changes hands, so it is the final code.
func answeredWith(status int) int {
	if status >= 100 && status < 200 && status != nethttp.StatusSwitchingProtocols {
		return nethttp.StatusOK
	}
	return status
}
