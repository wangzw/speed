package stdout

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// countingSpanExporter counts the shutdowns it is asked for.
type countingSpanExporter struct{ shutdowns int }

func (c *countingSpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return nil
}

func (c *countingSpanExporter) Shutdown(context.Context) error {
	c.shutdowns++
	return nil
}

// countingMetricExporter is the same on the metric side, and answers the
// shaping methods so that the product's forwarding is exercised.
type countingMetricExporter struct {
	shutdowns int
	flushes   int
}

func (c *countingMetricExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func (c *countingMetricExporter) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
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

// TestTheProductSatisfiesBothInterfacesAndShutsDownOnce pins the shape one
// module delivering two capabilities forces on its product: a single Shutdown
// serves both interfaces, so both providers call it, and it must release each
// exporter exactly once. Releasing twice is not something the upstream promises
// anything about, and the failure would show up as an exporter that refuses the
// second shutdown rather than as anything obviously wrong.
func TestTheProductSatisfiesBothInterfacesAndShutsDownOnce(t *testing.T) {
	span := &countingSpanExporter{}
	metric := &countingMetricExporter{}
	product := &exporters{span: span, metric: metric}

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

// TestTheProductForwardsTheShapingMethods pins that the product is transparent
// to everything but data: the temporality and the aggregation are the backend's
// answer, and a product that returned its own would change how the data is
// shaped without anyone asking it to.
func TestTheProductForwardsTheShapingMethods(t *testing.T) {
	metric := &countingMetricExporter{}
	product := &exporters{span: &countingSpanExporter{}, metric: metric}

	if got := product.Temporality(sdkmetric.InstrumentKindCounter); got != metricdata.CumulativeTemporality {
		t.Errorf("Temporality answered %v, want the backend's", got)
	}
	if _, ok := product.Aggregation(sdkmetric.InstrumentKindCounter).(sdkmetric.AggregationDefault); !ok {
		t.Errorf("Aggregation answered %T, want the backend's", product.Aggregation(sdkmetric.InstrumentKindCounter))
	}
	if err := product.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	if metric.flushes != 1 {
		t.Errorf("the backend saw %d flushes, want 1", metric.flushes)
	}
}
