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

// TestAPayloadFieldIsRecordedAsPublished pins what a field becomes on the span:
// the value the publisher wrote, in the type it wrote it in. A payload travels
// as JSON, so a number read the ordinary way is a float64, and printing that
// turns an amount of 2500000 into "2.5e+06" and a 64-bit identifier into a
// neighbouring number. A nested object or list is written as the JSON it was
// published as, and a null carries no value to record.
func TestAPayloadFieldIsRecordedAsPublished(t *testing.T) {
	p, rec, _ := layerUnderTest(t)
	handler := p.spanFor()

	payload := struct {
		Amount  int64             `json:"amount"`
		ID      int64             `json:"id"`
		Huge    uint64            `json:"huge"`
		Price   float64           `json:"price"`
		Won     bool              `json:"won"`
		Stage   string            `json:"stage"`
		Tags    []string          `json:"tags"`
		Address map[string]string `json:"address"`
		Note    *string           `json:"note"`
	}{
		Amount:  2500000,
		ID:      1234567890123456789,
		Huge:    18446744073709551615,
		Price:   19.99,
		Won:     true,
		Stage:   "closed",
		Tags:    []string{"a", "b"},
		Address: map[string]string{"city": "Oslo"},
	}
	if err := handler(context.Background(), event.Event{Type: "deal.won", Payload: payload}); err != nil {
		t.Fatalf("the handler returned %v", err)
	}

	spans := rec.recorded()
	if len(spans) != 1 {
		t.Fatalf("the subscription produced %d spans, want 1", len(spans))
	}
	attrs := attribute.NewSet(spans[0].Attributes()...)
	for key, want := range map[string]attribute.Value{
		"amount":  attribute.Int64Value(2500000),
		"id":      attribute.Int64Value(1234567890123456789),
		"huge":    attribute.StringValue("18446744073709551615"),
		"price":   attribute.Float64Value(19.99),
		"won":     attribute.BoolValue(true),
		"stage":   attribute.StringValue("closed"),
		"tags":    attribute.StringValue(`["a","b"]`),
		"address": attribute.StringValue(`{"city":"Oslo"}`),
	} {
		got, ok := attrs.Value(attribute.Key(key))
		switch {
		case !ok:
			t.Errorf("the span carries no %s attribute", key)
		case got != want:
			t.Errorf("the %s attribute is %s %q, want %s %q", key, got.Type(), got.String(), want.Type(), want.String())
		}
	}
	if got, ok := attrs.Value("note"); ok {
		t.Errorf("a null field became the attribute %q, want none", got.String())
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
