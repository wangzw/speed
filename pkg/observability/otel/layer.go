package otel

import (
	"fmt"
	nethttp "net/http"
	"time"

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

// The two attributes the request histogram carries. Both are closed sets — the
// standard methods plus otherMethod, and the status codes — so neither grows
// with traffic. A route attribute is not among them: this layer sits outside
// the routing engine and the request has not been dispatched yet, so the only
// way to fill it would be to invent a source.
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

// newRequestDuration builds the instrument the layer records on.
//
// The name, the unit and the description are constants of this package, so the
// error the upstream returns for an unusable instrument is a programming fault
// rather than anything a host can act on; it is handed back rather than
// panicked so that it reaches the startup failure like every other judgement
// this module makes.
func newRequestDuration(mp metric.MeterProvider) (metric.Float64Histogram, error) {
	return mp.Meter(instrumentationName).Float64Histogram(
		requestDurationName,
		metric.WithUnit(requestDurationUnit),
		metric.WithDescription("Duration of an inbound HTTP request, in seconds"),
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
		Order: layerOrder,
		Wrap:  p.instrument(endpoint),
	}
}

// instrument builds the wrapping function for one endpoint.
//
// The endpoint name is captured here because it is the only place that knows
// it: the registration surface hands out one endpoint at a time, and nothing
// downstream can recover which one a request arrived on.
func (p *providers) instrument(endpoint string) func(nethttp.Handler) nethttp.Handler {
	tracer := p.tp.Tracer(instrumentationName)
	duration := p.duration
	return func(next nethttp.Handler) nethttp.Handler {
		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			// Extraction first: the span this request continues, if the caller
			// sent one, is what decides sampling and what the trace looks like
			// from the other side.
			ctx := observability.Propagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := tracer.Start(ctx, endpoint+" "+r.Method, trace.WithSpanKind(trace.SpanKindServer))

			// The attribute keys are this module's, bound here and read by the
			// log module without it knowing what they mean. The logger itself is
			// injected upstream, by whoever built this context.
			ctx = log.WithAttrs(ctx,
				observability.TraceIDKey, span.SpanContext().TraceID().String(),
				observability.SpanIDKey, span.SpanContext().SpanID().String(),
			)

			rec := &recorder{ResponseWriter: w, status: nethttp.StatusOK}
			start := time.Now()
			defer func() {
				raised := recover()
				elapsed := time.Since(start)

				if raised != nil {
					span.RecordError(fmt.Errorf("%v", raised))
					span.SetStatus(codes.Error, "handler panicked")
				}
				duration.Record(ctx, elapsed.Seconds(), metric.WithAttributes(
					attrRequestMethod.String(methodOf(r.Method)),
					attrResponseCode.Int(rec.status),
				))
				span.End()

				// The panic goes on to the entry module's own recovery layer,
				// which sits outside this one. Swallowing it here would take
				// over a promise that is not this module's, and writing a
				// response is not this layer's job either.
				if raised != nil {
					panic(raised)
				}
			}()

			next.ServeHTTP(rec, r.WithContext(ctx))
		})
	}
}

// recorder remembers the status code and exposes the writer underneath.
//
// The status is what the histogram attribute needs; the Unwrap is what keeps
// the optional interfaces a handler may assert — flushing, hijacking — reachable
// through the wrapper. Without it, a handler that type-asserts the writer fails
// on every instrumented endpoint, silently.
type recorder struct {
	nethttp.ResponseWriter
	status int
}

// WriteHeader records the code and passes it on.
func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the writer this one wraps, which is how the optional
// interfaces are reached through it.
func (r *recorder) Unwrap() nethttp.ResponseWriter { return r.ResponseWriter }
