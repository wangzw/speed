package stdout

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/vislake/speed/pkg/core"
)

// exporters is this module's product: one value that is both a span exporter
// and a metric exporter.
//
// The two capabilities are delivered by one module, so the registry's delivery
// check requires one product to satisfy both — a module that produced two
// values would deliver neither. The consequence is that Shutdown, which both
// interfaces declare and which therefore exists once here, has two exporters to
// release: it releases both, and the second call finds the work already done.
// Without that guard the tracer provider's shutdown and the meter provider's
// would each release both, and an exporter released twice is not something the
// upstream promises anything about.
type exporters struct {
	span   sdktrace.SpanExporter
	metric sdkmetric.Exporter

	once sync.Once
	err  error
}

// newExporters builds the two exporters from this module's configuration.
//
// A construction failure is handed back wrapped rather than given a name of its
// own: the judgement is the upstream's and the fix is the endpoint or the
// writer the configuration gave, so a sentinel here would name a category this
// module cannot describe any better than the error text already does.
func newExporters(_ context.Context, reg *core.Registry) (any, error) {
	cfg, err := read(reg)
	if err != nil {
		return nil, err
	}
	var spanOpts []stdouttrace.Option
	var metricOpts []stdoutmetric.Option
	if cfg.Pretty {
		spanOpts = append(spanOpts, stdouttrace.WithPrettyPrint())
		metricOpts = append(metricOpts, stdoutmetric.WithPrettyPrint())
	}
	span, err := stdouttrace.New(spanOpts...)
	if err != nil {
		return nil, fmt.Errorf("%s: the span exporter could not be built: %w", ModuleName, err)
	}
	metric, err := stdoutmetric.New(metricOpts...)
	if err != nil {
		return nil, fmt.Errorf("%s: the metric exporter could not be built: %w", ModuleName, err)
	}
	return &exporters{span: span, metric: metric}, nil
}

// ExportSpans implements observability.SpanExporter.
func (e *exporters) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return e.span.ExportSpans(ctx, spans)
}

// Shutdown implements the shutdown method both exporter interfaces declare.
func (e *exporters) Shutdown(ctx context.Context) error {
	e.once.Do(func() {
		e.err = errors.Join(e.span.Shutdown(ctx), e.metric.Shutdown(ctx))
	})
	return e.err
}

// Export implements observability.MetricExporter.
func (e *exporters) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	return e.metric.Export(ctx, rm)
}

// Temporality implements observability.MetricExporter.
func (e *exporters) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return e.metric.Temporality(kind)
}

// Aggregation implements observability.MetricExporter.
func (e *exporters) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return e.metric.Aggregation(kind)
}

// ForceFlush implements observability.MetricExporter.
func (e *exporters) ForceFlush(ctx context.Context) error { return e.metric.ForceFlush(ctx) }
