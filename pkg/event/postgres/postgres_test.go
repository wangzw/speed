package postgres

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/bussuite"
	"github.com/vislake/speed/pkg/event/internal/pgtest"
)

// errFailedOnPurpose is what a subscriber returns in the cases that check what
// the publisher is told about it.
var errFailedOnPurpose = errors.New("this handler failed on purpose")

// openBus builds a bus over the given database.
func openBus(t *testing.T, dsn string) *bus {
	t.Helper()
	opened, err := open(context.Background(), &Config{DSN: dsn, MaxConns: 4})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	local := opened.(*bus)
	t.Cleanup(func() { _ = local.Close(context.Background()) })
	return local
}

// waitFor polls until cond holds, and fails the case if it never does. Delivery
// here happens on the reader goroutine, after Publish has returned, so every
// assertion about it waits.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestConforms(t *testing.T) {
	dsn := pgtest.Acquire(t)
	bussuite.AssertConforms(t, func(t *testing.T) (event.Bus, func()) {
		b := openBus(t, dsn)
		return b, func() {
			if err := b.begin(context.Background()); err != nil {
				t.Fatalf("begin: %v", err)
			}
		}
	})
}

func TestStanceStandsDownWithoutAConnectionString(t *testing.T) {
	got := stance(&Config{})
	if got.State != core.StateDisabled {
		t.Fatalf("stance with no locator = %v, want StateDisabled", got.State)
	}
	if got.Reason == "" {
		t.Fatal("the stand-down carries no reason")
	}
}

func TestStanceEnablesOnceAConnectionStringIsGiven(t *testing.T) {
	got := stance(&Config{DSN: "postgres://localhost/x"})
	if got.State != core.StateEnabled {
		t.Fatalf("stance with a locator = %v, want StateEnabled", got.State)
	}
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
	first.Subscribe("order.placed", func(context.Context, event.Event) error {
		gotFirst.Add(1)
		return nil
	})
	second.Subscribe("order.placed", func(context.Context, event.Event) error {
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
	waitFor(t, "the publishing replica's subscriber to run", func() bool { return gotSecond.Load() == 1 })
	waitFor(t, "the other replica's subscriber to run", func() bool { return gotFirst.Load() == 1 })
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
	late.Subscribe("t", func(context.Context, event.Event) error {
		got.Add(1)
		return nil
	})

	// The late bus is opened — its LISTEN is in place — but its reader is not
	// running, and the watermark it took sits at the outbox's end.
	if err := publisher.Publish(ctx, event.Event{Type: "t"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Put it into service only now. A bus that started reading at its watermark
	// picks the row up; one that relied on the notification alone would not.
	if err := late.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	waitFor(t, "the late replica to catch up", func() bool { return got.Load() == 1 })
}

// TestPublishReportsNothingAboutSubscribers pins the capability's floor against
// a real server.
func TestPublishReportsNothingAboutSubscribers(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	b := openBus(t, dsn)
	var ran atomic.Int32
	b.Subscribe("t", func(context.Context, event.Event) error {
		ran.Add(1)
		return errFailedOnPurpose
	})
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := b.Publish(ctx, event.Event{Type: "t"}); err != nil {
		t.Fatalf("Publish reported a subscriber's failure: %v", err)
	}
	waitFor(t, "the failing subscriber to run", func() bool { return ran.Load() == 1 })
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
