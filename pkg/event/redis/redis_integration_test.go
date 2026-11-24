//go:build integration

package redis

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/bussuite"
	"github.com/vislake/speed/pkg/event/internal/tracing"
	speedredis "github.com/vislake/speed/pkg/internal/redis"
	"github.com/vislake/speed/pkg/internal/redis/redistest"
	"github.com/vislake/speed/pkg/log"
)

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

// TestConforms runs the bus contract suite against the Redis backend.
func TestConforms(t *testing.T) {
	addr, db := redistest.Acquire(t)
	bussuite.AssertConforms(t, factoryOn(addr, db))
}

// TestReplicated runs the cross-replica contract against the Redis backend.
func TestReplicated(t *testing.T) {
	addr, db := redistest.Acquire(t)
	bussuite.AssertReplicated(t, factoryOn(addr, db))
}

// TestEveryReplicaReceivesEveryEvent is what this backend is for, and
// what a consumer-group name of its own buys.
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

// TestTwoInstancesSharingAGroupSplitTheEvents is the other side of the
// same fact, and it is asserted rather than assumed.
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
	// The group is the source of the delivery: once it has read every entry
	// in the stream and has none still pending, nothing is left for either
	// instance to deliver.
	bussuite.WaitFor(t, "the shared group to drain the stream",
		func() bool { return groupDrained(t, ctx, first, shared, streamKey("t")) })
	if n := total.Load(); n != 1 {
		t.Fatalf("the two instances handled the event %d times between them, want exactly 1: they "+
			"share a group, so Redis balances the stream between them", n)
	}
}

func groupDrained(t *testing.T, ctx context.Context, b *bus, group, stream string) bool {
	t.Helper()
	groups, err := b.client.XInfoGroups(ctx, stream).Result()
	if err != nil {
		t.Fatalf("reading the consumer groups of %s: %v", stream, err)
	}
	for _, g := range groups {
		if g.Name == group {
			return g.Lag == 0 && g.Pending == 0
		}
	}
	return false
}

// TestAnUnserializablePayloadIsRefusedBeforeItReachesTheStream covers the
// other half of the floor.
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

// TestReadRecoversWhenTheGroupIsGone pins the recovery from a vanished
// stream or group.
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
