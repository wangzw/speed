package otel

import (
	"bufio"
	"fmt"
	"net"
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
			ctx, span := tracer.Start(ctx, endpoint+" "+methodOf(r.Method), trace.WithSpanKind(trace.SpanKindServer))

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

				code := rec.status
				if raised != nil {
					span.RecordError(fmt.Errorf("%v", raised))
					span.SetStatus(codes.Error, "handler panicked")
					// Nothing was written, so the status the caller sees is the
					// one the entry module's recovery layer writes, and that
					// layer's default is an internal error. Reporting the
					// recorder's untouched default instead would put every
					// panicking request into the 200 bucket, which is the one
					// place an operator most needs it not to be. A module that
					// delivered a PanicResponder can write something else, and
					// this layer cannot see that decision — the module's
					// documentation states it as one of the things it does not
					// guarantee.
					if !rec.wroteHeader {
						code = nethttp.StatusInternalServerError
					}
				}
				duration.Record(ctx, elapsed.Seconds(), metric.WithAttributes(
					attrRequestMethod.String(methodOf(r.Method)),
					attrResponseCode.Int(code),
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

// recorder remembers what the handler answered and keeps the writer's optional
// interfaces reachable.
//
// The status it keeps is the first code the handler wrote, not the last: a
// second WriteHeader is ignored by the server, which answers with the first, so
// a recorder that took the last one would report a status no caller ever saw. A
// body written without a header is the implicit 200 the status starts from.
//
// The two optional interfaces net/http has a name for — flushing and hijacking
// — are forwarded explicitly rather than left to Unwrap. Unwrap is what
// http.ResponseController follows, and a handler using it is served either way;
// a handler that type-asserts the writer is not, because the assertion is made
// against this type and never reaches the writer underneath. It would fail, the
// handler's streaming branch would be skipped, and the endpoint would degrade
// with nothing said. Forwarding makes the assertion succeed, and a writer
// underneath that cannot do the thing is the same no-op it was before this
// layer existed.
type recorder struct {
	nethttp.ResponseWriter
	status      int
	wroteHeader bool
}

// WriteHeader records the first code and passes it on.
func (r *recorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.wroteHeader = true
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

// Write passes the body on, counting it as the implicit 200 the status starts
// from.
func (r *recorder) Write(body []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(body)
}

// Flush forwards to the writer underneath when it can flush.
func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(nethttp.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards to the writer underneath when it can be taken over.
func (r *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(nethttp.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, nethttp.ErrNotSupported
}

// Unwrap exposes the writer this one wraps, which is what http.ResponseController
// follows for the optional interfaces beyond the two above.
func (r *recorder) Unwrap() nethttp.ResponseWriter { return r.ResponseWriter }
