package nats

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/dispatch"
	"github.com/vislake/speed/pkg/event/internal/tracing"
	"github.com/vislake/speed/pkg/internal/natstest"
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

// TestTheTraceContextSurvivesTheWireEnvelope pins this backend's line format:
// the tracing context a publish writes into the envelope comes back out of a
// decode. It runs without a server, so the format is held to this wherever the
// suite runs.
func TestTheTraceContextSurvivesTheWireEnvelope(t *testing.T) {
	carrier := map[string]string{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}
	evt := event.Event{ID: "e1", Type: "t", TenantID: "acme", TraceContext: carrier}
	body, err := json.Marshal(envelopeFor(evt, []byte(`{"k":"v"}`)))
	if err != nil {
		t.Fatalf("encoding the envelope: %v", err)
	}
	got := decode("t", body)
	if want := carrier["traceparent"]; got.TraceContext["traceparent"] != want {
		t.Fatalf("the tracing context came out of the envelope as %v, want the traceparent %q",
			got.TraceContext, want)
	}
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

// TestARedeliveryOfTheSameEntryCarriesTheTracingContext pins the design's clause
// that a redelivery of a backend entry carries the same tracing context: the
// context rides in the entry the stream holds, so the entry handed over a
// second time — the durable's ack-wait redelivery, the one duplicate source
// this backend has — recovers the same parent for the subscriber's span rather
// than a trace of its own. The cross-replica cases do not cover it: they hold
// one publish reaching each replica once, not one stored entry arriving twice.
//
// A real ack-wait redelivery needs the server's acknowledgement wait to elapse.
// This implementation sets no AckWait on its durable, so it is JetStream's
// default of thirty seconds, and a case that waited it out would be slow and
// race the reader the suite leaves running. What is held here instead is the
// same property without the wait: the bytes a publish stores are read back and
// delivered twice, and both deliveries have to be parented on the publisher's
// span. An implementation that recovered the context from anything but the
// entry — a per-process record written at publish, the decoding of the first
// arrival — loses it on the second.
func TestEveryDeliveryOfOneStoredEntryCarriesTheTracingContext(t *testing.T) {
	const evtType = "redelivery"
	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	spanID := trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}
	carrier := propagation.MapCarrier{}
	tracing.Propagator().Inject(
		trace.ContextWithSpanContext(context.Background(), spanContext(traceID, spanID)), carrier)

	// The bytes the stream holds: what a publish of this event writes, and what
	// a redelivery hands the reader a second time.
	entry, err := json.Marshal(envelopeFor(event.Event{
		ID: "e1", Type: evtType, TraceContext: map[string]string(carrier),
	}, []byte(`{"k":"v"}`)))
	if err != nil {
		t.Fatalf("encoding the entry the stream holds: %v", err)
	}

	rec := &recordingTracer{}
	b := &bus{table: dispatch.New("event/nats", "nats", log.Default(), rec, tracing.Propagator())}
	b.Subscribe(evtType, "nats.subscriber", func(context.Context, event.Event) error { return nil })
	b.table.Freeze(context.Background())

	// The first arrival, then the redelivery of the same stored entry, each
	// decoded from the entry afresh as the reader decodes the backend's bytes.
	b.table.Deliver(context.Background(), decode(evtType, entry))
	b.table.Deliver(context.Background(), decode(evtType, entry))

	deliveries := rec.deliveriesOf("nats.subscriber")
	if len(deliveries) != 2 {
		t.Fatalf("the same entry delivered twice started %d spans, want 2", len(deliveries))
	}
	for i, d := range deliveries {
		if d.parent.TraceID() != traceID || d.parent.SpanID() != spanID {
			t.Fatalf("delivery %d is parented on %s/%s, want the publisher's span %s/%s: the tracing "+
				"context did not survive the redelivery of the same entry", i+1,
				d.parent.TraceID(), d.parent.SpanID(), traceID, spanID)
		}
	}
}
