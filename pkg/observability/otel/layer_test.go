package otel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

// recordingSpanExporter keeps what the layer ended.
type recordingSpanExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (r *recordingSpanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans = append(r.spans, spans...)
	return nil
}

func (r *recordingSpanExporter) Shutdown(context.Context) error { return nil }

func (r *recordingSpanExporter) recorded() []sdktrace.ReadOnlySpan {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sdktrace.ReadOnlySpan(nil), r.spans...)
}

// layerUnderTest builds the piece of a product the request layer needs. The
// export is forced through a synchronous processor so that a case reads what
// the layer produced without waiting on a batch.
func layerUnderTest(t *testing.T) (*providers, *recordingSpanExporter, *sdkmetric.ManualReader) {
	t.Helper()
	rec := &recordingSpanExporter{}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(rec)),
	)
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return &providers{cfg: prototype(), tp: tp, mp: mp}, rec, reader
}

// requestDurationName is the instrument a dashboard reads, written as the literal
// it reads rather than taken from anything the layer is built from.
const requestDurationName = "http.server.request.duration"

// The literals the tests look attributes up by.
const otherMethod = "_OTHER"

var attrRoute = attribute.Key("http.route")

// handlerDefectType is the error.type a span carries for a request the entry
// module's recovery answered. It is the name of the type that layer files in the
// request's error list — pkg/http's own, unexported — written here as a trace
// backend reads it rather than taken from anything this module is built from.
const handlerDefectType = "github.com/vislake/speed/pkg/http.handlerDefect"

// durationHistogram collects the one histogram the layer records.
func durationHistogram(t *testing.T, reader *sdkmetric.ManualReader) metricdata.Histogram[float64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != requestDurationName {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s carries %T, want a float64 histogram", requestDurationName, m.Data)
			}
			return hist
		}
	}
	t.Fatalf("no %s was recorded", requestDurationName)
	return metricdata.Histogram[float64]{}
}

// durationPoint returns the histogram's data point, for a case that sent
// requests which all carry the same attributes.
func durationPoint(t *testing.T, reader *sdkmetric.ManualReader) metricdata.HistogramDataPoint[float64] {
	t.Helper()
	hist := durationHistogram(t, reader)
	if len(hist.DataPoints) != 1 {
		t.Fatalf("%s has %d data points, want 1", requestDurationName, len(hist.DataPoints))
	}
	return hist.DataPoints[0]
}

// durationPoints returns the histogram's data points keyed by the method and the
// status code they carry, written "GET 404".
func durationPoints(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.HistogramDataPoint[float64] {
	t.Helper()
	points := map[string]metricdata.HistogramDataPoint[float64]{}
	for _, point := range durationHistogram(t, reader).DataPoints {
		key := attributeOf(t, point.Attributes, "http.request.method") + " " +
			attributeOf(t, point.Attributes, "http.response.status_code")
		points[key] = point
	}
	return points
}

func attributeOf(t *testing.T, attrs attribute.Set, key string) string {
	t.Helper()
	v, ok := attrs.Value(attribute.Key(key))
	if !ok {
		t.Fatalf("no %s attribute among %v", key, attrs.ToSlice())
	}
	return v.String()
}

// assertNoRequestIdentity holds a histogram's attributes to the ones whose
// values are bounded by what the host registers or by the protocol. The request's
// path, its full URL, its client and its user agent take a value per caller, so a
// series carrying any of them grows with traffic.
func assertNoRequestIdentity(t *testing.T, what string, attrs attribute.Set) {
	t.Helper()
	for _, key := range []attribute.Key{"url.path", "url.full", "url.query", "client.address", "user_agent.original"} {
		if _, ok := attrs.Value(key); ok {
			t.Errorf("%s carries %s, whose values grow with traffic: %v", what, key, attrs.ToSlice())
		}
	}
}

// assertNoRequestDuration holds that the layer recorded no request duration.
// That is the instrumentation's own outcome for a chain that did not return
// normally: its metrics are recorded after the call that never returns, so a
// request the caller abandons leaves none, which is deliberate and not a
// duration of zero.
func assertNoRequestDuration(t *testing.T, reader *sdkmetric.ManualReader) {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == requestDurationName {
				t.Errorf("%s was recorded for a chain that did not return normally", requestDurationName)
			}
		}
	}
}

// logRecord returns the last record the logger wrote whose msg contains substr,
// or nil when it wrote none. The entry module's records and the layer's own sit
// in the same log, so the search is by the message rather than by position.
func logRecord(logs *syncBuffer, substr string) map[string]any {
	var found map[string]any
	for line := range strings.SplitSeq(logs.String(), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil {
			continue
		}
		if msg, _ := record["msg"].(string); strings.Contains(msg, substr) {
			found = record
		}
	}
	return found
}

// routes registers one handler per route, on the endpoint the layer is on.
type routes map[[2]string]gin.HandlerFunc

func (r routes) register(ep speedhttp.Endpoint) {
	for route, handler := range r {
		ep.Route(route[0], route[1], handler)
	}
}

// TestTheLayerRecordsTheSpanAndTheRequestDuration covers the ordinary path: one
// span named for the method and the route the request matched, carrying the
// route and the endpoint, and one data point carrying the method, the status
// code the handler actually wrote and the route template. The route is
// parameterised, so the span name and the data point hold the template rather
// than the path the request asked for.
func TestTheLayerRecordsTheSpanAndTheRequestDuration(t *testing.T) {
	p, rec, reader := layerUnderTest(t)
	serve(t, p, routes{
		{nethttp.MethodGet, "/orders/:id"}: func(c *gin.Context) { c.Status(nethttp.StatusTeapot) },
	}.register, func(h *entryHost) {
		if got := h.status(t, nethttp.MethodGet, "/orders/7"); got != nethttp.StatusTeapot {
			t.Fatalf("the client was answered %d, want the 418 the handler wrote", got)
		}
	})

	spans := rec.recorded()
	if len(spans) != 1 {
		t.Fatalf("the layer ended %d spans, want 1", len(spans))
	}
	if got := spans[0].Name(); got != "GET /orders/:id" {
		t.Errorf("the span is named %q, want the method and the route template, GET /orders/:id", got)
	}
	if got := spans[0].SpanKind(); got != trace.SpanKindServer {
		t.Errorf("the span is a %v, want a server span", got)
	}
	attrs := attribute.NewSet(spans[0].Attributes()...)
	if got := attributeOf(t, attrs, "http.route"); got != "/orders/:id" {
		t.Errorf("the span's route attribute is %q, want the template /orders/:id", got)
	}
	if got := attributeOf(t, attrs, "speed.endpoint.name"); got != entryName {
		t.Errorf("the span's endpoint attribute is %q, want the endpoint the request arrived on, %s", got, entryName)
	}

	point := durationPoint(t, reader)
	if got := attributeOf(t, point.Attributes, "http.request.method"); got != nethttp.MethodGet {
		t.Errorf("the method attribute is %q, want GET", got)
	}
	if got := attributeOf(t, point.Attributes, "http.response.status_code"); got != "418" {
		t.Errorf("the status attribute is %q, want the code the handler wrote, 418", got)
	}
	if got := attributeOf(t, point.Attributes, "http.route"); got != "/orders/:id" {
		t.Errorf("the data point's route attribute is %q, want the template /orders/:id", got)
	}
	assertNoRequestIdentity(t, "the data point", point.Attributes)
	if point.Count != 1 {
		t.Errorf("the data point counts %d requests, want 1", point.Count)
	}
}

// TestTheRequestDurationBucketsAreInSeconds pins the boundaries the histogram
// counts into. The values are recorded in seconds, so boundaries meant for
// milliseconds would put every request under five seconds into one bucket and
// leave no percentile to read. Two requests an order of magnitude apart have to
// land in different buckets, and the boundaries are the ones the design names.
func TestTheRequestDurationBucketsAreInSeconds(t *testing.T) {
	p, _, reader := layerUnderTest(t)
	serve(t, p, routes{
		{nethttp.MethodGet, "/fast"}: func(*gin.Context) {},
		{nethttp.MethodGet, "/slow"}: func(*gin.Context) { time.Sleep(60 * time.Millisecond) },
	}.register, func(h *entryHost) {
		for _, path := range []string{"/fast", "/slow"} {
			h.status(t, nethttp.MethodGet, path)
		}
	})

	// The two routes are two data points of one instrument, so the buckets they
	// fill are read across both.
	want := []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}
	filled := make([]uint64, len(want)+1)
	for _, point := range durationHistogram(t, reader).DataPoints {
		if !slices.Equal(point.Bounds, want) {
			t.Errorf("the histogram counts into %v, want the boundaries in seconds %v", point.Bounds, want)
		}
		for i, count := range point.BucketCounts {
			filled[i] += count
		}
	}
	occupied := 0
	for _, count := range filled {
		if count > 0 {
			occupied++
		}
	}
	if occupied != 2 {
		t.Errorf("a request of microseconds and one of 60ms fill %d bucket(s) of %v, want 2",
			occupied, filled)
	}
}

// TestTheRequestDurationInstrumentKeepsItsNameUnitAndSeconds pins the rest of
// the surface a dashboard reads: the instrument's name, its unit, and the unit
// the value is really in. The name and the unit are looked for as the literals a
// dashboard writes, not through the constants the layer is built from, because a
// constant changed along with its uses would still agree with itself. A request
// of 30ms is recorded as 0.03, and the same request counted in milliseconds
// would be 30 under a unit that still says seconds.
func TestTheRequestDurationInstrumentKeepsItsNameUnitAndSeconds(t *testing.T) {
	p, _, reader := layerUnderTest(t)
	serve(t, p, routes{
		{nethttp.MethodGet, "/slow"}: func(c *gin.Context) {
			time.Sleep(30 * time.Millisecond)
			c.Status(nethttp.StatusOK)
		},
	}.register, func(h *entryHost) {
		h.status(t, nethttp.MethodGet, "/slow")
	})

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	var found *metricdata.Metrics
	for _, sm := range rm.ScopeMetrics {
		for i := range sm.Metrics {
			if sm.Metrics[i].Name == "http.server.request.duration" {
				found = &sm.Metrics[i]
			}
		}
	}
	if found == nil {
		t.Fatalf("no instrument is named http.server.request.duration, so a dashboard reading it finds nothing")
	}
	if found.Unit != "s" {
		t.Errorf("the unit is %q, want s", found.Unit)
	}
	hist, ok := found.Data.(metricdata.Histogram[float64])
	if !ok || len(hist.DataPoints) != 1 {
		t.Fatalf("the instrument carries %T with %d data points, want one float64 histogram point", found.Data, len(hist.DataPoints))
	}
	if sum := hist.DataPoints[0].Sum; sum < 0.03 || sum > 5 {
		t.Errorf("a request of 30ms was recorded as %v, want between 0.03 and 5: the value is in seconds", sum)
	}
}

// TestTheLayerRecordsTheStatusEvenWhenTheHandlerNeverWritesOne pins the default
// the response starts from: a handler that only writes a body, or writes nothing
// at all, has answered 200, and the attribute has to say so rather than report a
// zero.
func TestTheLayerRecordsTheStatusEvenWhenTheHandlerNeverWritesOne(t *testing.T) {
	for name, handler := range map[string]gin.HandlerFunc{
		"a body and no status":      func(c *gin.Context) { _, _ = c.Writer.WriteString("body") },
		"neither a body nor status": func(*gin.Context) {},
	} {
		t.Run(name, func(t *testing.T) {
			p, _, reader := layerUnderTest(t)
			serve(t, p, routes{{nethttp.MethodPost, "/x"}: handler}.register, func(h *entryHost) {
				h.status(t, nethttp.MethodPost, "/x")
			})

			point := durationPoint(t, reader)
			if got := attributeOf(t, point.Attributes, "http.response.status_code"); got != "200" {
				t.Errorf("the status attribute is %q, want 200", got)
			}
		})
	}
}

// TestTheLayerFoldsMethodsOutsideTheStandardSet pins the folding that keeps the
// method dimension closed. Without it a client could grow the dimension one
// verb at a time, and the resulting series would be unbounded in a signal that
// is meant to be cheap.
func TestTheLayerFoldsMethodsOutsideTheStandardSet(t *testing.T) {
	p, _, reader := layerUnderTest(t)
	serve(t, p, routes{{"PURGE", "/x"}: func(*gin.Context) {}}.register, func(h *entryHost) {
		h.status(t, "PURGE", "/x")
	})

	point := durationPoint(t, reader)
	if got := attributeOf(t, point.Attributes, "http.request.method"); got != otherMethod {
		t.Errorf("a non-standard method is recorded as %q, want %q", got, otherMethod)
	}
}

// TestTheLayerContinuesTheCallersTrace is the inbound half of propagation. It
// is the half that has no call site to forget: a server that does not extract
// breaks the trace for every caller, and the only way to notice is a trace that
// stops at the hop.
func TestTheLayerContinuesTheCallersTrace(t *testing.T) {
	p, rec, _ := layerUnderTest(t)
	serve(t, p, routes{{nethttp.MethodGet, "/x"}: func(*gin.Context) {}}.register, func(h *entryHost) {
		const incoming = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		h.status(t, nethttp.MethodGet, "/x", "traceparent", incoming)
	})

	spans := rec.recorded()
	if len(spans) != 1 {
		t.Fatalf("the layer ended %d spans, want 1", len(spans))
	}
	if got := spans[0].SpanContext().TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("the span's trace id is %s, want the caller's: the inbound trace context was not extracted", got)
	}
}

// TestTheLayerBindsTheCorrelationFieldsForDownstreamLogs pins the field names
// and the fact that they are bound rather than injected. A record written
// downstream has to carry the trace it belongs to, or a log and a span cannot
// be put together — and the names are this module's contract with whoever
// reads them. The logger is the one the entry module injects; the layer adds
// the fields to the request context the handler reads it from.
func TestTheLayerBindsTheCorrelationFieldsForDownstreamLogs(t *testing.T) {
	p, rec, _ := layerUnderTest(t)
	var logs *syncBuffer
	serve(t, p, routes{
		{nethttp.MethodGet, "/x"}: func(c *gin.Context) { log.FromContext(c.Request.Context()).Info("downstream") },
	}.register, func(h *entryHost) {
		h.status(t, nethttp.MethodGet, "/x")
		logs = h.logs
	})

	spans := rec.recorded()
	if len(spans) != 1 {
		t.Fatalf("the layer ended %d spans, want 1", len(spans))
	}
	var record map[string]any
	for line := range strings.SplitSeq(logs.String(), "\n") {
		var candidate map[string]any
		if json.Unmarshal([]byte(line), &candidate) == nil && candidate["msg"] == "downstream" {
			record = candidate
		}
	}
	if record == nil {
		t.Fatalf("the handler's record is not in the log, so nothing here was checked:\n%s", logs.String())
	}
	if got, want := record[observability.TraceIDKey], spans[0].SpanContext().TraceID().String(); got != want {
		t.Errorf("a record written downstream carries %s=%v, want the trace of the span %s",
			observability.TraceIDKey, got, want)
	}
	if got, want := record[observability.SpanIDKey], spans[0].SpanContext().SpanID().String(); got != want {
		t.Errorf("a record written downstream carries %s=%v, want the span %s",
			observability.SpanIDKey, got, want)
	}
}

// TestTheLayerRecordsAPanicAndLetsItThrough pins what a handler that panics
// leaves behind. The entry module's own recovery runs inside the observing
// layers, so it catches the panic and answers the internal error before the
// instrumentation returns: the observing layers see a request that returned,
// whose response is the recovery's 500, and the span and the histogram are built
// from exactly that. The panic is not recorded on the span as an exception
// event, because it never passed the span's own end; where its stack survives is
// the recovery's log record, which a sibling case pins.
func TestTheLayerRecordsAPanicAndLetsItThrough(t *testing.T) {
	p, rec, reader := layerUnderTest(t)
	var answered *nethttp.Response
	serve(t, p, routes{
		{nethttp.MethodGet, "/boom/:id"}: func(*gin.Context) { raiseFromHere() },
	}.register, func(h *entryHost) {
		resp, err := h.do(t, nethttp.MethodGet, "/boom/3")
		if err != nil {
			t.Fatalf("the request got no response: %v", err)
		}
		_ = resp.Body.Close()
		answered = resp
	})

	if answered.StatusCode != nethttp.StatusInternalServerError {
		t.Errorf("the client was answered %d, want the recovery's 500", answered.StatusCode)
	}
	if got := answered.Header.Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("the response is a %q, want the recovery's problem response", got)
	}

	spans := rec.recorded()
	if len(spans) != 1 {
		t.Fatalf("the layer ended %d spans, want the panicking request to have ended one", len(spans))
	}
	if got := spans[0].Status().Code; got != codes.Error {
		t.Errorf("the span's status is %v, want Error: a panicking request must not be reported as a success", got)
	}
	if got := spans[0].Name(); got != "GET /boom/:id" {
		t.Errorf("the span of a panicking request is named %q, want the method and the route it matched, "+
			"GET /boom/:id", got)
	}
	attrs := attribute.NewSet(spans[0].Attributes()...)
	if got := attributeOf(t, attrs, "http.route"); got != "/boom/:id" {
		t.Errorf("the span of a panicking request carries the route %q, want the template it matched, /boom/:id",
			got)
	}
	if got := attributeOf(t, attrs, "error.type"); got != handlerDefectType {
		t.Errorf("the span's error.type is %q, want %s, the type the recovery files", got, handlerDefectType)
	}
	if got := attributeOf(t, attrs, "http.response.status_code"); got != "500" {
		t.Errorf("the span's status attribute is %q, want the 500 the recovery wrote", got)
	}
	// The recovery caught the panic inside the observing layers, so the span was
	// ended by an ordinary return and carries no exception event: what went wrong
	// is in the recovery's log record, not on the span.
	if slices.ContainsFunc(spans[0].Events(), func(e sdktrace.Event) bool { return e.Name == "exception" }) {
		t.Errorf("the span of a recovered panic carries the events %v; the panic was caught inside it and "+
			"never reached the span's end", spans[0].Events())
	}

	if _, ok := durationPoints(t, reader)["GET 500"]; !ok {
		t.Errorf("the histogram has no data point for a request the recovery answered 500")
	}
}

// TestTheLayerReportsAPanicWithTheStatusTheCallerReceives pins what the
// histogram says about a request the handler abandoned.
//
// A response that has not started has no status yet. The one the writer holds is
// the 200 it starts from, or whatever the handler had set before it gave up, so
// reporting it would put a panicking request into the success bucket — the one
// place an operator most needs it not to be. The caller is answered with the
// fallback's internal error. A response that has already started is the other
// way round: the status is on the wire and the fallback's answer cannot change
// it, so the status the caller received is the committed one. Each case holds
// the histogram to what the client was told.
func TestTheLayerReportsAPanicWithTheStatusTheCallerReceives(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler gin.HandlerFunc
		want    int
	}{
		{
			name:    "nothing written",
			handler: func(*gin.Context) { panic("handler gave up") },
			want:    nethttp.StatusInternalServerError,
		},
		{
			name: "a status set and nothing written",
			handler: func(c *gin.Context) {
				c.Status(nethttp.StatusAccepted)
				panic("handler gave up")
			},
			want: nethttp.StatusInternalServerError,
		},
		{
			name: "a status already committed",
			handler: func(c *gin.Context) {
				c.Status(nethttp.StatusCreated)
				c.Writer.WriteHeaderNow()
				panic("handler gave up")
			},
			want: nethttp.StatusCreated,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, rec, reader := layerUnderTest(t)
			serve(t, p, routes{{nethttp.MethodGet, "/boom"}: tc.handler}.register, func(h *entryHost) {
				if got := h.status(t, nethttp.MethodGet, "/boom"); got != tc.want {
					t.Fatalf("the client was answered %d, want %d, so this case does not hold the "+
						"status the caller receives", got, tc.want)
				}
			})

			// The span says the request failed whatever status went out: a status
			// below 500 on the wire does not make a panicked request a success. The
			// failure reaches the span through the error the recovery files, so the
			// error type is the one that layer names.
			spans := rec.recorded()
			if len(spans) != 1 {
				t.Fatalf("the layer ended %d spans, want 1", len(spans))
			}
			if got := spans[0].Status().Code; got != codes.Error {
				t.Errorf("the span of a panicked request that went out with %d has the status %v, want Error",
					tc.want, got)
			}
			if got := attributeOf(t, attribute.NewSet(spans[0].Attributes()...), "error.type"); got != handlerDefectType {
				t.Errorf("the span's error.type is %q, want %s, the type the recovery files", got, handlerDefectType)
			}

			point := durationPoint(t, reader)
			if got, want := attributeOf(t, point.Attributes, "http.response.status_code"), strconv.Itoa(tc.want); got != want {
				t.Errorf("a panicking request is recorded as %q, want %s, the status the caller received",
					got, want)
			}
		})
	}
}

// TestTheLayerRecordsAnAbandonedConnectionAndLetsItThrough covers the panic
// value net/http gives a handler to drop its connection with. The entry module's
// recovery recognises it and lets it past, so the client sees a connection that
// was abandoned, not an answer. The sentinel then unwinds through the observing
// layers and out to net/http, which is what closes the connection: the
// instrumentation ends the span on its way out and records no request duration,
// and this module writes no status of its own. A client that asked for its
// connection to be dropped is not this endpoint's failure, so the span is left
// unmarked — the exception event the instrumentation's span end adds as the
// value passes it is that library's own, not a status this module set.
func TestTheLayerRecordsAnAbandonedConnectionAndLetsItThrough(t *testing.T) {
	p, rec, reader := layerUnderTest(t)
	serve(t, p, routes{
		{nethttp.MethodGet, "/abandon"}: func(*gin.Context) { panic(nethttp.ErrAbortHandler) },
	}.register, func(h *entryHost) {
		resp, err := h.do(t, nethttp.MethodGet, "/abandon")
		if err == nil {
			_ = resp.Body.Close()
			t.Fatalf("the client was answered %d, want the connection abandoned", resp.StatusCode)
		}
		if !errors.Is(err, io.EOF) {
			t.Errorf("the client failed with %v, want the end of a connection the server dropped", err)
		}
	})

	spans := rec.recorded()
	if len(spans) != 1 {
		t.Fatalf("the layer ended %d spans, want the abandoned request to have ended one", len(spans))
	}
	if got := spans[0].Status().Code; got != codes.Unset {
		t.Errorf("the span's status is %v, want Unset: an abandoned connection is not a failed request", got)
	}
	assertNoRequestDuration(t, reader)
}

// TestTheLayerFoldsTheMethodInTheSpanNameToo pins that the method dimension is
// closed in both signals. The histogram folds anything outside the standard set
// precisely because it is unbounded otherwise, and a span name is indexed by a
// backend just as a dimension is; leaving the span out of that would move the
// cardinality from the signal that was guarded to the one that was not. The name
// carries the upstream convention's placeholder, HTTP, whether a route matched or
// not, while the histogram's method attribute carries _OTHER.
func TestTheLayerFoldsTheMethodInTheSpanNameToo(t *testing.T) {
	p, rec, _ := layerUnderTest(t)
	serve(t, p, routes{{"PURGE", "/x"}: func(*gin.Context) {}}.register, func(h *entryHost) {
		h.status(t, "PURGE", "/x")
		h.status(t, "PURGE", "/missing")
	})

	var names []string
	for _, span := range rec.recorded() {
		names = append(names, span.Name())
	}
	slices.Sort(names)
	if want := []string{"HTTP", "HTTP /x"}; !slices.Equal(names, want) {
		t.Errorf("the spans are named %q, want %q: an unfolded method would let a caller grow the span "+
			"name one verb at a time", names, want)
	}
}

// TestASpanOfAnUnmatchedRequestIsNamedForTheMethodAlone pins the name and the
// attributes of a span no route matched. A request that matches no route and
// one whose method the path does not allow are answered by the engine, with no
// template to name them by; the path they asked for is whatever the caller
// wrote, so naming the span for it or carrying it as the route would let a
// caller grow both one path at a time.
func TestASpanOfAnUnmatchedRequestIsNamedForTheMethodAlone(t *testing.T) {
	p, rec, _ := layerUnderTest(t)
	serve(t, p, routes{{nethttp.MethodGet, "/x"}: func(*gin.Context) {}}.register, func(h *entryHost) {
		if got := h.status(t, nethttp.MethodGet, "/missing"); got != nethttp.StatusNotFound {
			t.Fatalf("a request matching no route was answered %d, want 404", got)
		}
		if got := h.status(t, nethttp.MethodPost, "/x"); got != nethttp.StatusMethodNotAllowed {
			t.Fatalf("a request whose method the path does not allow was answered %d, want 405", got)
		}
	})

	spans := rec.recorded()
	if len(spans) != 2 {
		t.Fatalf("the layer ended %d spans, want one for each of the two requests", len(spans))
	}
	var names []string
	for _, span := range spans {
		names = append(names, span.Name())
		attrs := attribute.NewSet(span.Attributes()...)
		// The upstream instrumentation writes the attribute with an empty value
		// for a request no route matched; what matters is that the path the
		// caller asked for does not stand in for a template.
		if route, ok := attrs.Value(attrRoute); ok && route.AsString() != "" {
			t.Errorf("the span %q carries the route %q, want none for a request no route matched",
				span.Name(), route.AsString())
		}
		if got, _ := attrs.Value(attrEndpointName); got.AsString() != entryName {
			t.Errorf("the span %q carries the endpoint %q, want %s", span.Name(), got.AsString(), entryName)
		}
	}
	slices.Sort(names)
	if want := []string{nethttp.MethodGet, nethttp.MethodPost}; !slices.Equal(names, want) {
		t.Errorf("the spans are named %q, want the methods alone, %q", names, want)
	}
}

// TestASpanOfAMatchedRouteKeepsItsRouteWhateverTheHandlerAnswers pins that the
// route goes by the match, not by the status. A handler that answers 404 for a
// resource it does not hold, or 405 of its own accord, still served a
// registered template, and its span is named and attributed for it just as a
// 200 would be.
func TestASpanOfAMatchedRouteKeepsItsRouteWhateverTheHandlerAnswers(t *testing.T) {
	for _, code := range []int{nethttp.StatusNotFound, nethttp.StatusMethodNotAllowed} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			p, rec, _ := layerUnderTest(t)
			serve(t, p, routes{{nethttp.MethodGet, "/orders/:id"}: func(c *gin.Context) { c.Status(code) }}.register,
				func(h *entryHost) {
					if got := h.status(t, nethttp.MethodGet, "/orders/7"); got != code {
						t.Fatalf("the route's handler was answered %d, want %d", got, code)
					}
				})

			spans := rec.recorded()
			if len(spans) != 1 {
				t.Fatalf("the layer ended %d spans, want 1", len(spans))
			}
			if got := spans[0].Name(); got != "GET /orders/:id" {
				t.Errorf("a %d the route's handler wrote ended a span named %q, want GET /orders/:id", code, got)
			}
			attrs := attribute.NewSet(spans[0].Attributes()...)
			if got, _ := attrs.Value(attrRoute); got.AsString() != "/orders/:id" {
				t.Errorf("a %d the route's handler wrote left the route attribute %q, want /orders/:id",
					code, got.AsString())
			}
		})
	}
}

// TestTwoPathsOfOneRouteShareTheSpanName pins that the name is drawn from the
// template, not from the path a request asked for. Two requests to one
// parameterised route are one operation, and a name carrying the path would
// grow with every identifier a caller sends.
func TestTwoPathsOfOneRouteShareTheSpanName(t *testing.T) {
	p, rec, _ := layerUnderTest(t)
	serve(t, p, routes{{nethttp.MethodGet, "/orders/:id"}: func(*gin.Context) {}}.register, func(h *entryHost) {
		h.status(t, nethttp.MethodGet, "/orders/7")
		h.status(t, nethttp.MethodGet, "/orders/8")
	})

	spans := rec.recorded()
	if len(spans) != 2 {
		t.Fatalf("the layer ended %d spans, want one for each of the two requests", len(spans))
	}
	for _, span := range spans {
		if got := span.Name(); got != "GET /orders/:id" {
			t.Errorf("a request to the route ended a span named %q, want GET /orders/:id", got)
		}
	}
}

// TestTheStatusAttributeIsTheOneTheCallerReceives pins which WriteHeader counts.
// The writer keeps the status it will commit: a second WriteHeader before the
// commit replaces it, and one after the commit is ignored by the server, which
// answers with the first. Either way the attribute has to be the code the
// client was answered with, not the first or the last one the handler wrote.
func TestTheStatusAttributeIsTheOneTheCallerReceives(t *testing.T) {
	for name, handler := range map[string]gin.HandlerFunc{
		"a second status before the commit replaces the first": func(c *gin.Context) {
			c.Status(nethttp.StatusOK)
			c.Status(nethttp.StatusInternalServerError)
		},
		"a second status after the commit is ignored": func(c *gin.Context) {
			c.Status(nethttp.StatusOK)
			c.Writer.WriteHeaderNow()
			c.Status(nethttp.StatusInternalServerError)
		},
	} {
		t.Run(name, func(t *testing.T) {
			p, _, reader := layerUnderTest(t)
			var received int
			serve(t, p, routes{{nethttp.MethodGet, "/x"}: handler}.register, func(h *entryHost) {
				received = h.status(t, nethttp.MethodGet, "/x")
			})

			point := durationPoint(t, reader)
			if got, want := attributeOf(t, point.Attributes, "http.response.status_code"), strconv.Itoa(received); got != want {
				t.Errorf("the status attribute is %q, want %s, the code the caller received", got, want)
			}
		})
	}
}

// TestTheLayerKeepsASwitchingProtocolsStatus pins the informational code that is
// a final one. 101 is the last thing written on a connection before it changes
// hands, so a handler that answers an upgrade request with it has been answered
// 101 and the attribute says so.
func TestTheLayerKeepsASwitchingProtocolsStatus(t *testing.T) {
	p, _, reader := layerUnderTest(t)
	serve(t, p, routes{{nethttp.MethodGet, "/upgrade"}: func(c *gin.Context) {
		c.Writer.WriteHeader(nethttp.StatusSwitchingProtocols)
		conn, rw, err := c.Writer.Hijack()
		if err != nil {
			return
		}
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: test\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		_ = conn.Close()
	}}.register, func(h *entryHost) {
		resp, err := h.do(t, nethttp.MethodGet, "/upgrade", "Connection", "Upgrade", "Upgrade", "test")
		if err != nil {
			t.Fatalf("the upgrade request got no response: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != nethttp.StatusSwitchingProtocols {
			t.Fatalf("the client was answered %d, want 101, so this case does not hold the upgrade", resp.StatusCode)
		}
	})

	point := durationPoint(t, reader)
	if got := attributeOf(t, point.Attributes, "http.response.status_code"); got != "101" {
		t.Errorf("the status attribute is %q, want 101: it is the final code of an upgrade", got)
	}
}

// TestTheServerSpanCarriesTheOutcome pins what a span says about the request it
// covers: the same method and status attributes the histogram carries, and an
// error status when the answer was a server error. A handler that answers 500
// without panicking failed just as much as one that panicked, and a span left
// unmarked would file it with the successes at every backend that filters
// traces by status. A client error is not this endpoint's failure and leaves
// the status unset.
func TestTheServerSpanCarriesTheOutcome(t *testing.T) {
	for _, tc := range []struct {
		code      int
		wantError bool
	}{
		{nethttp.StatusOK, false},
		{nethttp.StatusNotFound, false},
		{nethttp.StatusInternalServerError, true},
		{nethttp.StatusServiceUnavailable, true},
	} {
		t.Run(strconv.Itoa(tc.code), func(t *testing.T) {
			p, rec, _ := layerUnderTest(t)
			serve(t, p, routes{{"PURGE", "/x"}: func(c *gin.Context) { c.Status(tc.code) }}.register,
				func(h *entryHost) {
					h.status(t, "PURGE", "/x")
				})

			spans := rec.recorded()
			if len(spans) != 1 {
				t.Fatalf("the layer ended %d spans, want 1", len(spans))
			}
			attrs := attribute.NewSet(spans[0].Attributes()...)
			if got := attributeOf(t, attrs, "http.request.method"); got != otherMethod {
				t.Errorf("the span's method attribute is %q, want the folded %q", got, otherMethod)
			}
			if got := attributeOf(t, attrs, "http.response.status_code"); got != strconv.Itoa(tc.code) {
				t.Errorf("the span's status attribute is %q, want %d", got, tc.code)
			}
			if got := spans[0].Status().Code == codes.Error; got != tc.wantError {
				t.Errorf("a %d answer left the span with status %v, want error=%v",
					tc.code, spans[0].Status().Code, tc.wantError)
			}
		})
	}
}

// TestTheLayerSeesARequestThatMatchesNoRouteAndOneTheMethodDoesNotAllow pins that
// the layer covers every request the endpoint takes, not only the ones a route
// serves, and that the status it reads is the one the engine's own answer wrote.
// The 404 and the 405 are written after the layers have been entered, so a layer
// that reported a status of its own making, or only the one a route's handler
// set, would miss both.
func TestTheLayerSeesARequestThatMatchesNoRouteAndOneTheMethodDoesNotAllow(t *testing.T) {
	p, rec, reader := layerUnderTest(t)
	serve(t, p, routes{{nethttp.MethodGet, "/x"}: func(*gin.Context) {}}.register, func(h *entryHost) {
		if got := h.status(t, nethttp.MethodGet, "/missing"); got != nethttp.StatusNotFound {
			t.Fatalf("a request matching no route was answered %d, want 404", got)
		}
		if got := h.status(t, nethttp.MethodPost, "/x"); got != nethttp.StatusMethodNotAllowed {
			t.Fatalf("a request whose method the path does not allow was answered %d, want 405", got)
		}
	})

	points := durationPoints(t, reader)
	for _, want := range []string{"GET 404", "POST 405"} {
		point, ok := points[want]
		if !ok {
			t.Errorf("the histogram has no data point for %q, it has %d: the layer did not see the request",
				want, len(points))
			continue
		}
		if point.Count != 1 {
			t.Errorf("the data point for %q counts %d requests, want 1", want, point.Count)
		}
		assertNoRequestIdentity(t, "the data point for "+want, point.Attributes)
	}

	spans := rec.recorded()
	if len(spans) != 2 {
		t.Fatalf("the layer ended %d spans, want one for each of the two requests", len(spans))
	}
	for _, span := range spans {
		if span.Status().Code == codes.Error {
			t.Errorf("the span %q is marked failed, want a client error left unset", span.Name())
		}
	}
}

// TestTheLayerLeavesTheResponseWriterInPlace pins that the layer does not
// replace c.Writer. A layer outside this one and a handler inside it see the same
// writer, and the optional interfaces the writer carries reach the connection
// through the layer: a flush puts the first bytes on the wire while the handler
// is still running, and a hijack hands the handler the connection. A writer
// replaced by a wrapper that forwarded nothing would still satisfy the types and
// would fail the observations, and a wrapper that forwarded everything would
// still fail the identity.
func TestTheLayerLeavesTheResponseWriterInPlace(t *testing.T) {
	p, _, _ := layerUnderTest(t)

	// streamed is what a streaming handler reports once it has flushed, and the
	// gate keeps its response open until the case has read what was flushed.
	type streamed struct {
		writer   gin.ResponseWriter
		flushErr error
		gate     chan struct{}
	}
	outside := make(chan gin.ResponseWriter, 1)
	flushed := make(chan streamed, 1)
	stream := func(flush func(c *gin.Context) error) gin.HandlerFunc {
		return func(c *gin.Context) {
			gate := make(chan struct{})
			_, _ = c.Writer.WriteString("first")
			flushed <- streamed{writer: c.Writer, flushErr: flush(c), gate: gate}
			select {
			case <-gate:
			case <-c.Request.Context().Done():
			}
		}
	}
	serve(t, p, func(ep speedhttp.Endpoint) {
		ep.Use(speedhttp.Middleware{
			Name:   "test.outside",
			Stage:  speedhttp.StageObserve,
			Before: []core.Token{(*observability.TracerProvider)(nil)},
			Handler: func(c *gin.Context) {
				outside <- c.Writer
				c.Next()
			},
		})
		ep.Route(nethttp.MethodGet, "/flush", stream(func(c *gin.Context) error {
			c.Writer.Flush()
			return nil
		}))
		ep.Route(nethttp.MethodGet, "/controller", stream(func(c *gin.Context) error {
			return nethttp.NewResponseController(c.Writer).Flush()
		}))
		ep.Route(nethttp.MethodGet, "/hijack", func(c *gin.Context) {
			conn, rw, err := nethttp.NewResponseController(c.Writer).Hijack()
			if err != nil {
				c.Writer.Header().Set("X-Hijack-Error", err.Error())
				return
			}
			_, _ = rw.WriteString("HTTP/1.1 202 Accepted\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			_ = rw.Flush()
			_ = conn.Close()
		})
	}, func(h *entryHost) {
		for _, path := range []string{"/flush", "/controller"} {
			// The response comes back while the handler is still held, which is
			// only possible when the flush put its first bytes on the wire.
			resp, err := h.do(t, nethttp.MethodGet, path)
			if err != nil {
				t.Fatalf("GET %s got no response while the handler was still running: %v: what the "+
					"handler flushed did not reach the client", path, err)
			}
			first := make([]byte, len("first"))
			_, readErr := io.ReadFull(resp.Body, first)
			res := <-flushed
			close(res.gate)
			_ = resp.Body.Close()

			if res.flushErr != nil {
				t.Errorf("GET %s: the flush failed: %v", path, res.flushErr)
			}
			if readErr != nil || string(first) != "first" {
				t.Errorf("GET %s read %q and %v before the handler finished, want the flushed bytes",
					path, first, readErr)
			}
			if before := <-outside; before != res.writer {
				t.Errorf("GET %s: the layer outside sees the writer %p and the handler inside it sees %p, "+
					"want the same writer: this layer replaced it", path, before, res.writer)
			}
		}

		resp, err := h.do(t, nethttp.MethodGet, "/hijack")
		if err != nil {
			t.Fatalf("GET /hijack got no response: %v", err)
		}
		_ = resp.Body.Close()
		if msg := resp.Header.Get("X-Hijack-Error"); msg != "" {
			t.Errorf("the handler could not take the connection over: %s", msg)
		}
		if resp.StatusCode != nethttp.StatusAccepted {
			t.Errorf("GET /hijack was answered %d, want the 202 the handler wrote on the connection it took",
				resp.StatusCode)
		}
		<-outside
	})
}

// TestTheLayerStandsForExactlyTheTwoProvidersThisModuleDelivers pins what another
// layer's After or Before can point at. The layer stands for the two capabilities
// the module really delivers: a constraint naming either lands on it, and a
// capability invented for this layer alone would be one no module delivers. A
// layer missing one of them silently drops the constraints that point at it, and
// the only trace is an Info line in the startup diagnostics.
func TestTheLayerStandsForExactlyTheTwoProvidersThisModuleDelivers(t *testing.T) {
	p, _, _ := layerUnderTest(t)
	provides := p.layers("api")[0].Provides
	for _, want := range []core.Token{
		(*observability.TracerProvider)(nil),
		(*observability.MeterProvider)(nil),
	} {
		if !slices.Contains(provides, want) {
			t.Errorf("the layer stands for %v, which lacks %T", provides, want)
		}
	}
	if len(provides) != 2 {
		t.Errorf("the layer stands for %d capabilities, %v, want only the two this module delivers", len(provides), provides)
	}
}

// TestALayerStatingNoOrderRunsInsideTheInstrumentation pins the Order the
// instrumentation is registered with. It is at OrderOutermost in the observing
// stage, so a layer that leaves Order at its zero value runs inside it and the
// time it spends is in the span.
func TestALayerStatingNoOrderRunsInsideTheInstrumentation(t *testing.T) {
	p, _, _ := layerUnderTest(t)
	var inside atomic.Bool
	serve(t, p, func(ep speedhttp.Endpoint) {
		ep.Use(speedhttp.Middleware{
			Name:  "test.probe",
			Stage: speedhttp.StageObserve,
			Handler: func(c *gin.Context) {
				inside.Store(trace.SpanFromContext(c.Request.Context()).SpanContext().IsValid())
				c.Next()
			},
		})
		ep.Route(nethttp.MethodGet, "/x", func(c *gin.Context) { c.Status(nethttp.StatusOK) })
	}, func(h *entryHost) {
		h.status(t, nethttp.MethodGet, "/x")
	})

	if !inside.Load() {
		t.Error("a layer stating no Order ran outside the instrumentation layer: the time it spends is not in the span")
	}
}

// raiseFromHere is a named frame for the panic a case throws, so that the stack
// a record carries can be searched for the place the panic came from.
func raiseFromHere() { panic("handler gave up") }

// TestAPanicEndsTheChainAtTheLayerThatRaisedIt pins that a panic in a layer
// stops the chain at that layer. Each layer is a gin handler, so a panic in one
// unwinds the loop that called it: no layer registered after it and no route
// handler runs, and the handlers run on a request that is left unfinished rather
// than on one whose handler gave up. The panicking layer here is an observing
// one, which sits outside the entry module's recovery, so the panic reaches
// net/http and the client is answered with a dropped connection; a panic thrown
// by a layer the recovery does cover is answered 500 instead.
func TestAPanicEndsTheChainAtTheLayerThatRaisedIt(t *testing.T) {
	p, _, _ := layerUnderTest(t)
	var after, route atomic.Bool
	serve(t, p, func(ep speedhttp.Endpoint) {
		ep.Use(speedhttp.Middleware{
			Name:    "test.raises",
			Stage:   speedhttp.StageObserve,
			Handler: func(*gin.Context) { raiseFromHere() },
		})
		ep.Use(speedhttp.Middleware{
			Name:  "test.after",
			Stage: speedhttp.StageObserve,
			Handler: func(c *gin.Context) {
				after.Store(true)
				c.Next()
			},
		})
		ep.Route(nethttp.MethodGet, "/x", func(*gin.Context) { route.Store(true) })
	}, func(h *entryHost) {
		if resp, err := h.do(t, nethttp.MethodGet, "/x"); err == nil {
			_ = resp.Body.Close()
			t.Errorf("the client was answered %d, want no answer: a panic in an observing layer is "+
				"outside the recovery and reaches net/http", resp.StatusCode)
		}
	})

	if after.Load() || route.Load() {
		t.Errorf("a layer after the one that panicked ran: %v, and the route's handler ran: %v; "+
			"want neither", after.Load(), route.Load())
	}
}

// TestTheStackOfAPanicsOriginSurvivesInTheLog pins where the origin of a panic
// survives. The entry module's recovery takes the stack at its own frame, while
// the frames the panic came through are still on it, and writes it to a log
// record; the span carries no exception event, because the panic was caught
// inside the observing layers and never reached the span's own end. The record's
// stack names the function the handler gave up in, which is what a reader needs
// to find the line.
func TestTheStackOfAPanicsOriginSurvivesInTheLog(t *testing.T) {
	p, rec, _ := layerUnderTest(t)
	var logs *syncBuffer
	serve(t, p, routes{{nethttp.MethodGet, "/boom"}: func(*gin.Context) { raiseFromHere() }}.register,
		func(h *entryHost) {
			h.status(t, nethttp.MethodGet, "/boom")
			logs = h.logs
		})

	spans := rec.recorded()
	if len(spans) != 1 {
		t.Fatalf("the layer ended %d spans, want 1", len(spans))
	}
	if slices.ContainsFunc(spans[0].Events(), func(e sdktrace.Event) bool { return e.Name == "exception" }) {
		t.Errorf("the span of a recovered panic carries the events %v; the panic was caught inside it and "+
			"never reached the span's end", spans[0].Events())
	}

	record := logRecord(logs, "panicked")
	if record == nil {
		t.Fatalf("the recovery wrote no record for the panic:\n%s", logs.String())
	}
	if got := record["panic"]; got != "handler gave up" {
		t.Errorf("the record's panic is %v, want the panic value's text, handler gave up", got)
	}
	stack, _ := record["stack"].(string)
	if !strings.Contains(stack, "raiseFromHere") {
		t.Errorf("the record's stack is %q, want one naming raiseFromHere, the place the panic came from", stack)
	}
}

// TestAnErrorTheHandlerFilesMarksTheSpanFailed pins what the request's error list
// does to the span. A handler that files an error and answers with a status below
// 500 has still failed the request, and a span left unmarked files it with the
// successes wherever traces are filtered by status.
func TestAnErrorTheHandlerFilesMarksTheSpanFailed(t *testing.T) {
	p, rec, _ := layerUnderTest(t)
	serve(t, p, routes{{nethttp.MethodGet, "/x"}: func(c *gin.Context) {
		_ = c.Error(errors.New("the store refused"))
		c.Status(nethttp.StatusOK)
	}}.register, func(h *entryHost) {
		h.status(t, nethttp.MethodGet, "/x")
	})

	spans := rec.recorded()
	if len(spans) != 1 {
		t.Fatalf("the layer ended %d spans, want 1", len(spans))
	}
	if got := spans[0].Status().Code; got != codes.Error {
		t.Errorf("the span's status is %v, want Error for a request whose handler filed an error", got)
	}
	attrs := attribute.NewSet(spans[0].Attributes()...)
	if _, ok := attrs.Value("error.type"); !ok {
		t.Errorf("the span carries %v, want an error.type", spans[0].Attributes())
	}
}

// TestTheGuardStandsInsideTheInstrumentationAndStandsForNothing pins how the two
// layers are placed against each other. The guard has to sit inside the
// instrumentation, or the panic it stops would already have passed it, and a
// capability only the instrumentation stands for keeps the guard from being
// pointed at as a second provider of it.
func TestTheGuardStandsInsideTheInstrumentationAndStandsForNothing(t *testing.T) {
	p, _, _ := layerUnderTest(t)
	layers := p.layers("api")
	if len(layers) != 2 {
		t.Fatalf("the module registers %d layers for an endpoint, want the instrumentation and the guard", len(layers))
	}
	guard := layers[1]
	if !slices.Contains(guard.After, core.Token((*observability.TracerProvider)(nil))) {
		t.Errorf("the guard is placed after %v, want it inside the layer standing for the tracer provider", guard.After)
	}
	if len(guard.Provides) != 0 {
		t.Errorf("the guard stands for %v, want nothing: a second layer standing for the providers would "+
			"split what another layer's After or Before points at", guard.Provides)
	}
}

// TestALayerStatingNoOrderRunsInsideTheGuard pins where the guard stands
// among the layers other modules register. A layer that leaves Order at its
// zero value is inside the guard, and the guard is the layer that binds the trace and span
// identifiers into the request context: a record the layer writes carries them,
// and the span the instrumentation ends for the layer's request names the
// endpoint the guard put on it. A guard placed further in would leave the layer
// outside, and its records would have no trace to name.
//
// The layer panics on one of the two paths. That panic is its own, in an
// observing layer, so it is outside the entry module's recovery: the chain stops
// there and net/http drops the connection, and the span the instrumentation ends
// on the way out carries whatever it records for a chain that did not return.
// The record the layer wrote before it panicked still carries the trace, which
// is what places the layer inside the guard.
func TestALayerStatingNoOrderRunsInsideTheGuard(t *testing.T) {
	p, rec, _ := layerUnderTest(t)
	var logs *syncBuffer
	serve(t, p, func(ep speedhttp.Endpoint) {
		ep.Use(speedhttp.Middleware{
			Name:  "test.logs-then-raises",
			Stage: speedhttp.StageObserve,
			Handler: func(c *gin.Context) {
				log.FromContext(c.Request.Context()).Info("from the layer")
				if c.Request.URL.Path == "/boom" {
					panic("layer gave up")
				}
				c.Next()
			},
		})
		ep.Route(nethttp.MethodGet, "/boom", func(*gin.Context) {})
		ep.Route(nethttp.MethodGet, "/fine", func(*gin.Context) {})
	}, func(h *entryHost) {
		if resp, err := h.do(t, nethttp.MethodGet, "/boom"); err == nil {
			_ = resp.Body.Close()
			t.Errorf("the panicking request was answered %d, want no answer: its panic is in an "+
				"observing layer, outside the recovery", resp.StatusCode)
		}
		if got := h.status(t, nethttp.MethodGet, "/fine"); got != nethttp.StatusOK {
			t.Fatalf("the ordinary request was answered %d, want 200", got)
		}
		logs = h.logs
	})

	spans := rec.recorded()
	if len(spans) != 2 {
		t.Fatalf("the layer ended %d spans, want 2", len(spans))
	}
	for _, span := range spans {
		if got := attributeOf(t, attribute.NewSet(span.Attributes()...), "speed.endpoint.name"); got != entryName {
			t.Errorf("the span %q names the endpoint %q, want %q", span.Name(), got, entryName)
		}
	}

	var traced int
	for line := range strings.SplitSeq(logs.String(), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil || record["msg"] != "from the layer" {
			continue
		}
		if record[observability.TraceIDKey] != nil && record[observability.SpanIDKey] != nil {
			traced++
		}
	}
	if traced != 2 {
		t.Errorf("%d of the layer's 2 records carry the trace and span identifiers, want both: the "+
			"layer ran outside the guard that binds them\n%s", traced, logs.String())
	}
}

// TestAHandlerThatEndsItsGoroutineLeavesAnUnmarkedSpan pins what the span says
// about a runtime.Goexit in a handler. It is not a panic: the entry module's
// recovery sees a nil recover and the unwind keeps going past it, so the recovery
// never returns to the instrumentation, whose span end ends the span unmarked and
// records no request duration. Recovering the unwind here to mark the span would
// be the wrong trade: the recover that could see it is the one that would also
// swallow the recovery's own ErrAbortHandler, and the connection would stop being
// closed. The recovery's own record, naming a handler that stopped without
// returning, is where the request is accounted for.
func TestAHandlerThatEndsItsGoroutineLeavesAnUnmarkedSpan(t *testing.T) {
	p, rec, reader := layerUnderTest(t)
	var logs *syncBuffer
	serve(t, p, routes{{nethttp.MethodGet, "/exit"}: func(*gin.Context) { runtime.Goexit() }}.register,
		func(h *entryHost) {
			resp, err := h.do(t, nethttp.MethodGet, "/exit")
			if err == nil {
				_ = resp.Body.Close()
				t.Fatalf("the client was answered %d, want the connection dropped", resp.StatusCode)
			}
			logs = h.logs
		})

	spans := rec.recorded()
	if len(spans) != 1 {
		t.Fatalf("the layer ended %d spans, want 1", len(spans))
	}
	if got := spans[0].Status().Code; got != codes.Unset {
		t.Errorf("the span of a request whose handler called Goexit has the status %v, want Unset", got)
	}
	assertNoRequestDuration(t, reader)
	if logRecord(logs, "without a panic value") == nil {
		t.Errorf("the recovery wrote no record for the handler that stopped without returning:\n%s", logs.String())
	}
}

// panicWith panics with whatever value it is handed, which a case needs to throw
// a nil that is a value the handler was given rather than a constant.
func panicWith(v any) { panic(v) }

// nilPanicChild marks the copy of the test binary that
// TestANilPanicIsAnsweredByTheRecovery starts under GODEBUG=panicnil=1.
const nilPanicChild = "SPEED_OBSERVABILITY_NILPANIC_CHILD"

// TestANilPanicIsAnsweredByTheRecovery pins the panic(nil) that
// GODEBUG=panicnil=1 turns back into a recovered nil. Read as a request that
// returned, it would be stopped by the entry module's recovery: the handlers
// after the one that panicked would run, the client would be answered 200, the
// span would be a success and the recovery would write no record. The recovery
// has to tell the two apart, and it does: a nil recover with the call not
// returned is a handler that stopped, and it is answered as the defect it is.
//
// The panicking layer sits in the guard stage, inside the recovery. A nil panic
// from an observing layer would pass the recovery and be swallowed by the
// instrumentation's span end, which recovers a panic to record it and, finding a
// nil, records nothing and throws nothing on — the observing layers are not where
// a nil panic is answered.
//
// The setting is fixed when a process starts, so the case runs in a copy of this
// test binary started under it.
func TestANilPanicIsAnsweredByTheRecovery(t *testing.T) {
	if os.Getenv(nilPanicChild) == "" {
		child := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
		child.Env = append(os.Environ(), nilPanicChild+"=1", "GODEBUG=panicnil=1")
		if out, err := child.CombinedOutput(); err != nil {
			t.Fatalf("the case failed under GODEBUG=panicnil=1: %v\n%s", err, out)
		}
		return
	}

	p, rec, _ := layerUnderTest(t)
	var after, route atomic.Bool
	var logs *syncBuffer
	serve(t, p, func(ep speedhttp.Endpoint) {
		ep.Use(speedhttp.Middleware{
			Name:    "test.raises-nil",
			Stage:   speedhttp.StageGuard,
			Handler: func(*gin.Context) { panicWith(nil) },
		})
		ep.Use(speedhttp.Middleware{
			Name:  "test.after",
			Stage: speedhttp.StageGuard,
			Handler: func(c *gin.Context) {
				after.Store(true)
				c.Next()
			},
		})
		ep.Route(nethttp.MethodGet, "/x", func(*gin.Context) { route.Store(true) })
	}, func(h *entryHost) {
		if got := h.status(t, nethttp.MethodGet, "/x"); got != nethttp.StatusInternalServerError {
			t.Errorf("the client was answered %d, want the recovery's 500: a nil panic was read as a request that returned", got)
		}
		logs = h.logs
	})

	if after.Load() || route.Load() {
		t.Errorf("a handler after the one that panicked with nil ran: %v, and the route's handler ran: %v; want neither",
			after.Load(), route.Load())
	}
	if logRecord(logs, "without a panic value") == nil {
		t.Errorf("the recovery wrote no record for the nil panic:\n%s", logs.String())
	}
	spans := rec.recorded()
	if len(spans) != 1 {
		t.Fatalf("the layer ended %d spans, want 1", len(spans))
	}
	if got := spans[0].Status().Code; got != codes.Error {
		t.Errorf("the span of a request whose handler panicked with nil has the status %v, want Error", got)
	}
}
