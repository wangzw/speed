package journal

import (
	"context"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
)

// subscribe registers one handler per configured type name.
//
// This runs in the Init stage, which is the only stage the bus accepts a
// subscription in. A subscription made later would miss everything published
// before it and would have no way to learn that it had.
func (j *journal) subscribe(bus event.Bus) {
	for _, typeName := range unique(j.settings.events) {
		bus.Subscribe(typeName, j.handlerFor(typeName))
	}
}

// handlerFor builds the handler for one configured type name.
//
// The payload is read structurally, by field name, because a subscriber cannot
// name a publisher's type without inverting the dependency. A payload that does
// not read at all costs the event: the subscriber's contract with its publisher
// is to drop what it cannot read, and there is nobody to hand the failure to —
// a subscriber's failure never reaches the publisher.
//
// That drop is recorded through the log chain, and the chain can be turned off
// entirely by an empty output list, so the record is a non-guarantee rather
// than a report.
func (j *journal) handlerFor(typeName string) event.Handler {
	return func(ctx context.Context, evt event.Event) error {
		if !j.accepting.Load() {
			return nil
		}
		fields, readable := readFields(evt.Payload)
		if !readable {
			j.logger.Info(diagnosticPrefix+ModuleName+": an event payload could not be read",
				"type", typeName)
			return nil
		}
		entry := audit.Entry{
			Action:  typeName,
			Object:  objectOf(evt.Payload),
			Outcome: readOutcome(evt.Payload),
			Detail:  stringsOf(fields),
		}
		// The tenant arrives on the event rather than in the context: the bus
		// carries it so that a subscriber does not have to reconstruct it, and
		// a subscription callback usually has no request context to read one
		// from.
		j.note(j.write(ctx, audit.SourceEvent, entry, evt.TenantID))
		return nil
	}
}

// readFields decodes a payload's top-level fields, reporting whether it could
// be read.
//
// A payload that is simply absent is readable and empty: an event with nothing
// to say is still an event, and dropping it would lose the fact of its
// publication. Only a payload that is present and cannot be decoded is
// unreadable.
func readFields(payload any) (map[string]any, bool) {
	if payload == nil {
		return nil, true
	}
	return event.PayloadFields(payload)
}

// objectOf reads the object the event is about, by the spellings a publisher
// may have used. Nothing found leaves it empty rather than failing.
func objectOf(payload any) string {
	value, _ := event.PayloadString(payload, "object", "Object", "id", "ID", "name", "Name")
	return value
}

// readOutcome reads how the event ended.
func readOutcome(payload any) string {
	value, _ := event.PayloadString(payload, "outcome", "Outcome", "result", "Result")
	return value
}

// stringsOf keeps the payload's string-valued top-level fields as the entry's
// detail. Values of any other shape are left out rather than rendered: the
// detail is written as text, and a rendering chosen here would be this module
// inventing a spelling for someone else's data.
func stringsOf(fields map[string]any) map[string]string {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]string, len(fields))
	for key, value := range fields {
		if text, isString := value.(string); isString {
			out[key] = text
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
