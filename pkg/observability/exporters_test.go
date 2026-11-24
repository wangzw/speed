package observability_test

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/vislake/speed/pkg/observability"
)

type countingSpanExporter struct {
	shutdowns int
}

func (c *countingSpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return nil
}

func (c *countingSpanExporter) Shutdown(context.Context) error {
	c.shutdowns++
	return nil
}

// countingMetricExporter is the same on the metric side, and answers the
// shaping methods with values a default would not be, so that a combination
// failing to forward them is visible.
type countingMetricExporter struct {
	shutdowns int
	flushes   int
}

func (c *countingMetricExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.DeltaTemporality
}

func (c *countingMetricExporter) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDrop{}
}

func (c *countingMetricExporter) Export(context.Context, *metricdata.ResourceMetrics) error {
	return nil
}

func (c *countingMetricExporter) ForceFlush(context.Context) error {
	c.flushes++
	return nil
}

func (c *countingMetricExporter) Shutdown(context.Context) error {
	c.shutdowns++
	return nil
}

// TestExportersSatisfiesBothInterfacesAndShutsDownOnce pins the shape one
// module delivering two capabilities forces on its product.
func TestExportersSatisfiesBothInterfacesAndShutsDownOnce(t *testing.T) {
	span := &countingSpanExporter{}
	metric := &countingMetricExporter{}
	product := &observability.Exporters{Span: span, Metric: metric}

	var _ sdktrace.SpanExporter = product
	var _ sdkmetric.Exporter = product

	ctx := context.Background()
	if err := product.Shutdown(ctx); err != nil {
		t.Fatalf("the first shutdown: %v", err)
	}
	if err := product.Shutdown(ctx); err != nil {
		t.Fatalf("the second shutdown: %v", err)
	}
	if span.shutdowns != 1 {
		t.Errorf("the span exporter was shut down %d times, want 1", span.shutdowns)
	}
	if metric.shutdowns != 1 {
		t.Errorf("the metric exporter was shut down %d times, want 1", metric.shutdowns)
	}
}

// TestExportersForwardsTheShapingMethods pins that the combination is
// transparent to everything but data.
func TestExportersForwardsTheShapingMethods(t *testing.T) {
	metric := &countingMetricExporter{}
	product := &observability.Exporters{Span: &countingSpanExporter{}, Metric: metric}

	if got := product.Temporality(sdkmetric.InstrumentKindCounter); got != metricdata.DeltaTemporality {
		t.Errorf("Temporality answered %v, want the backend's", got)
	}
	if _, ok := product.Aggregation(sdkmetric.InstrumentKindCounter).(sdkmetric.AggregationDrop); !ok {
		t.Errorf("Aggregation answered %T, want the backend's", product.Aggregation(sdkmetric.InstrumentKindCounter))
	}
	if err := product.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	if metric.flushes != 1 {
		t.Errorf("the backend saw %d flushes, want 1", metric.flushes)
	}
}
