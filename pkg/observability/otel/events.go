package otel

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/event"
)

// tenantIDKey is the span attribute a domain fact's tenant is recorded under.
//
// A span attribute rather than a metric one: the tenant dimension is unbounded
// in the number of tenants, and the request histogram next door is the busiest
// signal in the process. On a span it costs one attribute per fact.
const tenantIDKey = attribute.Key("tenant_id")

// spanFor builds the handler that turns one subscribed event type into a span.
//
// What comes out is a short span per fact, not a counter: a counter's name and
// its dimensions are a naming decision, and the module that produces a fact is
// the one that can make it. Reading the payload structurally rather than
// asserting a type is the bus's contract — a publisher and a subscriber share
// no static type, and a subscriber that assumed one would break the moment a
// transport delivered the same fact as decoded data.
//
// An event whose payload cannot be read as an object is dropped whole: there is
// nothing to record about it but its type, and a span carrying only that would
// claim a fact was observed when nothing about it was.
func (p *providers) spanFor() event.Handler {
	tracer := p.tp.Tracer(instrumentationName)
	return func(ctx context.Context, evt event.Event) error {
		fields, ok := event.PayloadFields(evt.Payload)
		if !ok {
			return nil
		}
		// The span's start is when this handler was called, not when the fact
		// happened: the bus carries no timestamp, so an event-derived span sits
		// on the consuming timeline and cannot be lined up with the business
		// span that produced it.
		_, span := tracer.Start(ctx, evt.Type, trace.WithSpanKind(trace.SpanKindConsumer))
		defer span.End()

		for name, value := range fields {
			span.SetAttributes(attribute.String(name, fmt.Sprint(value)))
		}
		// The tenant goes on last, after the payload's own fields. Upstream
		// attributes are last-value-wins, so a payload that happens to carry a
		// field of the same name — which a fact about a tenant easily does —
		// would otherwise overwrite what the bus said. The bus is the authority
		// on which tenant a fact belongs to, and the payload is data a producer
		// wrote.
		if evt.TenantID != "" {
			span.SetAttributes(tenantIDKey.String(evt.TenantID))
		}
		return nil
	}
}
