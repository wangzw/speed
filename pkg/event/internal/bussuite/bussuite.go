// Package bussuite holds the contract every implementation of pkg/event must
// satisfy.
//
// It is written once and run by each backend, so "an implementation meets the
// capability's promises" is a single statement a backend either passes or does
// not.
//
// What is deliberately not here: delivery across replicas, durability, ordering,
// and exactly-once. Those are properties of a backend rather than of the
// capability, and the capability promises none of them; a backend that does
// promise one tests it where its own machinery lives.
package bussuite

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/event"
)

// Factory builds a fresh bus for one subtest, with an open subscription table
// and no relation to the buses built before it, and returns a function that
// puts it into service.
//
// The second result is why this is not a plain constructor. An implementation
// that delivers in-process has nothing to start and returns a no-op, but one
// that reads from a transport cannot deliver until its readers are running —
// and the readers cannot start before the table is complete, which is after the
// case has subscribed. A case therefore subscribes and then calls the function.
type Factory func(t *testing.T) (bus event.Bus, start func())

// WaitFor polls until cond holds, and fails the case if it never does.
//
// The polling is what lets one contract cover both delivery paths: a
// process-local bus has run every handler by the time Publish returns, while a
// bus backed by a transport delivers after it has. A case may not assume
// either, so it waits instead.
//
// It is exported so that a backend's own cases — the ones that go past the
// suite into what its backend does — wait the same way, at the same pace. A
// copy with a longer bound would let a case pass here that the suite's own
// bound would fail, which is how the pace drifted once already.
func WaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	// The bound is generous because delivery through a transport can take
	// seconds on a loaded machine — the cross-replica cases wait on another
	// process's reader — and a case that is genuinely broken still fails, just
	// later.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// AssertConforms runs the whole contract against buses the factory builds.
func AssertConforms(t *testing.T, newBus Factory) {
	t.Helper()

	t.Run("a subscriber receives a published event", func(t *testing.T) {
		b, start := newBus(t)
		var seen atomic.Int32
		b.Subscribe("order.placed", func(context.Context, event.Event) error {
			seen.Add(1)
			return nil
		})
		start()
		if err := b.Publish(context.Background(), event.Event{Type: "order.placed"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		WaitFor(t, "the subscriber to run", func() bool { return seen.Load() == 1 })
	})

	t.Run("matching is by exact type name", func(t *testing.T) {
		b, start := newBus(t)
		var wrong atomic.Int32
		b.Subscribe("order.placed", func(context.Context, event.Event) error {
			wrong.Add(1)
			return nil
		})
		start()
		// Neither a prefix nor a different case may reach it.
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
			b.Subscribe("t", func(context.Context, event.Event) error {
				seen.Add(1)
				return nil
			})
		}
		start()
		if err := b.Publish(context.Background(), event.Event{Type: "t"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		WaitFor(t, "all subscribers to run", func() bool { return seen.Load() == subscribers })
	})

	t.Run("publishing a type nobody subscribed to is accepted", func(t *testing.T) {
		b, start := newBus(t)
		start()
		if err := b.Publish(context.Background(), event.Event{Type: "nobody.listens"}); err != nil {
			t.Fatalf("Publish with no subscriber: %v", err)
		}
	})

	t.Run("the event arrives as it was published", func(t *testing.T) {
		b, start := newBus(t)
		want := event.Event{Type: "t", TenantID: "acme", Payload: map[string]any{"k": "carried"}}
		got := make(chan event.Event, 1)
		b.Subscribe("t", func(_ context.Context, evt event.Event) error {
			got <- evt
			return nil
		})
		start()
		if err := b.Publish(context.Background(), want); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		select {
		case evt := <-got:
			if evt.Type != want.Type || evt.TenantID != want.TenantID {
				t.Fatalf("the event arrived as %+v, want %+v", evt, want)
			}
			// The payload is read through the helpers rather than compared as a
			// value: a transport delivers the decoded shape, not the
			// publisher's own.
			s, ok := event.PayloadString(evt.Payload, "k")
			if !ok || s != "carried" {
				t.Fatalf("reading the payload through its helpers gave (%q, %v)", s, ok)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for the event to arrive")
		}
	})

	t.Run("a subscriber's failure does not reach the publisher", func(t *testing.T) {
		b, start := newBus(t)
		var ran atomic.Int32
		b.Subscribe("t", func(context.Context, event.Event) error {
			ran.Add(1)
			return errors.New("this handler failed on purpose")
		})
		start()
		// The contract says Publish's error is about handing the event over,
		// never about a subscriber. An implementation that knows the result —
		// the process-local one does — must still keep it out of this value,
		// or a publisher cannot tell "not accepted" from "accepted and
		// mishandled".
		if err := b.Publish(context.Background(), event.Event{Type: "t"}); err != nil {
			t.Fatalf("Publish reported a subscriber's failure: %v", err)
		}
		WaitFor(t, "the failing subscriber to run", func() bool { return ran.Load() == 1 })
	})

	t.Run("one subscriber's failure does not stop the others", func(t *testing.T) {
		b, start := newBus(t)
		var after atomic.Int32
		b.Subscribe("t", func(context.Context, event.Event) error {
			return errors.New("this handler fails on purpose")
		})
		b.Subscribe("t", func(context.Context, event.Event) error {
			after.Add(1)
			return nil
		})
		start()
		if err := b.Publish(context.Background(), event.Event{Type: "t"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		WaitFor(t, "the subscriber after the failing one to run",
			func() bool { return after.Load() == 1 })
	})

	t.Run("a cancelled context is honoured", func(t *testing.T) {
		b, start := newBus(t)
		var ran atomic.Int32
		b.Subscribe("t", func(context.Context, event.Event) error {
			ran.Add(1)
			return nil
		})
		start()
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
}
