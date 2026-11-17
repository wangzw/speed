package stream

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/event"
)

// A publish into a full buffer returns at once: it drops the frame instead of
// waiting for the slow reader, and the buffer stays at its depth.
func TestPublishDoesNotBlockOnAFullBuffer(t *testing.T) {
	h := &Hub{}
	conn := h.Subscribe("t1", "u1")

	for i := 0; i < bufferDepth; i++ {
		h.Publish(inboxEvent("t1", "u1"))
	}
	if got := len(conn.ch); got != bufferDepth {
		t.Fatalf("the buffer holds %d frames after %d publishes, want %d", got, bufferDepth, bufferDepth)
	}

	done := make(chan struct{})
	go func() {
		h.Publish(inboxEvent("t1", "u1"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(coretest.Scale(30 * time.Second)):
		t.Fatal("Publish blocked on a full buffer instead of dropping the frame")
	}
	if got := len(conn.ch); got != bufferDepth {
		t.Errorf("an overfull publish changed the buffer to %d, want %d", got, bufferDepth)
	}
}

// Close is idempotent and never closes the message channel: a second Close is a
// no-op, the done signal is set, and a receive on the channel blocks rather than
// returning a closed-channel zero value.
func TestCloseIsIdempotentAndNeverClosesTheChannel(t *testing.T) {
	h := &Hub{}
	conn := h.Subscribe("t1", "u1")

	h.Close(conn)
	h.Close(conn)

	select {
	case <-conn.done:
	default:
		t.Fatal("Close did not signal done")
	}
	select {
	case _, ok := <-conn.ch:
		if !ok {
			t.Fatal("the message channel was closed by Close")
		}
		t.Fatal("a frame arrived on a closed connection")
	case <-time.After(50 * time.Millisecond):
		// The receive blocked, which is what an open, empty channel does. A
		// closed one returns at once.
	}
}

// Matching isolates one scope from another: a connection receives only the
// events published to its own (tenant, recipient) pair.
func TestPublishMatchesOnlyTheConnectionsOwnScope(t *testing.T) {
	h := &Hub{}
	mine := h.Subscribe("t1", "u1")
	sameTenant := h.Subscribe("t1", "u2")
	otherTenant := h.Subscribe("t2", "u1")

	h.Publish(inboxEvent("t1", "u1"))

	if got := len(mine.ch); got != 1 {
		t.Errorf("the connection in scope holds %d frames, want 1", got)
	}
	if got := len(sameTenant.ch); got != 0 {
		t.Errorf("a connection of the same tenant, different recipient holds %d frames, want 0", got)
	}
	if got := len(otherTenant.ch); got != 0 {
		t.Errorf("a connection of another tenant holds %d frames, want 0", got)
	}
}

// The frame carries the payload without its tenant identifier: the tenant is
// the scope the hub matched on and is internal, so it is not something the
// browser reads. The rest of the payload is unchanged.
func TestTheFrameCarriesNoTenantIdentifier(t *testing.T) {
	h := &Hub{}
	conn := h.Subscribe("t1", "u1")

	h.Publish(inboxEvent("t1", "u1"))

	select {
	case f := <-conn.ch:
		var fields map[string]any
		if err := json.Unmarshal(f.data, &fields); err != nil {
			t.Fatalf("the frame's data %q is not a JSON object: %v", f.data, err)
		}
		if _, present := fields["tenant_id"]; present {
			t.Errorf("the frame carries tenant_id: %s", f.data)
		}
		if fields["message_id"] != "message-1" || fields["recipient_user_id"] != "u1" {
			t.Errorf("the frame dropped a field a client reads: %s", f.data)
		}
	default:
		t.Fatal("the connection received no frame")
	}
}

// Every connection on a matching scope receives the frame, not just one.
func TestPublishReachesEveryConnectionOnAMatchingScope(t *testing.T) {
	h := &Hub{}
	first := h.Subscribe("t1", "u1")
	second := h.Subscribe("t1", "u1")

	h.Publish(inboxEvent("t1", "u1"))

	if got := len(first.ch); got != 1 {
		t.Errorf("the first connection holds %d frames, want 1", got)
	}
	if got := len(second.ch); got != 1 {
		t.Errorf("the second connection holds %d frames, want 1", got)
	}
}

// An event that names no tenant, or no recipient, cannot be placed in any scope
// and is dropped: no connection receives it.
func TestAnEventWithNoScopeIsDropped(t *testing.T) {
	h := &Hub{}
	conn := h.Subscribe("t1", "u1")

	h.Publish(event.Event{
		Type:    eventInboxCreated,
		Payload: map[string]any{"message_id": "message-1", "type_key": "billing.invoice_paid"},
	})
	h.Publish(event.Event{
		Type:    eventInboxCreated,
		Payload: map[string]any{"message_id": "message-1", "tenant_id": "t1", "type_key": "billing.invoice_paid"},
	})

	if got := len(conn.ch); got != 0 {
		t.Errorf("a connection received %d frames from events that name no scope, want 0", got)
	}
}

// A connection that was closed is unregistered: a later matching publish finds
// nothing and delivers to nobody.
func TestAClosedConnectionIsUnregistered(t *testing.T) {
	h := &Hub{}
	conn := h.Subscribe("t1", "u1")
	h.Close(conn)

	h.Publish(inboxEvent("t1", "u1"))

	if got := len(conn.ch); got != 0 {
		t.Errorf("a closed connection received %d frames, want 0", got)
	}
}

// closeAll ends every open connection, which is what Stop runs.
func TestCloseAllEndsEveryConnection(t *testing.T) {
	h := &Hub{}
	first := h.Subscribe("t1", "u1")
	second := h.Subscribe("t2", "u2")

	h.closeAll()

	for name, conn := range map[string]*connection{"first": first, "second": second} {
		select {
		case <-conn.done:
		default:
			t.Errorf("%s connection was not signalled done", name)
		}
	}
}

// Concurrent publishes against a closing and reopening connection never panic:
// the message channel is never closed, so a send racing a Close lands or is
// dropped and never hits a closed channel.
func TestConcurrentPublishAndCloseDoNotPanic(t *testing.T) {
	h := &Hub{}
	conn := h.Subscribe("t1", "u1")

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					h.Publish(inboxEvent("t1", "u1"))
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			h.Close(conn)
			conn = h.Subscribe("t1", "u1")
		}
		close(stop)
	}()

	wg.Wait()
	h.Close(conn)
}
