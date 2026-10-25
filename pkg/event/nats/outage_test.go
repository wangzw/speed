//go:build integration

package nats

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/cutproxy"
	"github.com/vislake/speed/pkg/internal/natstest"
)

// flushBudget bounds the round trip that drains the client's outbound queue.
// A client with nothing left to send answers at once; one that has stopped
// answering fails the case rather than hanging it.
const flushBudget = 10 * time.Second

// TestAPublishTheCutLinkRefusesIsNotStoredLater is the case for what a failed
// publish means here, the lower bound design-event states: a publish that
// returns an error is one the bus did not accept. The publisher of an event
// the bus refused is free to publish it again under a new identifier, or not
// at all; either choice is wrong if the bus kept the refused event and sent it
// once its link was back, because the consumer then counts an event the
// publisher was told had not happened.
//
// The case cuts the link, waits until the client knows it is down, publishes
// there, and restores the link. The stream is then read through a connection
// of its own: nothing of that publish may be in it.
func TestAPublishTheCutLinkRefusesIsNotStoredLater(t *testing.T) {
	server := natstest.URL(t)
	proxy := cutproxy.Start(t, strings.TrimPrefix(server, "nats://"))
	bus := openBus(t, "nats://"+proxy.Addr(), "")
	eventType := nextType()
	delivered := make(chan event.Event, 8)
	bus.Subscribe(eventType, "test", func(_ context.Context, evt event.Event) error {
		delivered <- evt
		return nil
	})
	if err := bus.begin(context.Background()); err != nil {
		t.Fatalf("begin: %v", err)
	}

	// Warm up through the proxy: the stream is there, this bus's reader is
	// live, and the case has a count to compare against.
	if err := bus.Publish(context.Background(), event.Event{Type: eventType, ID: "warmup"}); err != nil {
		t.Fatalf("the warm-up publish: %v", err)
	}
	select {
	case <-delivered:
	case <-time.After(30 * time.Second):
		t.Fatalf("the warm-up event was not delivered")
	}

	// The stream is read through a connection of its own, so its count stays
	// observable while the bus's link is cut.
	directConn, err := nats.Connect(server)
	if err != nil {
		t.Fatalf("connecting to the server directly: %v", err)
	}
	t.Cleanup(directConn.Close)
	direct, err := jetstream.New(directConn)
	if err != nil {
		t.Fatalf("opening JetStream directly: %v", err)
	}
	stream, err := direct.Stream(context.Background(), bus.streamName(eventType))
	if err != nil {
		t.Fatalf("binding to the event type's stream: %v", err)
	}
	entries := func() uint64 {
		info, err := stream.Info(context.Background())
		if err != nil {
			t.Fatalf("reading the stream: %v", err)
		}
		return info.State.Msgs
	}
	before := entries()

	// The publish below has to be issued while the client knows the link is
	// down: that is the state in which the client library would otherwise hold
	// the message in its reconnect buffer and send it once the server
	// answered again.
	proxy.Cut()
	cutproxy.Await(t, 30*time.Second, "the client to notice the cut link", bus.nc.IsReconnecting)

	// The deadline is shorter than the library's wait between reconnect
	// attempts (two seconds), so the publish is decided while the link is
	// still cut.
	refused, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bus.Publish(refused, event.Event{Type: eventType, ID: "refused"}); err == nil {
		t.Fatalf("a publish issued while the link was cut was accepted")
	}

	proxy.Restore()
	cutproxy.Await(t, 30*time.Second, "the connection to come back", bus.nc.IsConnected)
	// With the link back, flush the client and confirm it has nothing left to
	// send before reading the stream: the flush drives whatever the client is
	// holding through a round trip to the server, so a publish it kept would
	// have reached the server by now, and the empty outbound queue afterwards
	// is the observation that none is still waiting to go out. A fixed pause
	// would instead let a client slow to send what it kept pass this case.
	if err := bus.nc.FlushTimeout(flushBudget); err != nil {
		t.Fatalf("flushing the client before reading the stream: %v", err)
	}
	if pending, err := bus.nc.Buffered(); err != nil {
		t.Fatalf("reading the client's outbound queue: %v", err)
	} else if pending != 0 {
		t.Fatalf("the client still has %d bytes to send after a flush: the refused publish was kept", pending)
	}
	if after := entries(); after != before {
		t.Fatalf("the stream holds %d entries after a publish this bus refused, want the %d it held: "+
			"the refused event was kept and sent once the link was back", after, before)
	}

	// The link is usable again: the reader takes what is published now, so
	// nothing was left in a state where the connection takes no publish.
	if err := bus.Publish(context.Background(), event.Event{Type: eventType, ID: "after"}); err != nil {
		t.Fatalf("a publish after the link came back: %v", err)
	}
	select {
	case evt := <-delivered:
		if evt.ID != "after" {
			t.Fatalf("the reader delivered %q after the link came back, want the event published then", evt.ID)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("a publish after the link came back was not delivered")
	}
}
