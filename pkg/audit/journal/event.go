package journal

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
)

func (j *journal) subscribe(bus event.Bus) {
	for _, typeName := range unique(j.settings.events) {
		bus.Subscribe(typeName, ModuleName, j.handlerFor(typeName))
	}
}

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
		// carries it so that a subscriber does not have to reconstruct it, and a
		// subscription callback usually has no request context to read one from.
		r := assemble(ctx, audit.SourceEvent, entry, evt.TenantID)
		r.EventID = eventIDOf(evt)
		j.submit(ctx, r)
		return nil
	}
}

func eventIDOf(evt event.Event) *string {
	if evt.ID == "" {
		return nil
	}
	id := evt.ID
	return &id
}

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

func objectOf(payload any) string {
	value, _ := event.PayloadString(payload, "object", "Object", "id", "ID", "name", "Name")
	return value
}

func readOutcome(payload any) string {
	value, _ := event.PayloadString(payload, "outcome", "Outcome", "result", "Result")
	return value
}

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
