package nats

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/dispatch"
	"github.com/vislake/speed/pkg/event/internal/tracing"
	"github.com/vislake/speed/pkg/log"
)

type recordedDelivery struct {
	name   string
	parent trace.SpanContext
	attrs  map[string]string
}

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

func spanContext(traceID trace.TraceID, spanID trace.SpanID) trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	})
}

// TestTheTraceContextSurvivesTheWireEnvelope pins this backend's line
// format.
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

// TestEveryDeliveryOfOneStoredEntryCarriesTheTracingContext checks that every
// delivery of a stored entry carries the same tracing context.
func TestEveryDeliveryOfOneStoredEntryCarriesTheTracingContext(t *testing.T) {
	const evtType = "redelivery"
	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	spanID := trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}
	carrier := propagation.MapCarrier{}
	tracing.Propagator().Inject(
		trace.ContextWithSpanContext(context.Background(), spanContext(traceID, spanID)), carrier)

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
