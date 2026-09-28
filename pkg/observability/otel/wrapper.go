package otel

import (
	"context"
	"log/slog"
	"sync"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// spanExporter wraps one backend's exporter so that a failed export is not
// silent.
//
// It embeds the interface rather than re-declaring its methods, and that is
// what keeps the wrapper transparent: every method the interface has, including
// the ones that have nothing to do with exporting data and any the upstream
// adds later, is forwarded to the backend unchanged. A hand-written forwarder
// would have to be kept in step by hand, and a method left out of it is a
// silent behaviour change rather than a compile error.
//
// Two calls are intercepted. The export call, because only it can fail in a way
// an operator needs to hear about once, and this wrapper is where that is said.
// And Shutdown, because the release of the backend is the assembler's to make
// rather than the provider's: see Shutdown.
type spanExporter struct {
	sdktrace.SpanExporter
	note *notifier
}

// ExportSpans forwards the batch and reports the outcome, and hands the upstream
// no error back.
//
// The report is the notifier's, and it is the only one. The upstream's batch
// processor hands a failed export to the process-level error handler, and with
// none installed — this module installs none, since the handler is a process
// singleton a second assembly would overwrite — that handler prints a line to
// standard error for every failed batch: outside the log chain, outside its
// redaction, and without the first-failure-only restraint. Beyond that the
// processor only returns the error from a forced flush, which nothing in this
// module asks for; the batch is dropped either way, and retrying is the
// exporter's own business. Absorbing it here therefore costs nothing but those
// lines.
func (w *spanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	w.note.note(w.SpanExporter.ExportSpans(ctx, spans))
	return nil
}

// Shutdown is where the tracer provider's processor releases the exporter it
// holds, and it does not reach the backend.
//
// A backend hands one product for both capabilities, and that product has one
// Shutdown that releases both of its exporters. Forwarded here, the tracer
// provider's shutdown would release the backend's metric exporter as well,
// before the meter provider made its final collection — the metrics recorded
// since the last interval would then be exported into an exporter that is
// already gone, and lost. The assembler releases every exporter it collected
// itself, once both providers have flushed through them (releaseExporters).
func (w *spanExporter) Shutdown(context.Context) error { return nil }

// metricExporter wraps one backend's metric exporter on the same terms.
//
// The metric side is where the embedding earns the most: the interface also
// carries the temporality and the aggregation the backend wants, and a wrapper
// that answered those itself would change how the data is shaped without
// anyone asking it to.
type metricExporter struct {
	sdkmetric.Exporter
	note *notifier
}

// Export forwards the batch and reports the outcome, and hands the upstream no
// error back, for the reason the span side gives: the periodic reader would
// print a line to standard error for every failed interval. The one caller that
// does more with the error is the reader's final export at shutdown, which would
// return it from Close — an export failure there is reported by the same line
// as any other, and it is not a failure of the stop.
func (w *metricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	w.note.note(w.Exporter.Export(ctx, rm))
	return nil
}

// Shutdown is where the meter provider's reader releases the exporter it holds,
// and it does not reach the backend, for the reason the span side gives: the
// release is the assembler's, made once both providers have flushed.
func (w *metricExporter) Shutdown(context.Context) error { return nil }

// notifier writes the two log lines an export failure is worth.
//
// A destination that is down fails every export, and writing a line each time
// would bury the rest of the log under the one failure an operator is already
// looking at. So the first failure of a run is written, the recovery after it
// is written, and the repeats in between are not. Each wrapper holds its own
// notifier, so one destination recovering does not silence another's first
// failure.
//
// This is the module's only mutable state after assembly, and it is confined to
// the wrapper that owns it: the flag is read and written under one mutex, and
// nothing else in the module reads it.
type notifier struct {
	lg *slog.Logger

	mu      sync.Mutex
	failing bool
}

// note records the outcome of one export call.
func (n *notifier) note(err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	switch {
	case err == nil:
		if n.failing {
			n.failing = false
			n.lg.Info("telemetry export recovered")
		}
	case !n.failing:
		n.failing = true
		n.lg.Error("telemetry export failed", "error", err)
	}
}
