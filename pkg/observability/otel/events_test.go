package otel

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	"github.com/vislake/speed/pkg/event"
)

// TestTheBusTenantWinsOverAPayloadFieldOfTheSameName pins the order the two
// sources are written in.
//
// Upstream attributes are last-value-wins, and a fact about a tenant very
// easily carries a field of that name in its payload. Written the other way
// round, the payload's value would be the one the span reports — and a payload
// is data a producer wrote, which may be stale, wrong, or absent, while the bus
// is the authority on which tenant a fact belongs to. The failure would be a
// fact recorded under the wrong tenant with nothing saying so.
func TestTheBusTenantWinsOverAPayloadFieldOfTheSameName(t *testing.T) {
	p, rec, _ := layerUnderTest(t)
	handler := p.spanFor()

	evt := event.Event{
		Type:     "order.placed",
		TenantID: "from-the-bus",
		Payload:  map[string]any{"tenant_id": "from-the-payload", "order": "42"},
	}
	if err := handler(context.Background(), evt); err != nil {
		t.Fatalf("the handler returned %v", err)
	}

	spans := rec.recorded()
	if len(spans) != 1 {
		t.Fatalf("the subscription produced %d spans, want 1", len(spans))
	}
	attrs := attribute.NewSet(spans[0].Attributes()...)
	if got := attributeOf(t, attrs, "tenant_id"); got != "from-the-bus" {
		t.Errorf("the span reports tenant_id %q, want the bus's value: a payload field must not "+
			"overwrite the tenant the fact was published under", got)
	}
	if got := attributeOf(t, attrs, "order"); got != "42" {
		t.Errorf("the payload's other fields are %q, want them carried through", got)
	}
}

// TestAnUnreadablePayloadIsDroppedWhole pins what happens to an event whose
// payload cannot be read as an object: nothing. A span carrying only the type
// would claim a fact was observed when nothing about it was.
func TestAnUnreadablePayloadIsDroppedWhole(t *testing.T) {
	p, rec, _ := layerUnderTest(t)
	handler := p.spanFor()

	for _, payload := range []any{nil, "not an object", 7} {
		if err := handler(context.Background(), event.Event{Type: "order.placed", Payload: payload}); err != nil {
			t.Fatalf("the handler returned %v", err)
		}
	}
	if got := len(rec.recorded()); got != 0 {
		t.Errorf("an unreadable payload produced %d spans, want none", got)
	}
}
