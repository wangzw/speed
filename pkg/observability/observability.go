// Package observability declares the runtime telemetry capability: the traces
// and metrics a process produces while it serves.
//
// This is the interface package. It registers no module. What registers are the
// implementation subpackages: one assembler, which builds the two providers,
// and one module per export backend, each delivering exporters.
//
// The signals themselves are the wrapped library's, and this package adds no
// vocabulary of its own. It re-exports the four types that carry them — the two
// providers a consumer takes up, and the two exporter interfaces a backend
// implements — so that a caller writes the upstream API rather than a parallel
// one. A module that produces telemetry imports this package, takes the
// providers up with core.Resolve[observability.TracerProvider](reg), and hands
// its own instrumentation the upstream types directly.
//
// Logs are not here. They are their own module and their own signal; what this
// package shares with that one is the pair of attribute keys below, which are
// the names a record is correlated under when it belongs to a request that has
// a span.
package observability

import (
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TracerProvider is the capability the assembler delivers exclusively, and the
// entry point to everything that creates a span.
//
// It is an alias rather than a defined type so that a consumer holds the
// upstream interface: the module is a wrapping of that library, and a parallel
// interface would only make every caller convert between two spellings of the
// same method set.
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

// propagator is the composite this module extracts and injects with.
//
// It is a value of this module's own rather than the library's global one: the
// upstream default is an empty composite that silently injects and extracts
// nothing, and installing a global would make one assembly's choice leak into
// another's. Every place in this module that crosses a process boundary uses
// this value through Propagator, so the two sides of a link cannot drift onto
// different formats.
var propagator propagation.TextMapPropagator = propagation.NewCompositeTextMapPropagator(
	propagation.TraceContext{},
	propagation.Baggage{},
)

// Propagator returns the trace-context propagator this module uses on both
// sides of a process boundary.
//
// It is exported for the same reason it exists as one value: a module that
// terminates an inbound protocol of its own — a message consumer, a job runner,
// a gateway — has to extract with the same propagator the server side injects
// with, and a second construction would be a second thing to keep in step.
func Propagator() propagation.TextMapPropagator { return propagator }
