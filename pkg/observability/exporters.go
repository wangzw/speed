package observability

import (
	"context"
	"errors"
	"sync"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Exporters is one value that is both a span exporter and a metric exporter.
//
// An export backend delivers both capabilities, and the registry requires a
// product to satisfy every capability its descriptor declares — so a backend
// that built two values would deliver neither. This is the shape that satisfies
// both, and a backend is expected to hand it back from New:
//
//	&observability.Exporters{Span: span, Metric: metric}
//
// It lives here rather than in each backend because two copies is how the two
// would drift, and here rather than under pkg/internal because of what it is:
// a combination of the two interfaces this package defines, which a backend
// author needs before they can write a backend. The pkg/internal units hold
// third-party clients and test fixtures; this holds neither.
type Exporters struct {
	// Span delivers spans to the backend.
	Span SpanExporter
	// Metric delivers metrics to the backend.
	Metric MetricExporter

	// Shutdown is declared by both exporter interfaces and therefore exists
	// once here, while there are two exporters to release. It releases both,
	// and the second call finds the work already done: the assembler collects
	// this one value under both capabilities and releases everything it
	// collected, so the value is asked twice for what is one shutdown.
	// Releasing an exporter twice is not something the upstream promises
	// anything about.
	//
	// Releasing both halves on one call is also why the providers never make
	// that call themselves: the first provider to shut down would release the
	// other provider's exporter ahead of its final export. The assembler holds
	// their calls back and releases this value once both have flushed.
	once sync.Once
	err  error
}

// ExportSpans implements SpanExporter.
func (e *Exporters) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return e.Span.ExportSpans(ctx, spans)
}

// Shutdown implements the shutdown method both exporter interfaces declare,
// releasing both exporters exactly once.
func (e *Exporters) Shutdown(ctx context.Context) error {
	e.once.Do(func() {
		e.err = errors.Join(e.Span.Shutdown(ctx), e.Metric.Shutdown(ctx))
	})
	return e.err
}

// Export implements MetricExporter.
func (e *Exporters) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	return e.Metric.Export(ctx, rm)
}

// Temporality implements MetricExporter.
//
// The shaping methods are forwarded rather than answered here. They are the
// backend's statement about the data it wants, and a combination that answered
// them itself would change how the data is shaped without the backend asking.
func (e *Exporters) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return e.Metric.Temporality(kind)
}

// Aggregation implements MetricExporter.
func (e *Exporters) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return e.Metric.Aggregation(kind)
}

// ForceFlush implements MetricExporter.
func (e *Exporters) ForceFlush(ctx context.Context) error { return e.Metric.ForceFlush(ctx) }
