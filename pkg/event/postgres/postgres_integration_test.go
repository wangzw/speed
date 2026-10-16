//go:build integration

package postgres

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/bussuite"
	"github.com/vislake/speed/pkg/event/internal/tracing"
	"github.com/vislake/speed/pkg/internal/pgpool"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/log"
)

// openBus builds a bus over the given database.
func openBus(t *testing.T, dsn string) *bus {
	t.Helper()
	opened, err := open(context.Background(), &pgpool.Config{DSN: dsn, MaxConns: 4}, log.Default(), tracing.Noop())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	local := opened.(*bus)
	t.Cleanup(func() { _ = local.Close(context.Background()) })
	return local
}

// factoryOn builds buses over one database, each a replica of its own.
func factoryOn(dsn string) bussuite.Factory {
	return func(t *testing.T) (event.Bus, func(context.Context)) {
		b := openBus(t, dsn)
		return b, func(ctx context.Context) {
			if err := b.begin(ctx); err != nil {
				t.Fatalf("begin: %v", err)
			}
		}
	}
}

func TestConforms(t *testing.T) {
	bussuite.AssertConforms(t, factoryOn(pgtest.Acquire(t)))
}

func TestReplicated(t *testing.T) {
	bussuite.AssertReplicated(t, factoryOn(pgtest.Acquire(t)))
}

// TestEveryReplicaReceivesEveryEvent is what this backend is for.
//
// The event is published through the second bus, so the first can only have seen
// it by reading the table: nothing in this process hands an event to another
// instance's subscribers.
func TestEveryReplicaReceivesEveryEvent(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	first := openBus(t, dsn)
	second := openBus(t, dsn)

	var gotFirst, gotSecond atomic.Int32
	first.Subscribe("order.placed", "test", func(context.Context, event.Event) error {
		gotFirst.Add(1)
		return nil
	})
	second.Subscribe("order.placed", "test", func(context.Context, event.Event) error {
		gotSecond.Add(1)
		return nil
	})
	if err := first.begin(ctx); err != nil {
		t.Fatalf("begin on the first: %v", err)
	}
	if err := second.begin(ctx); err != nil {
		t.Fatalf("begin on the second: %v", err)
	}

	if err := second.Publish(ctx, event.Event{Type: "order.placed"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	bussuite.WaitFor(t, "the publishing replica's subscriber to run", func() bool { return gotSecond.Load() == 1 })
	bussuite.WaitFor(t, "the other replica's subscriber to run", func() bool { return gotFirst.Load() == 1 })
}

// TestAnEventPublishedWhileNothingWasListeningIsStillDelivered is the window
// the outbox exists for, and it is asserted rather than assumed.
//
// The second bus is built before the event is published and put into service
// only afterwards, so the notification was sent while it was not listening for
// it — which is what NOTIFY cannot cover. The row is still there, and the
// reader's own timeout is what finds it.
func TestAnEventPublishedWhileNothingWasListeningIsStillDelivered(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	publisher := openBus(t, dsn)
	late := openBus(t, dsn)

	var got atomic.Int32
	late.Subscribe("t", "test", func(context.Context, event.Event) error {
		got.Add(1)
		return nil
	})

	// The late bus is opened — its LISTEN is in place — but its reader is not
	// running, and the watermark it took sits at the outbox's end. The
	// publisher is in service: a publish is accepted from the start stage on.
	if err := publisher.begin(ctx); err != nil {
		t.Fatalf("begin on the publisher: %v", err)
	}
	if err := publisher.Publish(ctx, event.Event{Type: "t"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Put it into service only now. A bus that started reading at its watermark
	// picks the row up; one that relied on the notification alone would not.
	if err := late.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	bussuite.WaitFor(t, "the late replica to catch up", func() bool { return got.Load() == 1 })
}

func TestAnUnserializablePayloadIsRefused(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	b := openBus(t, dsn)
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := b.Publish(ctx, event.Event{Type: "t", Payload: make(chan int)}); err == nil {
		t.Fatal("Publish accepted a payload that cannot be serialized")
	}
}

func TestAClosedBusRefusesPublishes(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	b := openBus(t, dsn)
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := b.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := b.Publish(ctx, event.Event{Type: "t"}); !errors.Is(err, event.ErrClosed) {
		t.Fatalf("Publish after Close: %v, want ErrClosed", err)
	}
}
