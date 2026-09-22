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
// Only the export call is intercepted, because only it can fail in a way an
// operator needs to hear about once.
type spanExporter struct {
	sdktrace.SpanExporter
	note *notifier
}

// ExportSpans forwards the batch and reports the outcome.
func (w *spanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := w.SpanExporter.ExportSpans(ctx, spans)
	w.note.note(err)
	return err
}

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

// Export forwards the batch and reports the outcome.
func (w *metricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	err := w.Exporter.Export(ctx, rm)
	w.note.note(err)
	return err
}

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
