package otel

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// flakySpanExporter fails while err is set, so a case can drive a destination
// in and out of failure without rebuilding the wrapper.
type flakySpanExporter struct {
	mu          sync.Mutex
	err         error
	shutdowns   int
	exportCalls int
}

func (f *flakySpanExporter) set(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *flakySpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exportCalls++
	return f.err
}

func (f *flakySpanExporter) Shutdown(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdowns++
	return nil
}

// stubMetricExporter answers the shaping methods with values a default would
// not be, so that a wrapper failing to forward them is visible.
type stubMetricExporter struct {
	mu          sync.Mutex
	exports     int
	flushes     int
	shutdowns   int
	temporality metricdata.Temporality
	aggregation sdkmetric.Aggregation
}

func (s *stubMetricExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return s.temporality
}

func (s *stubMetricExporter) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return s.aggregation
}

func (s *stubMetricExporter) Export(context.Context, *metricdata.ResourceMetrics) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exports++
	return nil
}

func (s *stubMetricExporter) ForceFlush(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushes++
	return nil
}

func (s *stubMetricExporter) Shutdown(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shutdowns++
	return nil
}

func logBuffer() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(buf, nil)), buf
}

// TestTheNotifierWritesTheFirstFailureAndTheRecovery pins the shape of the only
// report an export failure produces. A destination that is down fails every
// batch; one line per failure would bury the log, and no line at all would
// leave the failure invisible. The two edges are what is left.
func TestTheNotifierWritesTheFirstFailureAndTheRecovery(t *testing.T) {
	lg, buf := logBuffer()
	flaky := &flakySpanExporter{}
	w := &spanExporter{SpanExporter: flaky, note: &notifier{lg: lg}}
	ctx := context.Background()

	flaky.set(errors.New("collector is down"))
	for range 3 {
		_ = w.ExportSpans(ctx, nil)
	}
	if got := strings.Count(buf.String(), "telemetry export failed"); got != 1 {
		t.Errorf("three failures in a row wrote %d failure lines, want 1", got)
	}

	flaky.set(nil)
	for range 2 {
		_ = w.ExportSpans(ctx, nil)
	}
	if got := strings.Count(buf.String(), "telemetry export recovered"); got != 1 {
		t.Errorf("a recovery wrote %d recovery lines, want 1", got)
	}

	flaky.set(errors.New("collector is down again"))
	_ = w.ExportSpans(ctx, nil)
	if got := strings.Count(buf.String(), "telemetry export failed"); got != 2 {
		t.Errorf("a second run of failures wrote %d failure lines in total, want 2: each run gets its "+
			"own first line", got)
	}
}

// TestTheWrapperForwardsEverythingItDoesNotIntercept is the case that keeps the
// embedding honest. The metric interface carries the temporality and the
// aggregation the backend asked for, and a wrapper that answered those itself
// would change how the data is shaped without anyone asking it to; the
// lifecycle methods are what releases the backend and flushes it.
func TestTheWrapperForwardsEverythingItDoesNotIntercept(t *testing.T) {
	ctx := context.Background()
	inner := &stubMetricExporter{
		temporality: metricdata.DeltaTemporality,
		aggregation: sdkmetric.AggregationDrop{},
	}
	lg, _ := logBuffer()
	w := &metricExporter{Exporter: inner, note: &notifier{lg: lg}}

	if got := w.Temporality(sdkmetric.InstrumentKindCounter); got != metricdata.DeltaTemporality {
		t.Errorf("Temporality answered %v, want the backend's %v", got, metricdata.DeltaTemporality)
	}
	if _, ok := w.Aggregation(sdkmetric.InstrumentKindCounter).(sdkmetric.AggregationDrop); !ok {
		t.Errorf("Aggregation answered %T, want the backend's AggregationDrop", w.Aggregation(sdkmetric.InstrumentKindCounter))
	}
	if err := w.Export(ctx, nil); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := w.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	if err := w.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	inner.mu.Lock()
	defer inner.mu.Unlock()
	if inner.exports != 1 {
		t.Errorf("the backend saw %d exports, want 1", inner.exports)
	}
	if inner.flushes != 1 {
		t.Errorf("the backend saw %d flushes, want 1", inner.flushes)
	}
	if inner.shutdowns != 1 {
		t.Errorf("the backend saw %d shutdowns, want 1: a wrapper that swallowed this would leave the "+
			"exporter's own buffer unflushed at Close", inner.shutdowns)
	}
}

// TestTheSpanWrapperForwardsShutdown pins the trace-side half of the same
// obligation.
func TestTheSpanWrapperForwardsShutdown(t *testing.T) {
	flaky := &flakySpanExporter{}
	lg, _ := logBuffer()
	w := &spanExporter{SpanExporter: flaky, note: &notifier{lg: lg}}
	ctx := context.Background()
	if err := w.ExportSpans(ctx, nil); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}
	if err := w.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	flaky.mu.Lock()
	defer flaky.mu.Unlock()
	if flaky.exportCalls != 1 {
		t.Errorf("the backend saw %d exports, want 1", flaky.exportCalls)
	}
	if flaky.shutdowns != 1 {
		t.Errorf("the backend saw %d shutdowns, want 1", flaky.shutdowns)
	}
}
