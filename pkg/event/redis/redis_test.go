package redis

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
	speedredis "github.com/vislake/speed/pkg/redis"
	"github.com/vislake/speed/pkg/redis/redistest"
)

// errFailedOnPurpose is what a subscriber returns in the cases that check what
// the publisher is told about it.
var errFailedOnPurpose = errors.New("this handler failed on purpose")

// openBus builds a bus over the given server, with a consumer group name of its
// own unless one is given.
func openBus(t *testing.T, addr string, db int, group string) *bus {
	t.Helper()
	proto := Config{Config: speedredis.Defaults()}
	proto.Addr, proto.DB, proto.ConsumerGroup = addr, db, group
	opened, err := open(context.Background(), &proto)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	local := opened.(*bus)
	t.Cleanup(func() { _ = local.Close(context.Background()) })
	return local
}

// waitFor polls until cond holds, and fails the case if it never does. Delivery
// here happens on the reader goroutines, after Publish has returned, so every
// assertion about it waits.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestConforms(t *testing.T) {
	addr, db := redistest.Acquire(t)
	bussuite.AssertConforms(t, func(t *testing.T) (event.Bus, func()) {
		b := openBus(t, addr, db, "")
		return b, func() {
			if err := b.begin(context.Background()); err != nil {
				t.Fatalf("begin: %v", err)
			}
		}
	})
}

func TestStanceStandsDownWithoutAServer(t *testing.T) {
	cfg := Config{Config: speedredis.Defaults()}
	got := stance(&cfg)
	if got.State != core.StateDisabled {
		t.Fatalf("stance with no address = %v, want StateDisabled", got.State)
	}
	if got.Reason == "" {
		t.Fatal("the stand-down carries no reason")
	}
}

func TestStanceEnablesOnceAServerIsNamed(t *testing.T) {
	cfg := Config{Config: speedredis.Defaults()}
	cfg.Addr = "127.0.0.1:6379"
	got := stance(&cfg)
	if got.State != core.StateEnabled {
		t.Fatalf("stance with an address = %v, want StateEnabled", got.State)
	}
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

	// Published through the second, so the first can only have seen it by
	// reading the stream: nothing in this process hands an event to another
	// instance's subscribers.
	if err := second.Publish(ctx, event.Event{Type: "order.placed"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the publishing replica's subscriber to run", func() bool { return gotSecond.Load() == 1 })
	waitFor(t, "the other replica's subscriber to run", func() bool { return gotFirst.Load() == 1 })
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
		b.Subscribe("t", func(context.Context, event.Event) error {
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
	waitFor(t, "one of the two to take the event", func() bool { return total.Load() >= 1 })
	// The event is delivered once between them, not once each.
	time.Sleep(300 * time.Millisecond)
	if n := total.Load(); n != 1 {
		t.Fatalf("the two instances handled the event %d times between them, want exactly 1: they "+
			"share a group, so Redis balances the stream between them", n)
	}
}

// TestPublishReportsNothingAboutSubscribers pins the capability's floor against
// a real transport: a publish whose subscriber fails still returns nil, because
// the failure happened on a reader goroutine after this call had returned.
func TestPublishReportsNothingAboutSubscribers(t *testing.T) {
	addr, db := redistest.Acquire(t)
	ctx := context.Background()
	b := openBus(t, addr, db, "")
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

// TestStreamsAreNamespaced keeps this implementation out of everything else on
// a server it may be sharing.
func TestStreamsAreNamespaced(t *testing.T) {
	if got := streamKey("order.placed"); !strings.HasPrefix(got, streamPrefix) {
		t.Fatalf("streamKey returned %q, which is not under %q", got, streamPrefix)
	}
}
