// Package event declares the event-bus capability: one module publishes a
// domain fact under a type name, another subscribes to that name, and neither
// has to know the other.
//
// This is the interface package. It registers no module: what registers are the
// implementation subpackages, each binding one backend and each delivering this
// capability exclusively.
//
// It is the entry point through which cross-cutting capabilities — audit,
// observability — take up domain facts. They subscribe to the bus and receive
// what every module publishes, without any of those modules depending on them.
//
// A subscriber usually cannot name a publisher's type: importing it would
// invert the module dependency, and two modules cannot share a type without a
// third package to hold it, which every publisher would then depend on. The
// payload is therefore read structurally — through serialization, by field
// name — and the helpers below are the whole of the support for that.
//
// The delivery promise is a floor every implementation can meet, and it is
// narrow on purpose: a publish reports whether it reached the bus, never
// whether a subscriber handled it. An implementation may promise more, in its
// own documentation; a caller may not rely on it. See the Bus interface.
package event

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrClosed is returned by Publish when the bus has been closed. It describes
// the bus's own state, not anything about the event.
var ErrClosed = errors.New("event: the bus is closed")

// Event is a domain fact published on the bus.
type Event struct {
	// Type is the routing key handlers subscribe to. Subscription matching is
	// an exact string comparison; no wildcards or prefixes are interpreted.
	//
	// It is a string agreed between publisher and subscriber with nothing
	// checking it: a publisher that renames one silently stops reaching its
	// subscribers, and neither side sees an error.
	Type string

	// TenantID identifies the tenant the fact belongs to. It holds the tenant
	// capability's identifier as a string, and stays empty for events that are
	// not tenant-scoped.
	//
	// It is a string rather than the tenant capability's own type because every
	// publisher imports this package: naming that type here would make this
	// package import that module's interface package, and that edge would sit in
	// every publisher's closure. The cost is that edge — the bus reaching a
	// concrete capability's interface — rather than a third-party stack, since
	// that interface package imports the standard library alone.
	TenantID string

	// Payload carries the event data. Subscribers read it structurally, by
	// field name, rather than asserting a type; see the package documentation.
	Payload any
}

// Handler processes a single published Event. A non-nil error reports that this
// handler failed to process the event. It never prevents the remaining handlers
// of the same event from running, and it never reaches the publisher: no
// implementation hands a subscriber's failure back through Publish, which is
// what keeps that error meaning one thing.
//
// A handler that panics has failed in the same sense, and every implementation
// treats it the same way: the panic is recovered and reported, the remaining
// handlers still run, and nothing of it reaches the publisher — not even on the
// in-process bus, where the handler runs on the publisher's own goroutine.
type Handler func(ctx context.Context, evt Event) error

// Bus is the capability every implementation delivers.
//
// The published promise is a floor, and it has three parts:
//
//   - Publish returning nil means the event was accepted by the bus. It does
//     not mean any subscriber received it or finished handling it.
//   - Publish returning non-nil means the event was not accepted, and the
//     failure was in handing it over: a payload that cannot be serialized, a
//     backend that is unreachable, a bus that is closed. It does not mean a
//     subscriber failed.
//   - A subscriber's failure never reaches the publisher.
//
// An implementation may promise more, and says so in its own documentation. The
// process-local bus, for one, delivers to every subscriber on the publisher's
// own goroutine, so by the time Publish returns they have all run; a caller must
// not rely on that, because a change of implementation takes it away without any
// caller-side edit.
//
// The bus promises neither delivery count nor order. Exactly-once across
// replicas and publication order are properties of an implementation and its
// backend; a caller that needs ordering carries a sequence number in the
// payload.
type Bus interface {
	// Publish delivers evt to every handler subscribed to evt.Type.
	Publish(ctx context.Context, evt Event) error

	// Subscribe registers h for events whose Type equals eventType exactly.
	Subscribe(eventType string, h Handler)
}

// PayloadFields decodes an event payload of any of the shapes it arrives in
// into its top-level fields.
//
// A payload reaches this package in one of two shapes: a same-process publish
// hands over the publisher's own concrete value, while a bus backed by a
// transport delivers whatever that transport decoded, ordinarily a
// map[string]any carrying the same serialization. A subscriber reads either the
// same way.
//
// It reports false when the payload is nil, cannot be serialized, or does not
// decode to an object. It never panics and never returns a partial value: a
// subscriber's contract with its publisher is to drop what it cannot read, so
// there is no error to hand back on this path.
func PayloadFields(payload any) (map[string]any, bool) {
	if payload == nil {
		return nil, false
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, false
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil || fields == nil {
		return nil, false
	}
	return fields, true
}

// PayloadString returns the first non-empty string found among keys, probed in
// the order given so a caller can list the spellings a field may carry — its
// serialization tag, the struct field's own name.
//
// A key present with a value that is not a non-empty string is skipped, so the
// probe continues to the next spelling. It reports false for every unusable
// shape PayloadFields reports, and when no key carries a usable value.
func PayloadString(payload any, keys ...string) (string, bool) {
	fields, ok := PayloadFields(payload)
	if !ok {
		return "", false
	}
	for _, key := range keys {
		value, present := fields[key]
		if !present {
			continue
		}
		if s, isString := value.(string); isString && s != "" {
			return s, true
		}
	}
	return "", false
}

// PayloadBool is PayloadString's boolean counterpart.
//
// false is a meaningful value rather than a miss: a payload explicitly carrying
// false answers (false, true), so only an absent or non-boolean value moves the
// probe on.
func PayloadBool(payload any, keys ...string) (bool, bool) {
	fields, ok := PayloadFields(payload)
	if !ok {
		return false, false
	}
	for _, key := range keys {
		value, present := fields[key]
		if !present {
			continue
		}
		if b, isBool := value.(bool); isBool {
			return b, true
		}
	}
	return false, false
}

// DecodePayload decodes a payload of any of its shapes into v, which must be a
// non-nil pointer, by round-tripping it through serialization.
//
// It is the typed counterpart of the probes: a subscriber that can name a shape
// of its own, or that must surface the failure rather than drop the event,
// decodes into it directly instead of probing field spellings.
func DecodePayload(payload any, v any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, v)
}
