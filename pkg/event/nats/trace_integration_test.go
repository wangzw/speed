//go:build integration

package nats

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/tracing"
	"github.com/vislake/speed/pkg/internal/natstest"
	"github.com/vislake/speed/pkg/log"
)

// openBusTracing builds a bus over url whose delivery spans are recorded on
// tracer.
func openBusTracing(t *testing.T, url string, tracer event.TracerProvider) *bus {
	t.Helper()
	opened, err := open(context.Background(), &Config{URL: url, Timeout: 10 * time.Second}, log.Default(), tracer)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	local := opened.(*bus)
	t.Cleanup(func() { _ = local.Close(context.Background()) })
	return local
}

// TestDeliveryContinuesThePublishersTrace is the cross-replica case for this
// backend: an event published inside a span by one replica carries that span's
// tracing context through the NATS stream, and the other replica's subscriber
// records its span as a child of it — the same trace id, not a trace of its own.
func TestDeliveryContinuesThePublishersTrace(t *testing.T) {
	url := natstest.URL(t)
	rec := &recordingTracer{}
	observer := openBusTracing(t, url, rec)
	publisher := openBusTracing(t, url, tracing.Noop())
	evtType := nextType()
	delivered := make(chan struct{}, 1)
	observer.Subscribe(evtType, "nats.subscriber", func(context.Context, event.Event) error {
		delivered <- struct{}{}
		return nil
	})
	if err := observer.begin(context.Background()); err != nil {
		t.Fatalf("begin on the observing replica: %v", err)
	}
	if err := publisher.begin(context.Background()); err != nil {
		t.Fatalf("begin on the publishing replica: %v", err)
	}
	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	spanID := trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}
	carrier := propagation.MapCarrier{}
	tracing.Propagator().Inject(trace.ContextWithSpanContext(context.Background(), spanContext(traceID, spanID)), carrier)
	if err := publisher.Publish(context.Background(), event.Event{
		Type: evtType, TraceContext: map[string]string(carrier),
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case <-delivered:
	case <-time.After(10 * time.Second):
		t.Fatal("the other replica's subscriber did not receive the event")
	}
	deliveries := rec.deliveriesOf("nats.subscriber")
	if len(deliveries) != 1 {
		t.Fatalf("the subscriber's delivery started %d spans, want 1", len(deliveries))
	}
	got := deliveries[0]
	if got.parent.TraceID() != traceID || got.parent.SpanID() != spanID {
		t.Fatalf("the subscriber's span is parented on %s/%s, want the publisher's span %s/%s: the "+
			"tracing context did not survive the stream", got.parent.TraceID(), got.parent.SpanID(), traceID, spanID)
	}
	if v := got.attrs["messaging.system"]; v != "nats" {
		t.Fatalf("messaging.system is %q, want the backend name nats", v)
	}
}
