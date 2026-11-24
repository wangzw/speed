// Package bussuite holds the contract every implementation of pkg/event
// must satisfy.
package bussuite

import (
	"context"
	"errors"
	"maps"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/event"
)

// Factory builds a fresh bus for one subtest and returns a function that puts it
// into service at the start stage.
type Factory func(t *testing.T) (bus event.Bus, start func(ctx context.Context))

type stopper interface {
	Stop(ctx context.Context) error
}

func stopOf(t *testing.T, b event.Bus) stopper {
	t.Helper()
	s, ok := b.(stopper)
	if !ok {
		t.Fatalf("%T has no stop stage, and every implementation ends its service there", b)
	}
	return s
}

// WaitFor polls until cond holds, and fails the case if it never does.
func WaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	// The bound is generous because delivery through a transport can take seconds
	// on a loaded machine.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a delivery")
		var zero T
		return zero
	}
}

// AssertReplicated runs the part of the contract only a bus carrying events
// between replicas can be held to, against pairs of buses the factory builds.
func AssertReplicated(t *testing.T, newBus Factory) {
	t.Helper()

	t.Run("every replica receives the one identifier", func(t *testing.T) {
		publisher, startPublisher := newBus(t)
		other, startOther := newBus(t)
		here, there := make(chan string, 2), make(chan string, 2)
		publisher.Subscribe("t", "suite", func(_ context.Context, evt event.Event) error {
			here <- evt.ID
			return nil
		})
		other.Subscribe("t", "suite", func(_ context.Context, evt event.Event) error {
			there <- evt.ID
			return nil
		})
		startPublisher(context.Background())
		startOther(context.Background())
		// Broadcast hands one event to every replica, so a consumer whose effect
		// is shared between replicas — one row, one webhook — acts on it once
		// only by recognising the second arrival.
		for _, published := range []event.Event{{Type: "t"}, {ID: "set-by-the-publisher", Type: "t"}} {
			if err := publisher.Publish(context.Background(), published); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			a, b := receive(t, here), receive(t, there)
			if a == "" || a != b {
				t.Fatalf("the replicas received the identifiers %q and %q for one event", a, b)
			}
			if published.ID != "" && a != published.ID {
				t.Fatalf("the replicas received the identifier %q, want the %q its publisher set", a, published.ID)
			}
		}
	})

	t.Run("every replica receives the one tracing context", func(t *testing.T) {
		publisher, startPublisher := newBus(t)
		other, startOther := newBus(t)
		here, there := make(chan map[string]string, 2), make(chan map[string]string, 2)
		publisher.Subscribe("t", "suite", func(_ context.Context, evt event.Event) error {
			here <- evt.TraceContext
			return nil
		})
		other.Subscribe("t", "suite", func(_ context.Context, evt event.Event) error {
			there <- evt.TraceContext
			return nil
		})
		startPublisher(context.Background())
		startOther(context.Background())
		carried := map[string]string{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}
		if err := publisher.Publish(context.Background(), event.Event{Type: "t", TraceContext: carried}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		a, b := receive(t, here), receive(t, there)
		if !maps.Equal(a, carried) || !maps.Equal(b, carried) {
			t.Fatalf("the replicas received the tracing contexts %v and %v, want the %v its publisher set",
				a, b, carried)
		}
	})

	t.Run("an event published as soon as a replica starts reaches it", func(t *testing.T) {
		publisher, startPublisher := newBus(t)
		late, startLate := newBus(t)
		var got atomic.Int32
		late.Subscribe("t", "suite", func(context.Context, event.Event) error {
			got.Add(1)
			return nil
		})
		startPublisher(context.Background())
		// A subscriber registered in Init owns everything published once its
		// implementation's start stage has returned: the replica's reading
		// position is fixed before the stage returns.
		startLate(context.Background())
		if err := publisher.Publish(context.Background(), event.Event{Type: "t"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		WaitFor(t, "the replica that had just started to receive the event", func() bool { return got.Load() == 1 })
	})
}

// AssertConforms runs the whole contract against buses the factory builds.
func AssertConforms(t *testing.T, newBus Factory) {
	t.Helper()

	t.Run("a subscriber receives a published event", func(t *testing.T) {
		b, start := newBus(t)
		var seen atomic.Int32
		b.Subscribe("order.placed", "suite", func(context.Context, event.Event) error {
			seen.Add(1)
			return nil
		})
		start(context.Background())
		if err := b.Publish(context.Background(), event.Event{Type: "order.placed"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		WaitFor(t, "the subscriber to run", func() bool { return seen.Load() == 1 })
	})

	t.Run("matching is by exact type name", func(t *testing.T) {
		b, start := newBus(t)
		var wrong atomic.Int32
		b.Subscribe("order.placed", "suite", func(context.Context, event.Event) error {
			wrong.Add(1)
			return nil
		})
		start(context.Background())
		for _, other := range []string{"order", "order.placed.extra", "Order.Placed", ""} {
			if err := b.Publish(context.Background(), event.Event{Type: other}); err != nil {
				t.Fatalf("Publish(%q): %v", other, err)
			}
		}
		time.Sleep(200 * time.Millisecond)
		if n := wrong.Load(); n != 0 {
			t.Fatalf("a handler for order.placed ran %d times on types it was not subscribed to", n)
		}
	})

	t.Run("every subscriber of a type receives the event", func(t *testing.T) {
		b, start := newBus(t)
		const subscribers = 3
		var seen atomic.Int32
		for i := 0; i < subscribers; i++ {
			b.Subscribe("t", "suite", func(context.Context, event.Event) error {
				seen.Add(1)
				return nil
			})
		}
		start(context.Background())
		if err := b.Publish(context.Background(), event.Event{Type: "t"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		WaitFor(t, "all subscribers to run", func() bool { return seen.Load() == subscribers })
	})

	t.Run("publishing a type nobody subscribed to is accepted", func(t *testing.T) {
		b, start := newBus(t)
		start(context.Background())
		if err := b.Publish(context.Background(), event.Event{Type: "nobody.listens"}); err != nil {
			t.Fatalf("Publish with no subscriber: %v", err)
		}
	})

	t.Run("the event arrives as it was published", func(t *testing.T) {
		b, start := newBus(t)
		want := event.Event{Type: "t", TenantID: "acme", Payload: map[string]any{"k": "carried"}}
		got := make(chan event.Event, 1)
		b.Subscribe("t", "suite", func(_ context.Context, evt event.Event) error {
			got <- evt
			return nil
		})
		start(context.Background())
		if err := b.Publish(context.Background(), want); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		select {
		case evt := <-got:
			if evt.Type != want.Type || evt.TenantID != want.TenantID {
				t.Fatalf("the event arrived as %+v, want %+v", evt, want)
			}
			s, ok := event.PayloadString(evt.Payload, "k")
			if !ok || s != "carried" {
				t.Fatalf("reading the payload through its helpers gave (%q, %v)", s, ok)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for the event to arrive")
		}
	})

	t.Run("the event's tracing context arrives as it was published", func(t *testing.T) {
		b, start := newBus(t)
		carried := map[string]string{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}
		got := make(chan map[string]string, 1)
		b.Subscribe("t", "suite", func(_ context.Context, evt event.Event) error {
			got <- evt.TraceContext
			return nil
		})
		start(context.Background())
		if err := b.Publish(context.Background(), event.Event{Type: "t", TraceContext: carried}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if got := receive(t, got); !maps.Equal(got, carried) {
			t.Fatalf("the event arrived carrying the tracing context %v, want the %v its publisher set",
				got, carried)
		}
	})

	t.Run("an integer past the exact range of a float64 arrives exact", func(t *testing.T) {
		type deal struct {
			AmountCents int64 `json:"amount_cents"`
		}
		type won struct {
			ID   int64 `json:"id"`
			Deal deal  `json:"deal"`
		}
		// 2^53+1 is the first integer a float64 cannot hold; the other is the
		// largest int64.
		want := won{ID: 1<<53 + 1, Deal: deal{AmountCents: 1<<63 - 1}}
		b, start := newBus(t)
		got := make(chan won, 1)
		failed := make(chan error, 1)
		b.Subscribe("t", "suite", func(_ context.Context, evt event.Event) error {
			var w won
			if err := event.DecodePayload(evt.Payload, &w); err != nil {
				failed <- err
				return err
			}
			got <- w
			return nil
		})
		start(context.Background())
		if err := b.Publish(context.Background(), event.Event{Type: "t", Payload: want}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		select {
		case w := <-got:
			if w != want {
				t.Fatalf("the payload decoded as %+v, want %+v", w, want)
			}
		case err := <-failed:
			t.Fatalf("DecodePayload: %v", err)
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for the event to arrive")
		}
	})

	t.Run("an event published without a payload decodes as missing", func(t *testing.T) {
		b, start := newBus(t)
		got := make(chan error, 1)
		b.Subscribe("t", "suite", func(_ context.Context, evt event.Event) error {
			var v struct {
				ID string `json:"id"`
			}
			err := event.DecodePayload(evt.Payload, &v)
			got <- err
			return err
		})
		start(context.Background())
		if err := b.Publish(context.Background(), event.Event{Type: "t"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if err := receive(t, got); !errors.Is(err, event.ErrNoPayload) {
			t.Fatalf("decoding the payload of an event published without one: %v, want ErrNoPayload", err)
		}
	})

	t.Run("every subscriber receives the identifier the bus assigned", func(t *testing.T) {
		b, start := newBus(t)
		first, second := make(chan string, 2), make(chan string, 2)
		b.Subscribe("t", "suite", func(_ context.Context, evt event.Event) error {
			first <- evt.ID
			return nil
		})
		b.Subscribe("t", "suite", func(_ context.Context, evt event.Event) error {
			second <- evt.ID
			return nil
		})
		start(context.Background())
		for range 2 {
			if err := b.Publish(context.Background(), event.Event{Type: "t"}); err != nil {
				t.Fatalf("Publish: %v", err)
			}
		}
		// The identifier is the key an idempotent consumer de-duplicates by, so
		// it has to be there, it has to be the same for every subscriber of one
		// event, and it has to differ between two events.
		var seen []string
		for range 2 {
			a, b := receive(t, first), receive(t, second)
			if a == "" {
				t.Fatal("an event published without an identifier reached its subscriber without one")
			}
			if a != b {
				t.Fatalf("two subscribers of one event received the identifiers %q and %q", a, b)
			}
			seen = append(seen, a)
		}
		if seen[0] == seen[1] {
			t.Fatalf("two events published separately received the one identifier %q", seen[0])
		}
	})

	t.Run("an identifier the publisher set arrives unchanged", func(t *testing.T) {
		b, start := newBus(t)
		got := make(chan string, 1)
		b.Subscribe("t", "suite", func(_ context.Context, evt event.Event) error {
			got <- evt.ID
			return nil
		})
		start(context.Background())
		const mine = "order-42/placed"
		if err := b.Publish(context.Background(), event.Event{ID: mine, Type: "t"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if id := receive(t, got); id != mine {
			t.Fatalf("the event arrived with the identifier %q, want the %q its publisher set", id, mine)
		}
	})

	t.Run("a subscriber's failure does not reach the publisher", func(t *testing.T) {
		b, start := newBus(t)
		var ran atomic.Int32
		b.Subscribe("t", "suite", func(context.Context, event.Event) error {
			ran.Add(1)
			return errors.New("this handler failed on purpose")
		})
		start(context.Background())
		// The contract says Publish's error is about handing the event over,
		// never about a subscriber.
		if err := b.Publish(context.Background(), event.Event{Type: "t"}); err != nil {
			t.Fatalf("Publish reported a subscriber's failure: %v", err)
		}
		WaitFor(t, "the failing subscriber to run", func() bool { return ran.Load() == 1 })
	})

	t.Run("one subscriber's failure does not stop the others", func(t *testing.T) {
		b, start := newBus(t)
		var after atomic.Int32
		b.Subscribe("t", "suite", func(context.Context, event.Event) error {
			return errors.New("this handler fails on purpose")
		})
		b.Subscribe("t", "suite", func(context.Context, event.Event) error {
			after.Add(1)
			return nil
		})
		start(context.Background())
		if err := b.Publish(context.Background(), event.Event{Type: "t"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		WaitFor(t, "the subscriber after the failing one to run",
			func() bool { return after.Load() == 1 })
	})

	t.Run("a subscriber's panic reaches neither the publisher nor the others", func(t *testing.T) {
		b, start := newBus(t)
		var after atomic.Int32
		b.Subscribe("t", "suite", func(context.Context, event.Event) error {
			panic("this handler panics on purpose")
		})
		b.Subscribe("t", "suite", func(context.Context, event.Event) error {
			after.Add(1)
			return nil
		})
		start(context.Background())
		// A panic is a subscriber's failure like any other: it must not come back
		// through Publish or keep the next subscriber from running.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("a subscriber's panic reached the publisher: %v", r)
				}
			}()
			if err := b.Publish(context.Background(), event.Event{Type: "t"}); err != nil {
				t.Fatalf("Publish reported a subscriber's panic: %v", err)
			}
		}()
		WaitFor(t, "the subscriber after the panicking one to run", func() bool { return after.Load() == 1 })
	})

	t.Run("a cancelled context is honoured", func(t *testing.T) {
		b, start := newBus(t)
		var ran atomic.Int32
		b.Subscribe("t", "suite", func(context.Context, event.Event) error {
			ran.Add(1)
			return nil
		})
		start(context.Background())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := b.Publish(ctx, event.Event{Type: "t"}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Publish with a cancelled context: %v, want context.Canceled", err)
		}
		time.Sleep(200 * time.Millisecond)
		if n := ran.Load(); n != 0 {
			t.Fatalf("a subscriber ran %d times for a publish whose context was already cancelled", n)
		}
	})

	t.Run("a publish before the start stage is refused", func(t *testing.T) {
		b, start := newBus(t)
		var ran atomic.Int32
		b.Subscribe("t", "suite", func(context.Context, event.Event) error {
			ran.Add(1)
			return nil
		})
		// The table is still being filled before the start stage, so a publish
		// there would reach whichever subscribers happened to be registered
		// and miss the rest. It is refused, and the refusal says why.
		if err := b.Publish(context.Background(), event.Event{Type: "t"}); !errors.Is(err, event.ErrNotStarted) {
			t.Fatalf("Publish before the start stage: %v, want ErrNotStarted", err)
		}
		start(context.Background())
		time.Sleep(200 * time.Millisecond)
		if n := ran.Load(); n != 0 {
			t.Fatalf("a subscriber ran %d times for a publish the bus refused", n)
		}
	})

	t.Run("delivery outlives the drive's context until the stop stage", func(t *testing.T) {
		b, start := newBus(t)
		var ran atomic.Int32
		b.Subscribe("t", "suite", func(context.Context, event.Event) error {
			ran.Add(1)
			return nil
		})
		// A host cancels the drive's context the moment it begins shutting
		// down, before any module's stop stage — while a request still being
		// drained may publish. The bus serves until its own stop stage.
		drive, cancel := context.WithCancel(context.Background())
		start(drive)
		cancel()
		time.Sleep(2500 * time.Millisecond)
		if err := b.Publish(context.Background(), event.Event{Type: "t"}); err != nil {
			t.Fatalf("Publish after the drive's context was cancelled: %v", err)
		}
		WaitFor(t, "the subscriber to run", func() bool { return ran.Load() == 1 })
	})

	t.Run("a publish from the stop stage on is refused", func(t *testing.T) {
		b, start := newBus(t)
		var ran atomic.Int32
		b.Subscribe("t", "suite", func(context.Context, event.Event) error {
			ran.Add(1)
			return nil
		})
		start(context.Background())
		if err := stopOf(t, b).Stop(context.Background()); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		// No reader is left on this replica to deliver it, so accepting it
		// would report as handed over an event that goes nowhere.
		if err := b.Publish(context.Background(), event.Event{Type: "t"}); !errors.Is(err, event.ErrClosed) {
			t.Fatalf("Publish after the stop stage: %v, want ErrClosed", err)
		}
		time.Sleep(200 * time.Millisecond)
		if n := ran.Load(); n != 0 {
			t.Fatalf("a subscriber ran %d times for a publish the bus refused", n)
		}
	})

	t.Run("a payload that cannot be serialized is refused", func(t *testing.T) {
		b, start := newBus(t)
		var ran atomic.Int32
		b.Subscribe("t", "suite", func(context.Context, event.Event) error {
			ran.Add(1)
			return nil
		})
		start(context.Background())
		// Every implementation refuses it, the in-process one included, which
		// never serializes the payload: a publisher developed against it would
		// otherwise meet the refusal only once the assembly moved to a transport.
		if err := b.Publish(context.Background(), event.Event{Type: "t", Payload: make(chan int)}); err == nil {
			t.Fatal("a payload that cannot be serialized was accepted")
		}
		time.Sleep(200 * time.Millisecond)
		if n := ran.Load(); n != 0 {
			t.Fatalf("a subscriber ran %d times for a publish the bus refused", n)
		}
	})

	t.Run("a subscription after the start stage is refused at the call site", func(t *testing.T) {
		b, start := newBus(t)
		start(context.Background())
		// A subscription arriving now would miss everything published before
		// it with nothing to say so; the call site is the one place the mistake
		// is visible.
		defer func() {
			if recover() == nil {
				t.Fatal("Subscribe after the start stage was accepted")
			}
		}()
		b.Subscribe("t", "suite", func(context.Context, event.Event) error { return nil })
	})

	t.Run("a publish after the bus is closed is refused", func(t *testing.T) {
		b, start := newBus(t)
		start(context.Background())
		c, ok := b.(interface{ Close(context.Context) error })
		if !ok {
			t.Fatalf("%T has no Close", b)
		}
		if err := c.Close(context.Background()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if err := b.Publish(context.Background(), event.Event{Type: "t"}); !errors.Is(err, event.ErrClosed) {
			t.Fatalf("Publish after Close: %v, want ErrClosed", err)
		}
	})

	t.Run("a subscriber running at the stop stage finishes on a live context", func(t *testing.T) {
		b, start := newBus(t)
		entered := make(chan struct{})
		release := make(chan struct{})
		finished := make(chan error, 1)
		b.Subscribe("t", "suite", func(ctx context.Context, _ event.Event) error {
			close(entered)
			<-release
			// What the subscriber would do next — write what it handled —
			// runs on this context, so it must not have been cancelled
			// underneath it.
			finished <- ctx.Err()
			return nil
		})
		drive, cancel := context.WithCancel(context.Background())
		start(drive)
		go func() { _ = b.Publish(context.Background(), event.Event{Type: "t"}) }()
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for the subscriber to start")
		}
		// The shape of a host's shutdown: the drive's context goes first, then
		// the stop stage begins while the subscriber is still running.
		cancel()
		stopped := make(chan error, 1)
		go func() { stopped <- stopOf(t, b).Stop(context.Background()) }()
		time.Sleep(200 * time.Millisecond)
		close(release)
		if err := <-finished; err != nil {
			t.Fatalf("the subscriber running at the stop stage found its context done: %v", err)
		}
		select {
		case err := <-stopped:
			if err != nil {
				t.Fatalf("Stop: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the stop stage did not return after the subscriber finished")
		}
	})
}
