package otel

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

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
// would change how the data is shaped without anyone asking it to; the flush is
// what pushes the backend's own buffer out.
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
	inner.mu.Lock()
	defer inner.mu.Unlock()
	if inner.exports != 1 {
		t.Errorf("the backend saw %d exports, want 1", inner.exports)
	}
	if inner.flushes != 1 {
		t.Errorf("the backend saw %d flushes, want 1", inner.flushes)
	}
}

// TestTheWrappersHoldTheProvidersShutdownBack pins the one lifecycle call the
// wrappers do not forward. A provider shuts down the exporter it holds as the
// last step of its own shutdown, and a backend's product releases both of its
// exporters on that one call: forwarded, the first provider to shut down would
// release the second provider's exporter ahead of its final export. The release
// is the assembler's, made after both providers are done, and the case for it is
// TestCloseDeliversTheLastBatchOfBothSignals.
func TestTheWrappersHoldTheProvidersShutdownBack(t *testing.T) {
	ctx := context.Background()
	lg, _ := logBuffer()
	spans := &flakySpanExporter{}
	metrics := &stubMetricExporter{}

	if err := (&spanExporter{SpanExporter: spans, note: &notifier{lg: lg}}).Shutdown(ctx); err != nil {
		t.Fatalf("the span wrapper's Shutdown: %v", err)
	}
	if err := (&metricExporter{Exporter: metrics, note: &notifier{lg: lg}}).Shutdown(ctx); err != nil {
		t.Fatalf("the metric wrapper's Shutdown: %v", err)
	}
	spans.mu.Lock()
	defer spans.mu.Unlock()
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if spans.shutdowns != 0 || metrics.shutdowns != 0 {
		t.Errorf("a provider's shutdown reached the backend (%d span, %d metric), want neither: the "+
			"backend is released by the assembler once both providers have flushed", spans.shutdowns, metrics.shutdowns)
	}
}

// contextSeen is what an export saw of the context it was handed: whether it
// had already ended, and the deadline it carried.
type contextSeen struct {
	err      error
	deadline time.Time
}

func see(ctx context.Context) contextSeen {
	deadline, _ := ctx.Deadline()
	return contextSeen{err: ctx.Err(), deadline: deadline}
}

// seeingSpans and seeingMetrics record the context of their one export and
// answer with its error, the way an exporter cut short does.
type seeingSpans struct{ seen contextSeen }

func (s *seeingSpans) ExportSpans(ctx context.Context, _ []sdktrace.ReadOnlySpan) error {
	s.seen = see(ctx)
	return s.seen.err
}

func (*seeingSpans) Shutdown(context.Context) error { return nil }

type seeingMetrics struct {
	stubMetricExporter
	seen contextSeen
}

func (s *seeingMetrics) Export(ctx context.Context, _ *metricdata.ResourceMetrics) error {
	s.seen = see(ctx)
	return s.seen.err
}

// TestAnExportBeforeTheStopRunsOnTheContextItWasGiven pins that the cutoff
// changes nothing while the process serves: both wrappers hand the exporter a
// context that has not ended and carries the deadline the upstream set, which
// is batch.export-timeout or metric.timeout, so the exporter's own retry and
// timeout apply as they would without the wrapper.
func TestAnExportBeforeTheStopRunsOnTheContextItWasGiven(t *testing.T) {
	lg, _ := logBuffer()
	cut := newCutoff()
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	want, _ := ctx.Deadline()

	spans := &seeingSpans{}
	_ = (&spanExporter{SpanExporter: spans, note: &notifier{lg: lg}, cut: cut}).ExportSpans(ctx, nil)
	metrics := &seeingMetrics{}
	_ = (&metricExporter{Exporter: metrics, note: &notifier{lg: lg}, cut: cut}).Export(ctx, nil)

	for signal, seen := range map[string]contextSeen{"span": spans.seen, "metric": metrics.seen} {
		if seen.err != nil {
			t.Errorf("the %s export before the stop was handed a context that had ended with %v", signal, seen.err)
		}
		if !seen.deadline.Equal(want) {
			t.Errorf("the %s export before the stop carried the deadline %v, want the upstream's %v", signal, seen.deadline, want)
		}
	}
}

// TestAnExportPastTheCutoffIsCutBeforeItStarts pins the cutoff on both wrappers:
// once it has ended, an export is handed a context that has ended already, so a
// final export made after the cutoff gives up at once instead of retrying until
// the provider's bound, and the wrapper writes its line for it.
func TestAnExportPastTheCutoffIsCutBeforeItStarts(t *testing.T) {
	lg, buf := logBuffer()
	cut := newCutoff()
	disarm := cut.arm(0)
	defer disarm()
	<-cut.ctx.Done()

	spans := &seeingSpans{}
	_ = (&spanExporter{SpanExporter: spans, note: &notifier{lg: lg}, cut: cut}).ExportSpans(context.Background(), nil)
	metrics := &seeingMetrics{}
	_ = (&metricExporter{Exporter: metrics, note: &notifier{lg: lg}, cut: cut}).Export(context.Background(), nil)

	for signal, seen := range map[string]contextSeen{"span": spans.seen, "metric": metrics.seen} {
		if !errors.Is(seen.err, context.Canceled) {
			t.Errorf("the %s export past the cutoff was handed a context with error %v, want it cancelled", signal, seen.err)
		}
	}
	if got := strings.Count(buf.String(), "telemetry export failed"); got != 2 {
		t.Errorf("the cut exports wrote %d failure lines, want one from each wrapper", got)
	}
}
