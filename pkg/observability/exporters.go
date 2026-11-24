package observability

import (
	"context"
	"errors"
	"sync"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Exporters is one value that is both a span exporter and a metric
// exporter.
type Exporters struct {
	// Span delivers spans to the backend.
	Span SpanExporter
	// Metric delivers metrics to the backend.
	Metric MetricExporter

	// Shutdown is declared by both exporter interfaces and therefore exists
	// once here, while there are two exporters to release.
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
func (e *Exporters) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return e.Metric.Temporality(kind)
}

// Aggregation implements MetricExporter.
func (e *Exporters) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return e.Metric.Aggregation(kind)
}

// ForceFlush implements MetricExporter.
func (e *Exporters) ForceFlush(ctx context.Context) error { return e.Metric.ForceFlush(ctx) }
