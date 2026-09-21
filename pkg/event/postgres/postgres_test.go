package postgres

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/bussuite"
	"github.com/vislake/speed/pkg/pgpool"
	"github.com/vislake/speed/pkg/pgpool/pgtest"
)

// openBus builds a bus over the given database.
func openBus(t *testing.T, dsn string) *bus {
	t.Helper()
	opened, err := open(context.Background(), &pgpool.Config{DSN: dsn, MaxConns: 4})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	local := opened.(*bus)
	t.Cleanup(func() { _ = local.Close(context.Background()) })
	return local
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

// TestStanceFailsWithoutAConnectionString pins the enablement rule at this
// backend's own precondition: a module taken in at run level has to start, so an
// implementation the host did not point at a database fails the startup naming
// the item. It is not a stance that lets the run continue without an event bus.
func TestStanceFailsWithoutAConnectionString(t *testing.T) {
	got, err := stance(&pgpool.Config{})
	if err == nil {
		t.Fatal("stance with no connection string did not fail the startup")
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+pgpool.DSNKey) {
		t.Fatalf("the failure does not name the input that caused it: %v", err)
	}
	if got.State == core.StateDisabled {
		t.Fatal("the stance stands this module down, and StateDisabled is not a stance a module " +
			"may state: resolution is its only source")
	}
}

func TestStanceEnablesOnceAConnectionStringIsGiven(t *testing.T) {
	got, err := stance(&pgpool.Config{DSN: "postgres://localhost/x"})
	if err != nil {
		t.Fatalf("stance with a locator failed the startup: %v", err)
	}
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

// TestRetireRunsAtMostOncePerInterval pins the pacing of the retention pass:
// draining used to end in the pass on every round — two large DELETEs at the
// poll interval, per replica, one of them an anti-join over the delivered
// table — a standing load whose window is a day wide.
func TestRetireRunsAtMostOncePerInterval(t *testing.T) {
	b := &bus{}
	start := time.Now()
	runs := 0
	run := func() error { runs++; return nil }
	if err := b.retireIfDue(start, run); err != nil {
		t.Fatalf("the first pass: %v", err)
	}
	if runs != 1 {
		t.Fatal("the first round did not run the retention pass, so a process that just started " +
			"retires nothing until the second interval")
	}
	if err := b.retireIfDue(start.Add(time.Minute), run); err != nil {
		t.Fatalf("the second pass: %v", err)
	}
	if runs != 1 {
		t.Fatal("a round a minute later ran the retention pass again; the pass is due once per " +
			"retireEvery, not once per poll")
	}
	if err := b.retireIfDue(start.Add(retireEvery), run); err != nil {
		t.Fatalf("the third pass: %v", err)
	}
	if runs != 2 {
		t.Fatal("the retention pass is not due when the interval has passed")
	}
}

// TestAFailedRetireDoesNotBuyAnHour pins what a failed pass does to the
// schedule: nothing. Recording the run before running it would leave a
// transient DELETE failure with an hour of silence.
func TestAFailedRetireDoesNotBuyAnHour(t *testing.T) {
	b := &bus{}
	start := time.Now()
	sentinel := errors.New("the connection dropped")
	runs := 0
	fail := func() error { runs++; return sentinel }
	if err := b.retireIfDue(start, fail); !errors.Is(err, sentinel) {
		t.Fatalf("a failed pass returned %v, want the failure itself", err)
	}
	if err := b.retireIfDue(start.Add(time.Second), fail); !errors.Is(err, sentinel) {
		t.Fatalf("the next round returned %v; a failed pass has to be retried, not skipped for "+
			"the whole interval", err)
	}
	if runs != 2 {
		t.Fatalf("the failing pass ran %d times across two rounds, want 2: a failed pass must not "+
			"record itself as done", runs)
	}
}
