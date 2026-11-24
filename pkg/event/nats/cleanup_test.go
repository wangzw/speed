//go:build integration

package nats

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/bussuite"
	"github.com/vislake/speed/pkg/internal/natstest"
)

func inspect(t *testing.T, url string) jetstream.JetStream {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("opening JetStream: %v", err)
	}
	return js
}

func consumerOf(t *testing.T, js jetstream.JetStream, stream, name string) (jetstream.ConsumerConfig, bool) {
	t.Helper()
	c, err := js.Consumer(context.Background(), stream, name)
	if errors.Is(err, jetstream.ErrConsumerNotFound) || errors.Is(err, jetstream.ErrStreamNotFound) {
		return jetstream.ConsumerConfig{}, false
	}
	if err != nil {
		t.Fatalf("reading the consumer %s on %s: %v", name, stream, err)
	}
	return c.CachedInfo().Config, true
}

func consumerDrained(t *testing.T, js jetstream.JetStream, stream, name string) bool {
	t.Helper()
	ctx := context.Background()
	s, err := js.Stream(ctx, stream)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		return true
	}
	if err != nil {
		t.Fatalf("reading the stream %s: %v", stream, err)
	}
	state, err := s.Info(ctx)
	if err != nil {
		t.Fatalf("reading the state of %s: %v", stream, err)
	}
	c, err := js.Consumer(ctx, stream, name)
	if errors.Is(err, jetstream.ErrConsumerNotFound) || errors.Is(err, jetstream.ErrStreamNotFound) {
		return false
	}
	if err != nil {
		t.Fatalf("reading the consumer %s on %s: %v", name, stream, err)
	}
	info, err := c.Info(ctx)
	if err != nil {
		t.Fatalf("reading the state of the consumer %s on %s: %v", name, stream, err)
	}
	return info.NumPending == 0 && info.NumAckPending == 0 && info.AckFloor.Stream >= state.State.LastSeq
}

// TestClosingRemovesTheProcesssOwnConsumer pins what a process leaves on
// the server once it is gone.
func TestClosingRemovesTheProcesssOwnConsumer(t *testing.T) {
	url := natstest.URL(t)
	eventType := nextType()
	b := openBus(t, url, "")
	b.Subscribe(eventType, "test", func(context.Context, event.Event) error { return nil })
	if err := b.begin(context.Background()); err != nil {
		t.Fatalf("begin: %v", err)
	}
	js := inspect(t, url)
	stream := b.streamName(eventType)
	cfg, ok := consumerOf(t, js, stream, b.consumer)
	if !ok {
		t.Fatalf("the bus's own consumer %s is not on the stream while it reads", b.consumer)
	}
	if cfg.InactiveThreshold != inactiveThreshold {
		t.Fatalf("the process's own consumer carries the inactivity threshold %v, want %v: a process "+
			"that never closes leaves it on the server for good", cfg.InactiveThreshold, inactiveThreshold)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok := consumerOf(t, js, stream, b.consumer); ok {
		t.Fatalf("the consumer %s, named after a process that has closed, is still on the stream", b.consumer)
	}
}

// TestClosingKeepsAConfiguredConsumer is the other side.
func TestClosingKeepsAConfiguredConsumer(t *testing.T) {
	url := natstest.URL(t)
	eventType := nextType()
	name := "configured_" + runToken
	b := openBus(t, url, name)
	b.Subscribe(eventType, "test", func(context.Context, event.Event) error { return nil })
	if err := b.begin(context.Background()); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	js := inspect(t, url)
	stream := b.streamName(eventType)
	t.Cleanup(func() { _ = js.DeleteConsumer(context.Background(), stream, name) })
	cfg, ok := consumerOf(t, js, stream, name)
	if !ok {
		t.Fatalf("the configured consumer %s was removed when one of its replicas closed", name)
	}
	if cfg.InactiveThreshold != 0 {
		t.Fatalf("the configured consumer carries the inactivity threshold %v, and the server would "+
			"remove it while the deployment is down", cfg.InactiveThreshold)
	}
}

// TestReadRecoversWhenTheConsumerIsGone pins recovery when the durable
// vanished under a running reader.
func TestReadRecoversWhenTheConsumerIsGone(t *testing.T) {
	url := natstest.URL(t)
	ctx := context.Background()
	eventType := nextType()
	b := openBus(t, url, "")
	var seen atomic.Int32
	b.Subscribe(eventType, "test", func(context.Context, event.Event) error {
		seen.Add(1)
		return nil
	})
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	stream := b.streamName(eventType)
	js := inspect(t, url)
	if err := js.DeleteConsumer(ctx, stream, b.consumer); err != nil {
		t.Fatalf("deleting the consumer: %v", err)
	}
	// It is the reader that recreates the consumer, not this case: waiting for
	// it to come back is what shows the recovery happened at all.
	bussuite.WaitFor(t, "the reader to recreate its consumer", func() bool {
		_, ok := consumerOf(t, js, stream, b.consumer)
		return ok
	})
	// The consumer starts at the stream's end once it is back; give the
	// reader's first fetch on it a moment to be issued.
	time.Sleep(200 * time.Millisecond)
	if err := b.Publish(ctx, event.Event{Type: eventType}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	bussuite.WaitFor(t, "the event to arrive after the consumer came back", func() bool { return seen.Load() == 1 })
}

// TestAPublishCreatesAGoneStreamAgain pins recovery when the stream vanished
// under a running bus.
func TestAPublishCreatesAGoneStreamAgain(t *testing.T) {
	url := natstest.URL(t)
	ctx := context.Background()
	eventType := nextType()
	b := openBus(t, url, "")
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := b.Publish(ctx, event.Event{Type: eventType}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := inspect(t, url).DeleteStream(ctx, b.streamName(eventType)); err != nil {
		t.Fatalf("deleting the stream: %v", err)
	}
	if err := b.Publish(ctx, event.Event{Type: eventType}); err != nil {
		t.Fatalf("Publish after the stream was deleted: %v", err)
	}
}

// TestAReaderCreatesAGoneStreamAgain is the same recovery on the reading
// side.
func TestAReaderCreatesAGoneStreamAgain(t *testing.T) {
	url := natstest.URL(t)
	ctx := context.Background()
	eventType := nextType()
	b := openBus(t, url, "")
	var seen atomic.Int32
	b.Subscribe(eventType, "test", func(context.Context, event.Event) error {
		seen.Add(1)
		return nil
	})
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	stream := b.streamName(eventType)
	js := inspect(t, url)
	if err := js.DeleteStream(ctx, stream); err != nil {
		t.Fatalf("deleting the stream: %v", err)
	}
	// The durable the reader opens again starts at the stream's end, so what
	// is published once it is back is delivered.
	bussuite.WaitFor(t, "the reader to open its stream and durable again", func() bool {
		_, ok := consumerOf(t, js, stream, b.consumer)
		return ok
	})
	time.Sleep(200 * time.Millisecond)
	if err := b.Publish(ctx, event.Event{Type: eventType}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	bussuite.WaitFor(t, "the event to arrive on the stream created again", func() bool { return seen.Load() == 1 })
}
