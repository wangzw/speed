// Package event declares the event-bus capability: a module publishes a
// domain fact under a type name and another subscribes to that name,
// without either knowing the other.
package event

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrClosed is returned by Publish once the bus has stopped: from its stop
// stage on and after it is closed.
var ErrClosed = errors.New("event: the bus is closed")

// ErrNotStarted is returned by Publish before the bus has reached its start
// stage.
var ErrNotStarted = errors.New("event: the bus has not started")

// ErrNoPayload is returned by DecodePayload for an event that carries no
// payload: nil, or a value that serializes to null — a nil pointer, map or
// slice — in whichever shape it arrived.
var ErrNoPayload = errors.New("event: the event carries no payload")

// Event is a domain fact published on the bus.
type Event struct {
	// ID identifies this event and is the key a consumer de-duplicates by.
	// Publish assigns one when empty, in the form NewID mints; a non-empty
	// value is carried as is and its shape is not checked.
	ID string

	// Type is the routing key handlers subscribe to; matching is an exact string
	// comparison, with no wildcards or prefixes. A rename silently stops reaching
	// subscribers, and neither side sees an error.
	Type string

	// TenantID identifies the tenant the fact belongs to as a string, empty
	// for events that are not tenant-scoped.
	TenantID string

	// TraceContext carries the publisher's tracing context, keyed by the
	// upstream propagation format (a traceparent and, when present, a
	// tracestate).
	TraceContext map[string]string

	// Payload carries the event data. Subscribers read it structurally, by
	// field name, rather than asserting a type; see the package documentation.
	Payload any
}

// Handler processes a single published Event. A non-nil error reports that this
// handler failed: it never stops the other handlers and never reaches the
// publisher, and a panic is recovered and reported the same way.
type Handler func(ctx context.Context, evt Event) error

// Bus is the capability every implementation delivers.
type Bus interface {
	// Publish delivers evt to every handler subscribed to evt.Type. It is accepted
	// from the start stage until the stop stage: before, ErrNotStarted, and from
	// the stop stage on, ErrClosed.
	Publish(ctx context.Context, evt Event) error

	// Subscribe registers h for events whose Type equals eventType exactly. name
	// identifies the subscriber and names the span of every delivery to it; an
	// empty name panics at the call site.
	Subscribe(eventType string, name string, h Handler)
}

// PayloadFields decodes an event payload of any shape into its top-level
// fields. Numbers read as float64, exact to 2^53; DecodePayload reads
// larger integers exactly.
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
// the order given. It reports false for every unusable shape PayloadFields
// reports, and when no key carries a usable value.
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

// PayloadBool is PayloadString's boolean counterpart. false is a value rather
// than a miss: only an absent or non-boolean value moves the probe on.
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

// DecodePayload decodes a payload of any shape into v, which must be a non-nil
// pointer. An event that carries no payload is reported as ErrNoPayload with v
// unchanged; an integer field reads exactly whatever its size.
func DecodePayload(payload any, v any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if string(encoded) == "null" {
		return ErrNoPayload
	}
	return json.Unmarshal(encoded, v)
}
