package stream

import (
	"context"
	"encoding/json"
	"testing"
)

// An event on the bus reaches the connection whose scope matches it, as a frame
// carrying the event's payload.
func TestAnEventDeliversAFrameToTheConnectionInItsScope(t *testing.T) {
	p, bus := newTestProduct()
	p.subscribe()
	conn := p.hub.Subscribe("t1", "u1")

	if err := bus.Publish(context.Background(), inboxEvent("t1", "u1")); err != nil {
		t.Fatalf("publishing the event: %v", err)
	}

	select {
	case f := <-conn.ch:
		if f.event != frameEventName {
			t.Errorf("the frame event is %q, want %q", f.event, frameEventName)
		}
		var got map[string]any
		if err := json.Unmarshal(f.data, &got); err != nil {
			t.Fatalf("the frame data %q is not JSON: %v", f.data, err)
		}
		if got["message_id"] != "message-1" {
			t.Errorf("the frame data carries message_id %v, want message-1", got["message_id"])
		}
		if got["recipient_user_id"] != "u1" {
			t.Errorf("the frame data carries recipient_user_id %v, want u1", got["recipient_user_id"])
		}
	default:
		t.Fatal("the connection in scope received no frame")
	}
}

// A connection outside the event's scope receives nothing.
func TestAnEventReachesNoConnectionOutsideItsScope(t *testing.T) {
	p, bus := newTestProduct()
	p.subscribe()
	conn := p.hub.Subscribe("t2", "u2")

	if err := bus.Publish(context.Background(), inboxEvent("t1", "u1")); err != nil {
		t.Fatalf("publishing the event: %v", err)
	}

	if got := len(conn.ch); got != 0 {
		t.Errorf("a connection outside the scope received %d frames, want 0", got)
	}
}

// The subscription is on the inbox-created event type, spelled as a literal.
func TestTheSubscriptionIsOnTheInboxCreatedType(t *testing.T) {
	p, bus := newTestProduct()
	p.subscribe()

	if got := bus.subscribed(eventInboxCreated); got != 1 {
		t.Errorf("subscribed %d handlers to %q, want 1", got, eventInboxCreated)
	}
	if eventInboxCreated != "notification.inbox.created" {
		t.Errorf("the subscribed type is %q, want notification.inbox.created", eventInboxCreated)
	}
}
