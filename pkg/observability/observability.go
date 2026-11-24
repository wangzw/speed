// Package observability declares the runtime telemetry capability: the
// traces and metrics a process produces while it serves.
package observability

import (
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TracerProvider is the capability the assembler delivers exclusively,
// and the entry point to everything that creates a span.
type TracerProvider = trace.TracerProvider

// MeterProvider is the capability the assembler delivers exclusively, and the
// entry point to everything that creates or records an instrument.
type MeterProvider = metric.MeterProvider

// SpanExporter is the capability an export backend delivers, non-exclusively,
// so that several destinations can be configured at once.
type SpanExporter = sdktrace.SpanExporter

// MetricExporter is the capability an export backend delivers, non-exclusively,
// on the same terms as SpanExporter.
type MetricExporter = sdkmetric.Exporter

const (
	// TraceIDKey is the attribute a trace is correlated under in a log record.
	// The span-producing module binds it; the log module carries the value
	// without knowing the name means anything.
	TraceIDKey = "trace_id"
	// SpanIDKey is the attribute a single span is correlated under in a log
	// record, alongside TraceIDKey.
	SpanIDKey = "span_id"
)

// propagator is the format this module extracts and injects with: the W3C
// trace context, and nothing besides.
var propagator propagation.TextMapPropagator = propagation.TraceContext{}

// Propagator returns the trace-context propagator this module uses on
// both sides of a process boundary.
func Propagator() propagation.TextMapPropagator { return propagator }
