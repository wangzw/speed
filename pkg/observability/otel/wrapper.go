package otel

import (
	"context"
	"log/slog"
	"sync"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// spanExporter wraps one backend's exporter so that a failed export is
// not silent.
type spanExporter struct {
	sdktrace.SpanExporter
	note *notifier
	cut  *cutoff
}

// ExportSpans forwards the batch and reports the outcome, and hands the
// upstream no error back.
func (w *spanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	ctx, release := w.cut.join(ctx)
	defer release()
	w.note.note(w.SpanExporter.ExportSpans(ctx, spans))
	return nil
}

// Shutdown is where the tracer provider's processor releases the exporter
// it holds, and it does not reach the backend.
func (w *spanExporter) Shutdown(context.Context) error { return nil }

type metricExporter struct {
	sdkmetric.Exporter
	note *notifier
	cut  *cutoff
}

// Export forwards the batch, reports the outcome, and hands the upstream no error
// back: the reader would otherwise print a line per failed interval.
func (w *metricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	ctx, release := w.cut.join(ctx)
	defer release()
	w.note.note(w.Exporter.Export(ctx, rm))
	return nil
}

// Shutdown is where the meter provider's reader releases the exporter it holds,
// and it does not reach the backend, for the reason the span side gives: the
// release is the assembler's, made once both providers have flushed.
func (w *metricExporter) Shutdown(context.Context) error { return nil }

// cutoff is the moment of a stop past which no export runs on.
type cutoff struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func newCutoff() *cutoff {
	ctx, cancel := context.WithCancel(context.Background())
	return &cutoff{ctx: ctx, cancel: cancel}
}

// join returns ctx ended by the cutoff as well, and the function that
// undoes the join once the export has returned.
func (c *cutoff) join(ctx context.Context) (context.Context, func()) {
	if c == nil {
		return ctx, func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	if c.ctx.Err() != nil {
		cancel()
		return ctx, cancel
	}
	stop := context.AfterFunc(c.ctx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

// arm ends the cutoff after d and returns the function that releases the stop's
// hold on it.
func (c *cutoff) arm(d time.Duration) func() {
	if c == nil {
		return func() {}
	}
	t := time.AfterFunc(d, c.end)
	return func() {
		t.Stop()
		c.end()
	}
}

func (c *cutoff) end() { c.cancel() }

type notifier struct {
	lg *slog.Logger

	mu      sync.Mutex
	failing bool
}

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
