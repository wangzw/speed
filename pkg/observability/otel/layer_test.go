package otel

import (
	"bytes"
	"context"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/log"
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

// durationPoint collects the one histogram the layer records and returns its
// data point.
func durationPoint(t *testing.T, reader *sdkmetric.ManualReader) metricdata.HistogramDataPoint[float64] {
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
			if len(hist.DataPoints) != 1 {
				t.Fatalf("%s has %d data points, want 1", requestDurationName, len(hist.DataPoints))
			}
			return hist.DataPoints[0]
		}
	}
	t.Fatalf("no %s was recorded", requestDurationName)
	return metricdata.HistogramDataPoint[float64]{}
}

func attributeOf(t *testing.T, attrs attribute.Set, key string) string {
	t.Helper()
	v, ok := attrs.Value(attribute.Key(key))
	if !ok {
		t.Fatalf("no %s attribute among %v", key, attrs.ToSlice())
	}
	return v.String()
}

// TestTheLayerRecordsTheSpanAndTheRequestDuration covers the ordinary path: one
// span named for the endpoint and the method, one data point carrying the
// method and the status code the handler actually wrote.
func TestTheLayerRecordsTheSpanAndTheRequestDuration(t *testing.T) {
	p, rec, reader := layerUnderTest(t)
	handler := p.instrument("api")(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusTeapot)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(nethttp.MethodGet, "http://example.test/x", nil))

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
}

// TestTheLayerRecordsTheStatusEvenWhenTheHandlerNeverWritesOne pins the default
// the recorder starts from: a handler that only writes a body has answered 200,
// and the attribute has to say so rather than report a zero.
func TestTheLayerRecordsTheStatusEvenWhenTheHandlerNeverWritesOne(t *testing.T) {
	p, _, reader := layerUnderTest(t)
	handler := p.instrument("api")(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = w.Write([]byte("body"))
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(nethttp.MethodPost, "http://example.test/x", nil))

	point := durationPoint(t, reader)
	if got := attributeOf(t, point.Attributes, "http.response.status_code"); got != "200" {
		t.Errorf("the status attribute is %q, want 200", got)
	}
}

// TestTheLayerFoldsMethodsOutsideTheStandardSet pins the folding that keeps the
// method dimension closed. Without it a client could grow the dimension one
// verb at a time, and the resulting series would be unbounded in a signal that
// is meant to be cheap.
func TestTheLayerFoldsMethodsOutsideTheStandardSet(t *testing.T) {
	p, _, reader := layerUnderTest(t)
	handler := p.instrument("api")(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PURGE", "http://example.test/x", nil))

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
	handler := p.instrument("api")(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {}))

	const incoming = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	req := httptest.NewRequest(nethttp.MethodGet, "http://example.test/x", nil)
	req.Header.Set("traceparent", incoming)
	handler.ServeHTTP(httptest.NewRecorder(), req)

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
// reads them.
func TestTheLayerBindsTheCorrelationFieldsForDownstreamLogs(t *testing.T) {
	p, _, _ := layerUnderTest(t)
	buf := &bytes.Buffer{}
	lg := slog.New(slog.NewJSONHandler(buf, nil))

	var downstream context.Context
	handler := p.instrument("api")(nethttp.HandlerFunc(func(_ nethttp.ResponseWriter, r *nethttp.Request) {
		downstream = r.Context()
	}))
	req := httptest.NewRequest(nethttp.MethodGet, "http://example.test/x", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req.WithContext(log.WithLogger(req.Context(), lg)))

	if downstream == nil {
		t.Fatal("the handler was not reached")
	}
	log.FromContext(downstream).Info("downstream")
	line := buf.String()
	if !strings.Contains(line, `"trace_id"`) || !strings.Contains(line, `"span_id"`) {
		t.Errorf("a record written downstream is %s, want it to carry trace_id and span_id", line)
	}
}

// TestTheLayerRecordsAPanicAndLetsItThrough pins both halves of the recovery
// the layer does: the span says the request failed, and the panic goes on to
// whatever handles it outside this layer. Swallowing it would take over a
// promise that belongs to the entry module.
func TestTheLayerRecordsAPanicAndLetsItThrough(t *testing.T) {
	p, rec, _ := layerUnderTest(t)
	handler := p.instrument("api")(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {
		panic("handler gave up")
	}))

	var raised any
	func() {
		defer func() { raised = recover() }()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(nethttp.MethodGet, "http://example.test/x", nil))
	}()
	if raised == nil {
		t.Fatal("the layer swallowed the panic instead of letting the entry module handle it")
	}

	spans := rec.recorded()
	if len(spans) != 1 {
		t.Fatalf("the layer ended %d spans, want the panicking request to have ended one", len(spans))
	}
	if got := spans[0].Status().Code; got != codes.Error {
		t.Errorf("the span's status is %v, want Error: a panicking request must not be reported as a success", got)
	}
}

// TestTheLayerReportsAPanicAsAnInternalErrorOutcome pins what the histogram
// says about a request the handler abandoned.
//
// The recorder's status is untouched when nothing was written, so reporting it
// reports the 200 it started from — putting every panicking request into the
// success bucket, which is the one place an operator most needs it not to be.
// The response the caller ends up with is the entry module's, whose recovery
// layer answers a panicking handler with an internal error.
func TestTheLayerReportsAPanicAsAnInternalErrorOutcome(t *testing.T) {
	p, _, reader := layerUnderTest(t)
	handler := p.instrument("api")(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {
		panic("handler gave up")
	}))

	func() {
		defer func() { _ = recover() }()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(nethttp.MethodGet, "http://example.test/x", nil))
	}()

	point := durationPoint(t, reader)
	if got := attributeOf(t, point.Attributes, "http.response.status_code"); got != "500" {
		t.Errorf("a panicking request is recorded as %q, want the internal error the caller is left "+
			"with", got)
	}
}

// TestTheLayerFoldsTheMethodInTheSpanNameToo pins that the method dimension is
// closed in both signals. The histogram folds anything outside the standard set
// precisely because it is unbounded otherwise, and a span name is indexed by a
// backend just as a dimension is; leaving the span out of that would move the
// cardinality from the signal that was guarded to the one that was not.
func TestTheLayerFoldsTheMethodInTheSpanNameToo(t *testing.T) {
	p, rec, _ := layerUnderTest(t)
	handler := p.instrument("api")(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PURGE", "http://example.test/x", nil))

	spans := rec.recorded()
	if len(spans) != 1 {
		t.Fatalf("the layer ended %d spans, want 1", len(spans))
	}
	if got, want := spans[0].Name(), "api "+otherMethod; got != want {
		t.Errorf("the span is named %q, want %q: an unfolded method would let a caller grow the span "+
			"name one verb at a time", got, want)
	}
}

// TestTheLayerKeepsTheFirstStatusWritten pins which WriteHeader counts. The
// server ignores a second one and answers with the first, so a recorder that
// took the last would report a status no caller ever saw.
func TestTheLayerKeepsTheFirstStatusWritten(t *testing.T) {
	p, _, reader := layerUnderTest(t)
	handler := p.instrument("api")(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
		w.WriteHeader(nethttp.StatusInternalServerError)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(nethttp.MethodGet, "http://example.test/x", nil))

	point := durationPoint(t, reader)
	if got := attributeOf(t, point.Attributes, "http.response.status_code"); got != "200" {
		t.Errorf("the status attribute is %q, want the first code written, which is the one the caller "+
			"received", got)
	}
}

// TestTheLayerForwardsTheOptionalInterfaces pins the two the standard library
// names. A handler that type-asserts the writer makes the assertion against
// this layer, never against the writer underneath, so without the forwarding
// its streaming branch is skipped and the endpoint degrades with nothing said.
func TestTheLayerForwardsTheOptionalInterfaces(t *testing.T) {
	p, _, _ := layerUnderTest(t)
	var flushed bool
	sentinel := &flushRecorder{header: nethttp.Header{}, flushes: &flushed}
	handler := p.instrument("api")(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		f, ok := w.(nethttp.Flusher)
		if !ok {
			t.Error("the writer does not satisfy http.Flusher, so a streaming handler would skip its flush")
			return
		}
		f.Flush()
	}))
	handler.ServeHTTP(sentinel, httptest.NewRequest(nethttp.MethodGet, "http://example.test/x", nil))
	if !flushed {
		t.Error("Flush did not reach the writer underneath")
	}
}

// flushRecorder is a writer that can flush and says so.
type flushRecorder struct {
	header  nethttp.Header
	flushes *bool
}

func (f *flushRecorder) Header() nethttp.Header { return f.header }

func (f *flushRecorder) Write(b []byte) (int, error) { return len(b), nil }

func (f *flushRecorder) WriteHeader(int) {}

func (f *flushRecorder) Flush() { *f.flushes = true }

// TestTheLayerExposesTheWriterUnderneath pins the Unwrap. A handler that
// asserts the writer for flushing or hijacking reaches the real one through it;
// without it, such a handler fails on every instrumented endpoint and nothing
// says so.
func TestTheLayerExposesTheWriterUnderneath(t *testing.T) {
	p, _, _ := layerUnderTest(t)
	var inner nethttp.ResponseWriter
	handler := p.instrument("api")(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		if u, ok := w.(interface{ Unwrap() nethttp.ResponseWriter }); ok {
			inner = u.Unwrap()
		}
	}))
	sentinel := httptest.NewRecorder()
	handler.ServeHTTP(sentinel, httptest.NewRequest(nethttp.MethodGet, "http://example.test/x", nil))

	if inner != sentinel {
		t.Errorf("the writer underneath is %v, want the one the server handed the layer: a flushing "+
			"handler would not reach it", inner)
	}
}
