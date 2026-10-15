package nats

import (
	"context"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/bussuite"
	"github.com/vislake/speed/pkg/internal/natstest"
)

// TestAnEntryDeliveredWhileStoppingIsAcknowledged pins what the stop stage
// leaves behind on this backend: nothing awaiting acknowledgement. An entry
// this consumer was handed and never acknowledged goes back to the durable only
// after the server's acknowledgement wait, and only a consumer of the same name
// ever receives it again — which, for a process-unique name, is no one.
func TestAnEntryDeliveredWhileStoppingIsAcknowledged(t *testing.T) {
	url := natstest.URL(t)
	ctx := context.Background()
	eventType := nextType()
	b := openBus(t, url, "")
	entered := make(chan struct{})
	release := make(chan struct{})
	b.Subscribe(eventType, "test", func(context.Context, event.Event) error {
		close(entered)
		<-release
		return nil
	})
	drive, cancel := context.WithCancel(ctx)
	if err := b.begin(drive); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := b.Publish(ctx, event.Event{Type: eventType}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the subscriber to start")
	}
	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- b.Stop(ctx) }()
	time.Sleep(200 * time.Millisecond)
	close(release)
	if err := <-stopped; err != nil {
		t.Fatalf("Stop: %v", err)
	}

	consumer, err := b.js.Consumer(ctx, b.streamName(eventType), b.consumer)
	if err != nil {
		t.Fatalf("taking the consumer: %v", err)
	}
	bussuite.WaitFor(t, "the entry delivered while stopping to be acknowledged", func() bool {
		info, err := consumer.Info(ctx)
		return err == nil && info.NumAckPending == 0 && info.AckFloor.Stream >= 1
	})
}
