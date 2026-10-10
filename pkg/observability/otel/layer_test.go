package otel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
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
	duration, err := newRequestDuration(mp)
	if err != nil {
		t.Fatalf("build the request duration instrument: %v", err)
	}
	return &providers{cfg: prototype(), tp: tp, mp: mp, duration: duration}, rec, reader
}

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

// routes registers one handler per route, on the endpoint the layer is on.
type routes map[[2]string]gin.HandlerFunc

func (r routes) register(ep speedhttp.Endpoint) {
	for route, handler := range r {
		ep.Route(route[0], route[1], handler)
	}
}

// TestTheLayerRecordsTheSpanAndTheRequestDuration covers the ordinary path: one
// span named for the endpoint and the method, one data point carrying the
// method and the status code the handler actually wrote, and nothing else. The
// route is parameterised so that an attribute carrying the matched route would
// have a value to carry.
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
	if got := spans[0].Name(); got != "api GET" {
		t.Errorf("the span is named %q, want the endpoint and the method", got)
	}
	if got := spans[0].SpanKind(); got != trace.SpanKindServer {
		t.Errorf("the span is a %v, want a server span", got)
	}

	point := durationPoint(t, reader)
	if got := attributeOf(t, point.Attributes, "http.request.method"); got != nethttp.MethodGet {
		t.Errorf("the method attribute is %q, want GET", got)
	}
	if got := attributeOf(t, point.Attributes, "http.response.status_code"); got != "418" {
		t.Errorf("the status attribute is %q, want the code the handler wrote, 418", got)
	}
	if got := point.Attributes.Len(); got != 2 {
		t.Errorf("the data point carries %d attributes, %v, want the method and the status code alone: a "+
			"route attribute would make the dimension grow with what hosts register",
			got, point.Attributes.ToSlice())
	}
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

	point := durationPoint(t, reader)
	want := []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}
	if !slices.Equal(point.Bounds, want) {
		t.Errorf("the histogram counts into %v, want the boundaries in seconds %v", point.Bounds, want)
	}
	occupied := 0
	for _, count := range point.BucketCounts {
		if count > 0 {
			occupied++
		}
	}
	if occupied != 2 {
		t.Errorf("a request of microseconds and one of 60ms fill %d bucket(s) of %v, want 2",
			occupied, point.BucketCounts)
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

// TestEveryStandardMethodKeepsItsName is the other half of the folding: the nine
// standard methods are the closed set, and each of them is recorded as itself. A
// set that lost one would fold that method into the value meant for the unknown
// ones, and the method dimension would stop telling it apart.
func TestEveryStandardMethodKeepsItsName(t *testing.T) {
	standard := []string{
		nethttp.MethodConnect, nethttp.MethodDelete, nethttp.MethodGet, nethttp.MethodHead, nethttp.MethodOptions,
		nethttp.MethodPatch, nethttp.MethodPost, nethttp.MethodPut, nethttp.MethodTrace,
	}
	for _, method := range standard {
		if got := methodOf(method); got != method {
			t.Errorf("methodOf(%q) is %q, want the method itself", method, got)
		}
	}
	if len(standardMethods) != len(standard) {
		t.Errorf("the standard set holds %d methods, want the nine of net/http", len(standardMethods))
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

// TestTheLayerRecordsAPanicAndLetsItThrough pins both halves of the recovery
// the layer does: the span says the request failed, and the panic goes on to
// the entry module's fallback, outside the engine. Swallowing it would take over
// a promise that belongs to the entry module, and what the client is answered
// with says which happened: a swallowed panic ends the handler as a request that
// returned, which is a 200, while the fallback answers an internal error.
func TestTheLayerRecordsAPanicAndLetsItThrough(t *testing.T) {
	p, rec, _ := layerUnderTest(t)
	var answered *nethttp.Response
	serve(t, p, routes{
		{nethttp.MethodGet, "/boom"}: func(*gin.Context) { panic("handler gave up") },
	}.register, func(h *entryHost) {
		resp, err := h.do(t, nethttp.MethodGet, "/boom")
		if err != nil {
			t.Fatalf("the request got no response: %v", err)
		}
		_ = resp.Body.Close()
		answered = resp
	})

	if answered.StatusCode != nethttp.StatusInternalServerError {
		t.Errorf("the client was answered %d, want the fallback's 500: the panic did not reach it",
			answered.StatusCode)
	}
	if got := answered.Header.Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("the response is a %q, want the fallback's problem response", got)
	}

	spans := rec.recorded()
	if len(spans) != 1 {
		t.Fatalf("the layer ended %d spans, want the panicking request to have ended one", len(spans))
	}
	if got := spans[0].Status().Code; got != codes.Error {
		t.Errorf("the span's status is %v, want Error: a panicking request must not be reported as a success", got)
	}
	// The panic is recorded on the span as an exception event, which is where a
	// trace backend shows what went wrong; a status alone says only that
	// something did.
	if !slices.ContainsFunc(spans[0].Events(), func(e sdktrace.Event) bool { return e.Name == "exception" }) {
		t.Errorf("the span of a panicking request carries the events %v, want an exception event: the "+
			"panic was not recorded on it", spans[0].Events())
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
			p, _, reader := layerUnderTest(t)
			serve(t, p, routes{{nethttp.MethodGet, "/boom"}: tc.handler}.register, func(h *entryHost) {
				if got := h.status(t, nethttp.MethodGet, "/boom"); got != tc.want {
					t.Fatalf("the client was answered %d, want %d, so this case does not hold the "+
						"status the caller receives", got, tc.want)
				}
			})

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
// fallback recognises it and throws it again, so the client sees a connection
// that was abandoned, not an answer. This layer sits inside the fallback and
// reads that value as it reads any panic: the span is marked and the histogram
// records the internal error, which is the documented misreport. What it must
// not do is answer for the fallback — swallow the value, which would leave the
// client a 200, or throw something else, which the fallback would answer as an
// ordinary defect with a 500.
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
	if got := spans[0].Status().Code; got != codes.Error {
		t.Errorf("the span's status is %v, want Error", got)
	}
	point := durationPoint(t, reader)
	if got := attributeOf(t, point.Attributes, "http.response.status_code"); got != "500" {
		t.Errorf("an abandoned request is recorded as %q, want the fallback's default, 500", got)
	}
}

// TestTheLayerFoldsTheMethodInTheSpanNameToo pins that the method dimension is
// closed in both signals. The histogram folds anything outside the standard set
// precisely because it is unbounded otherwise, and a span name is indexed by a
// backend just as a dimension is; leaving the span out of that would move the
// cardinality from the signal that was guarded to the one that was not.
func TestTheLayerFoldsTheMethodInTheSpanNameToo(t *testing.T) {
	p, rec, _ := layerUnderTest(t)
	serve(t, p, routes{{"PURGE", "/x"}: func(*gin.Context) {}}.register, func(h *entryHost) {
		h.status(t, "PURGE", "/x")
	})

	spans := rec.recorded()
	if len(spans) != 1 {
		t.Fatalf("the layer ended %d spans, want 1", len(spans))
	}
	if got, want := spans[0].Name(), "api "+otherMethod; got != want {
		t.Errorf("the span is named %q, want %q: an unfolded method would let a caller grow the span "+
			"name one verb at a time", got, want)
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

// TestTheLayerSkipsAnInformationalStatus pins the one kind of code a handler may
// write before its final one. An informational response — 103 Early Hints, say —
// is sent ahead of the response and does not stop the handler from writing the
// final status, so it is not what the caller was answered with. A handler that
// goes on to write a body is answered 200, and one that writes a status is
// answered with it; the writer, though, goes on reporting the informational
// code. Each case holds the attribute to the code the client received.
func TestTheLayerSkipsAnInformationalStatus(t *testing.T) {
	for name, handler := range map[string]gin.HandlerFunc{
		"a body after the hints": func(c *gin.Context) {
			c.Writer.WriteHeader(nethttp.StatusEarlyHints)
			_, _ = c.Writer.WriteString("body")
		},
		"hints sent before a body": func(c *gin.Context) {
			c.Writer.WriteHeader(nethttp.StatusEarlyHints)
			c.Writer.WriteHeaderNow()
			_, _ = c.Writer.WriteString("body")
		},
		"an explicit final status": func(c *gin.Context) {
			c.Writer.WriteHeader(nethttp.StatusEarlyHints)
			c.Writer.WriteHeader(nethttp.StatusCreated)
		},
	} {
		t.Run(name, func(t *testing.T) {
			p, _, reader := layerUnderTest(t)
			var received int
			serve(t, p, routes{{nethttp.MethodGet, "/x"}: handler}.register, func(h *entryHost) {
				received = h.status(t, nethttp.MethodGet, "/x")
			})
			if received < 200 {
				t.Fatalf("the client was answered %d, want a final status", received)
			}

			point := durationPoint(t, reader)
			if got, want := attributeOf(t, point.Attributes, "http.response.status_code"), strconv.Itoa(received); got != want {
				t.Errorf("the status attribute is %q, want the final code %s the caller was answered with", got, want)
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
		if got := point.Attributes.Len(); got != 2 {
			t.Errorf("the data point for %q carries %d attributes, want the method and the status code alone", want, got)
		}
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
	provides := p.layer("api").Provides
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

// TestTheLayerSitsOutsideLayersThatStateNoPlace pins the preference the layer is
// registered with. It is the outermost layer unless another layer asks to be
// further out, so that the time every other layer spends is in the span. A layer
// that states no order, and one that states a slightly smaller one than the
// default, both end up inside it: the second is what tells an order only a little
// below the default from the layer's own.
func TestTheLayerSitsOutsideLayersThatStateNoPlace(t *testing.T) {
	p, _, _ := layerUnderTest(t)
	var inside [2]atomic.Bool
	serve(t, p, func(ep speedhttp.Endpoint) {
		for i, order := range []int{0, -1} {
			ep.Use(speedhttp.Middleware{
				Name:  "test.probe",
				Order: order,
				Handler: func(c *gin.Context) {
					inside[i].Store(trace.SpanFromContext(c.Request.Context()).SpanContext().IsValid())
					c.Next()
				},
			})
		}
		ep.Route(nethttp.MethodGet, "/x", func(c *gin.Context) { c.Status(nethttp.StatusOK) })
	}, func(h *entryHost) {
		h.status(t, nethttp.MethodGet, "/x")
	})

	for i, order := range []int{0, -1} {
		if !inside[i].Load() {
			t.Errorf("a layer with order %d ran outside the instrumentation layer: the time it spends is not in the span", order)
		}
	}
}
