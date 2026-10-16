//go:build integration

package redis

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/tracing"
	speedredis "github.com/vislake/speed/pkg/internal/redis"
	"github.com/vislake/speed/pkg/internal/redis/redistest"
	"github.com/vislake/speed/pkg/log"
)

// recordedDelivery is one span the dispatch layer started for a subscriber: the
// name it was recorded under, the parent it was handed and the attributes it
// carried.
type recordedDelivery struct {
	name   string
	parent trace.SpanContext
	attrs  map[string]string
}

// recordingTracer keeps every span the dispatch layer starts, so a case can
// hold a delivery's parent and attributes to what the design fixes without an
// exporter.
type recordingTracer struct {
	mu    sync.Mutex
	spans []recordedDelivery
}

func (r *recordingTracer) Start(ctx context.Context, name string, attrs map[string]string) (context.Context, event.Span) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans = append(r.spans, recordedDelivery{name: name, parent: trace.SpanContextFromContext(ctx), attrs: attrs})
	return ctx, deliveredSpan{}
}

type deliveredSpan struct{}

func (deliveredSpan) End(error) {}

// deliveriesOf lists every span started for the named subscriber.
func (r *recordingTracer) deliveriesOf(name string) []recordedDelivery {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []recordedDelivery
	for _, s := range r.spans {
		if s.name == name {
			out = append(out, s)
		}
	}
	return out
}

// spanContext is a valid span context with the given identifiers, so a case can
// name the span a delivery is expected to be parented on.
func spanContext(traceID trace.TraceID, spanID trace.SpanID) trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	})
}

// openBusTracing builds a bus over the given server whose delivery spans are
// recorded on tracer.
func openBusTracing(t *testing.T, addr string, db int, tracer event.TracerProvider) *bus {
	t.Helper()
	proto := Config{Config: speedredis.Defaults()}
	proto.Addr, proto.DB = addr, db
	opened, err := open(context.Background(), &proto, log.Default(), tracer)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	local := opened.(*bus)
	t.Cleanup(func() { _ = local.Close(context.Background()) })
	return local
}

// TestDeliveryContinuesThePublishersTrace is the cross-replica case for this
// backend: an event published inside a span by one replica carries that span's
// tracing context through the Redis stream, and the other replica's subscriber
// records its span as a child of it — the same trace id, not a trace of its own.
func TestDeliveryContinuesThePublishersTrace(t *testing.T) {
	addr, db := redistest.Acquire(t)
	rec := &recordingTracer{}
	observer := openBusTracing(t, addr, db, rec)
	publisher := openBusTracing(t, addr, db, tracing.Noop())
	delivered := make(chan struct{}, 1)
	observer.Subscribe("t", "redis.subscriber", func(context.Context, event.Event) error {
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
		Type: "t", TraceContext: map[string]string(carrier),
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case <-delivered:
	case <-time.After(10 * time.Second):
		t.Fatal("the other replica's subscriber did not receive the event")
	}
	deliveries := rec.deliveriesOf("redis.subscriber")
	if len(deliveries) != 1 {
		t.Fatalf("the subscriber's delivery started %d spans, want 1", len(deliveries))
	}
	got := deliveries[0]
	if got.parent.TraceID() != traceID || got.parent.SpanID() != spanID {
		t.Fatalf("the subscriber's span is parented on %s/%s, want the publisher's span %s/%s: the "+
			"tracing context did not survive the stream", got.parent.TraceID(), got.parent.SpanID(), traceID, spanID)
	}
	if v := got.attrs["messaging.system"]; v != "redis" {
		t.Fatalf("messaging.system is %q, want the backend name redis", v)
	}
}
