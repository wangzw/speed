package journal

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
)

// subscribe registers one handler per configured type name.
//
// It runs in the Init stage: the bus freezes its subscription table at its own
// Start callback, which the drive reaches after every module's Init. A
// subscription registered after that freeze does not cover anything published
// before it, whether the bus refuses it at the call site or accepts it and
// misses those events.
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
			Detail:  detailOf(fields),
		}
		// The tenant arrives on the event rather than in the context: the bus
		// carries it so that a subscriber does not have to reconstruct it, and
		// a subscription callback usually has no request context to read one
		// from.
		j.submit(ctx, audit.SourceEvent, entry, evt.TenantID)
		return nil
	}
}

// readFields decodes a payload's top-level fields, each as the text the
// payload's own serialization gave it, reporting whether it could be read.
//
// A payload that is simply absent is readable and empty: an event with nothing
// to say is still an event, and dropping it would lose the fact of its
// publication. Only a payload that is present and does not serialize to an
// object is unreadable, which is the line the bus's own field helpers draw.
func readFields(payload any) (map[string]json.RawMessage, bool) {
	if payload == nil {
		return nil, true
	}
	var fields map[string]json.RawMessage
	if err := event.DecodePayload(payload, &fields); err != nil || fields == nil {
		return nil, false
	}
	return fields, true
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

// detailOf keeps the payload's top-level scalar fields as the entry's detail.
//
// A string is kept as its value, and a boolean or a number as the text the
// payload's serialization wrote for it. That text is the payload's own spelling
// rather than one this module chose, and it keeps a number exact where decoding
// it first would round an integer past a float's range. Those fields are where a
// publisher puts the facts that tell one kind of event from another — a delete
// from a write of the empty value, a withheld value from an unchanged one, the
// version a change produced — so leaving them out loses the distinction.
//
// A nested object, an array and a null are left out. Masking compares whole
// top-level keys, so a key inside a nested value is never compared at all, and
// rendering the value as text would put whatever it carries into the table
// unmasked.
func detailOf(fields map[string]json.RawMessage) map[string]string {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]string, len(fields))
	for key, raw := range fields {
		if text, scalar := scalarText(raw); scalar {
			out[key] = text
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// scalarText renders one serialized field, reporting false for the shapes the
// detail leaves out.
func scalarText(raw json.RawMessage) (string, bool) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 {
		return "", false
	}
	switch first := value[0]; {
	case first == '"':
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return "", false
		}
		return text, true
	case first == 't', first == 'f', first == '-', first >= '0' && first <= '9':
		return string(value), true
	default:
		return "", false
	}
}
