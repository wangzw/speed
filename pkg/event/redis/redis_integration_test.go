//go:build integration

package redis

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/bussuite"
	"github.com/vislake/speed/pkg/event/internal/tracing"
	speedredis "github.com/vislake/speed/pkg/internal/redis"
	"github.com/vislake/speed/pkg/internal/redis/redistest"
	"github.com/vislake/speed/pkg/log"
)

// openBus builds a bus over the given server, with a consumer group name of its
// own unless one is given.
func openBus(t *testing.T, addr string, db int, group string) *bus {
	t.Helper()
	proto := Config{Config: speedredis.Defaults()}
	proto.Addr, proto.DB, proto.ConsumerGroup = addr, db, group
	opened, err := open(context.Background(), &proto, log.Default(), tracing.Noop())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	local := opened.(*bus)
	t.Cleanup(func() { _ = local.Close(context.Background()) })
	return local
}

// factoryOn builds buses over one server and database, each with a consumer
// group of its own: replicas of one assembly.
func factoryOn(addr string, db int) bussuite.Factory {
	return func(t *testing.T) (event.Bus, func(context.Context)) {
		b := openBus(t, addr, db, "")
		return b, func(ctx context.Context) {
			if err := b.begin(ctx); err != nil {
				t.Fatalf("begin: %v", err)
			}
		}
	}
}

func TestConforms(t *testing.T) {
	addr, db := redistest.Acquire(t)
	bussuite.AssertConforms(t, factoryOn(addr, db))
}

func TestReplicated(t *testing.T) {
	addr, db := redistest.Acquire(t)
	bussuite.AssertReplicated(t, factoryOn(addr, db))
}

// TestEveryReplicaReceivesEveryEvent is what this backend is for, and what a
// consumer-group name of its own buys: Redis balances a stream between the
// consumers of one group, so two instances sharing a name would split the
// events rather than both seeing them.
func TestEveryReplicaReceivesEveryEvent(t *testing.T) {
	addr, db := redistest.Acquire(t)
	ctx := context.Background()
	first := openBus(t, addr, db, "")
	second := openBus(t, addr, db, "")

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

	// Published through the second, so the first can only have seen it by
	// reading the stream: nothing in this process hands an event to another
	// instance's subscribers.
	if err := second.Publish(ctx, event.Event{Type: "order.placed"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	bussuite.WaitFor(t, "the publishing replica's subscriber to run", func() bool { return gotSecond.Load() == 1 })
	bussuite.WaitFor(t, "the other replica's subscriber to run", func() bool { return gotFirst.Load() == 1 })
}

// TestTwoInstancesSharingAGroupSplitTheEvents is the other side of the same
// fact, and it is asserted rather than assumed: it is what makes the default
// group name load-bearing. A host that names one group for both replicas has
// asked for a load balance, and this is what it gets.
func TestTwoInstancesSharingAGroupSplitTheEvents(t *testing.T) {
	addr, db := redistest.Acquire(t)
	ctx := context.Background()
	const shared = "one-group-for-both"
	first := openBus(t, addr, db, shared)
	second := openBus(t, addr, db, shared)

	var total atomic.Int32
	for _, b := range []*bus{first, second} {
		b.Subscribe("t", "test", func(context.Context, event.Event) error {
			total.Add(1)
			return nil
		})
	}
	if err := first.begin(ctx); err != nil {
		t.Fatalf("begin on the first: %v", err)
	}
	if err := second.begin(ctx); err != nil {
		t.Fatalf("begin on the second: %v", err)
	}
	if err := first.Publish(ctx, event.Event{Type: "t"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	bussuite.WaitFor(t, "one of the two to take the event", func() bool { return total.Load() >= 1 })
	// The event is delivered once between them, not once each.
	time.Sleep(300 * time.Millisecond)
	if n := total.Load(); n != 1 {
		t.Fatalf("the two instances handled the event %d times between them, want exactly 1: they "+
			"share a group, so Redis balances the stream between them", n)
	}
}

// TestAnUnserializablePayloadIsRefusedBeforeItReachesTheStream covers the other
// half of the floor: a publish that could not be handed over fails, and fails
// here rather than on a reader that would have to drop it silently.
func TestAnUnserializablePayloadIsRefusedBeforeItReachesTheStream(t *testing.T) {
	addr, db := redistest.Acquire(t)
	ctx := context.Background()
	b := openBus(t, addr, db, "")
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := b.Publish(ctx, event.Event{Type: "t", Payload: make(chan int)}); err == nil {
		t.Fatal("Publish accepted a payload that cannot be serialized")
	}
}

// TestAClosedBusRefusesPublishes pins that the refusal is this implementation's
// own: a publish after Close reports it rather than reaching nobody.
func TestAClosedBusRefusesPublishes(t *testing.T) {
	addr, db := redistest.Acquire(t)
	ctx := context.Background()
	b := openBus(t, addr, db, "")
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

// TestReadRecoversWhenTheGroupIsGone pins the recovery from a vanished stream
// or group: an operator's XGROUP DESTROY, a flush, or an eviction under memory
// pressure answers every read with NOGROUP, and a reader that does not recreate
// the group delivers nothing for this type for the rest of the process's life —
// while Publish keeps returning nil.
func TestReadRecoversWhenTheGroupIsGone(t *testing.T) {
	addr, db := redistest.Acquire(t)
	ctx := context.Background()
	b := openBus(t, addr, db, "")
	var seen atomic.Int32
	b.Subscribe("t", "test", func(context.Context, event.Event) error {
		seen.Add(1)
		return nil
	})
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}

	if err := b.client.XGroupDestroy(ctx, streamKey("t"), b.group).Err(); err != nil {
		t.Fatalf("destroying the consumer group: %v", err)
	}

	// It is the reader that recreates the group, not this case: waiting for
	// the group to come back is what shows the recovery happened at all.
	bussuite.WaitFor(t, "the reader to recreate the consumer group", func() bool {
		groups, err := b.client.XInfoGroups(ctx, streamKey("t")).Result()
		if err != nil {
			return false
		}
		for _, g := range groups {
			if g.Name == b.group {
				return true
			}
		}
		return false
	})

	if err := b.Publish(ctx, event.Event{Type: "t"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	bussuite.WaitFor(t, "the event to arrive after the group came back",
		func() bool { return seen.Load() == 1 })
}
