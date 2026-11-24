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

const queueChecks = 3

// TestAPublishTheCutLinkRefusesIsNotStoredLater checks that a publish the
// cut link refused is not stored and sent once the link returns.
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

	if err := bus.Publish(context.Background(), event.Event{Type: eventType, ID: "warmup"}); err != nil {
		t.Fatalf("the warm-up publish: %v", err)
	}
	select {
	case <-delivered:
	case <-time.After(30 * time.Second):
		t.Fatalf("the warm-up event was not delivered")
	}

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

	// The publish below has to be issued while the client knows the link is down,
	// when it would otherwise hold the message in its reconnect buffer.
	proxy.Cut()
	cutproxy.Await(t, 30*time.Second, "the client to notice the cut link", bus.nc.IsReconnecting)

	// The deadline is shorter than the library's two-second wait between
	// reconnects, so the publish is decided while the link is still cut.
	refused, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bus.Publish(refused, event.Event{Type: eventType, ID: "refused"}); err == nil {
		t.Fatalf("a publish issued while the link was cut was accepted")
	}

	proxy.Restore()
	cutproxy.Await(t, 30*time.Second, "the connection to come back", bus.nc.IsConnected)
	if err := bus.nc.FlushTimeout(flushBudget); err != nil {
		t.Fatalf("flushing the client before reading the stream: %v", err)
	}
	for check := 1; ; check++ {
		pending, err := bus.nc.Buffered()
		if err != nil {
			t.Fatalf("reading the client's outbound queue: %v", err)
		}
		if pending == 0 {
			break
		}
		if check >= queueChecks {
			t.Fatalf("the client still has %d bytes to send after %d flushes: the refused publish was kept",
				pending, check)
		}
		if err := bus.nc.FlushTimeout(flushBudget); err != nil {
			t.Fatalf("flushing the client again before reading the stream: %v", err)
		}
	}
	if after := entries(); after != before {
		t.Fatalf("the stream holds %d entries after a publish this bus refused, want the %d it held: "+
			"the refused event was kept and sent once the link was back", after, before)
	}

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
