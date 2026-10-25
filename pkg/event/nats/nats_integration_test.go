//go:build integration

package nats

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/bussuite"
	"github.com/vislake/speed/pkg/event/internal/tracing"
	"github.com/vislake/speed/pkg/internal/natstest"
	"github.com/vislake/speed/pkg/log"
)

// eventTypeSerial names each case's event type, so two cases on one server
// cannot see each other's streams.
var eventTypeSerial atomic.Int32

// runToken keeps those names apart across test processes as well. A stream
// outlives the process that made it on the shared server, and so does a durable
// consumer with a fixed name: a type named by a counter alone is the same type
// in the next run, or in another checkout running at the same moment, and a
// case sharing a consumer would be handed every event that earlier run left in
// the stream.
var runToken = func() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}()

func nextType() string {
	return fmt.Sprintf("case.%s.%d", runToken, eventTypeSerial.Add(1))
}

// openBus builds a bus over the shared server, with a consumer name of its own
// unless one is given.
func openBus(t *testing.T, url string, name string) *bus {
	t.Helper()
	return openBusIn(t, url, name, "")
}

// openBusIn is openBus with the namespace named too.
func openBusIn(t *testing.T, url string, name string, namespace string) *bus {
	t.Helper()
	opened, err := open(context.Background(), &Config{
		URL: url, ConsumerName: name, Namespace: namespace, Timeout: 10 * time.Second,
	}, log.Default(), tracing.Noop())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	local := opened.(*bus)
	t.Cleanup(func() { _ = local.Close(context.Background()) })
	return local
}

// TestTwoAssembliesOnOneServerDoNotSeeEachOther covers what the namespace is
// for. Everything about these two buses is identical — same server, same event
// type, a consumer name of its own each — and the only thing keeping them apart
// is the namespace they were given.
func TestTwoAssembliesOnOneServerDoNotSeeEachOther(t *testing.T) {
	url := natstest.URL(t)
	ctx := context.Background()
	eventType := nextType()
	first := openBusIn(t, url, "", "assembly_one")
	second := openBusIn(t, url, "", "assembly_two")

	var gotFirst, gotSecond atomic.Int32
	first.Subscribe(eventType, "test", func(context.Context, event.Event) error {
		gotFirst.Add(1)
		return nil
	})
	second.Subscribe(eventType, "test", func(context.Context, event.Event) error {
		gotSecond.Add(1)
		return nil
	})
	if err := first.begin(ctx); err != nil {
		t.Fatalf("begin on the first: %v", err)
	}
	if err := second.begin(ctx); err != nil {
		t.Fatalf("begin on the second: %v", err)
	}

	if err := second.Publish(ctx, event.Event{Type: eventType}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	js := inspect(t, url)
	bussuite.WaitFor(t, "the publishing assembly's subscriber to run", func() bool { return gotSecond.Load() == 1 })
	// The first assembly's own consumer is the source of its deliveries: once it
	// has taken and acknowledged everything its stream holds, anything it was
	// going to deliver has arrived. On a stream of its own that stream is empty;
	// a bus that read the other assembly's stream would instead have the event
	// to deliver before this condition holds.
	bussuite.WaitFor(t, "the receiving assembly's consumer to drain its stream",
		func() bool { return consumerDrained(t, js, first.streamName(eventType), first.consumer) })
	if n := gotFirst.Load(); n != 0 {
		t.Fatalf("the other assembly's subscriber ran %d times: the two share a stream, so each is "+
			"receiving the other's events", n)
	}
}

// factoryOn builds buses over one server, each with a durable consumer of its
// own: replicas of one assembly.
//
// They share a namespace of this test process's own. The suite names its
// event types by hand, the same names in every run, and the server is shared
// with every other checkout running the suite at the same moment: in the
// default namespace, each of those runs would publish onto the streams this
// one reads, and a case counting deliveries would count theirs.
func factoryOn(url string) bussuite.Factory {
	namespace := "suite_" + runToken
	return func(t *testing.T) (event.Bus, func(context.Context)) {
		b := openBusIn(t, url, "", namespace)
		return b, func(ctx context.Context) {
			if err := b.begin(ctx); err != nil {
				t.Fatalf("begin: %v", err)
			}
		}
	}
}

func TestConforms(t *testing.T) {
	bussuite.AssertConforms(t, factoryOn(natstest.URL(t)))
}

func TestReplicated(t *testing.T) {
	bussuite.AssertReplicated(t, factoryOn(natstest.URL(t)))
}

// TestEveryReplicaReceivesEveryEvent is what this backend is for, and what a
// consumer name of its own buys: JetStream balances a stream between consumers
// sharing a name, so two instances sharing one would split the events.
//
// The event is published through the second bus, so the first can only have
// seen it by reading the stream.
func TestEveryReplicaReceivesEveryEvent(t *testing.T) {
	url := natstest.URL(t)
	ctx := context.Background()
	eventType := nextType()
	first := openBus(t, url, "")
	second := openBus(t, url, "")

	var gotFirst, gotSecond atomic.Int32
	first.Subscribe(eventType, "test", func(context.Context, event.Event) error {
		gotFirst.Add(1)
		return nil
	})
	second.Subscribe(eventType, "test", func(context.Context, event.Event) error {
		gotSecond.Add(1)
		return nil
	})
	if err := first.begin(ctx); err != nil {
		t.Fatalf("begin on the first: %v", err)
	}
	if err := second.begin(ctx); err != nil {
		t.Fatalf("begin on the second: %v", err)
	}

	if err := second.Publish(ctx, event.Event{Type: eventType}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	bussuite.WaitFor(t, "the publishing replica's subscriber to run", func() bool { return gotSecond.Load() == 1 })
	bussuite.WaitFor(t, "the other replica's subscriber to run", func() bool { return gotFirst.Load() == 1 })
}

// TestTwoInstancesSharingAConsumerSplitTheEvents is the other side of the same
// fact, asserted rather than assumed: it is what makes the default name
// load-bearing.
func TestTwoInstancesSharingAConsumerSplitTheEvents(t *testing.T) {
	url := natstest.URL(t)
	ctx := context.Background()
	eventType := nextType()
	const shared = "one_consumer_for_both"
	first := openBus(t, url, shared)
	second := openBus(t, url, shared)

	var total atomic.Int32
	for _, b := range []*bus{first, second} {
		b.Subscribe(eventType, "test", func(context.Context, event.Event) error {
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
	if err := first.Publish(ctx, event.Event{Type: eventType}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	js := inspect(t, url)
	bussuite.WaitFor(t, "one of the two to take the event", func() bool { return total.Load() >= 1 })
	// The shared consumer is the source of these deliveries: once it has taken
	// and acknowledged everything the stream holds, no further delivery is
	// coming, so the count taken afterwards is final. If the two instances were
	// each handed the event, both acknowledgements would already be in.
	bussuite.WaitFor(t, "the shared consumer to drain the stream",
		func() bool { return consumerDrained(t, js, first.streamName(eventType), shared) })
	if n := total.Load(); n != 1 {
		t.Fatalf("the two instances handled the event %d times between them, want exactly 1: they "+
			"share a consumer, so JetStream balances the stream between them", n)
	}
}

func TestAnUnserializablePayloadIsRefused(t *testing.T) {
	url := natstest.URL(t)
	ctx := context.Background()
	b := openBus(t, url, "")
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := b.Publish(ctx, event.Event{Type: nextType(), Payload: make(chan int)}); err == nil {
		t.Fatal("Publish accepted a payload that cannot be serialized")
	}
}

func TestAClosedBusRefusesPublishes(t *testing.T) {
	url := natstest.URL(t)
	ctx := context.Background()
	b := openBus(t, url, "")
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := b.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := b.Publish(ctx, event.Event{Type: nextType()}); !errors.Is(err, event.ErrClosed) {
		t.Fatalf("Publish after Close: %v, want ErrClosed", err)
	}
}

// TestAReaderOpensWhatIsMissing pins that a reader does not depend on begin
// having left its stream and durable in place: it opens both itself, and
// again whenever it finds them gone. A reader that only took an existing
// durable would, on the first one missing, leave its event type undelivered
// for the rest of the process's life while Publish kept returning nil.
func TestAReaderOpensWhatIsMissing(t *testing.T) {
	url := natstest.URL(t)
	ctx := context.Background()
	eventType := nextType()
	b := openBus(t, url, "")
	var seen atomic.Int32
	b.Subscribe(eventType, "test", func(context.Context, event.Event) error {
		seen.Add(1)
		return nil
	})

	// Neither the stream nor the durable exists. begin is not called — what
	// it creates is exactly what this case withholds — so what it does
	// besides is done here.
	b.table.Freeze(ctx)
	b.table.Go(func(reading, handling context.Context) { b.read(reading, handling, eventType) })

	js := inspect(t, url)
	bussuite.WaitFor(t, "the reader to open its durable", func() bool {
		_, ok := consumerOf(t, js, b.streamName(eventType), b.consumer)
		return ok
	})
	if err := b.Publish(ctx, event.Event{Type: eventType}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	bussuite.WaitFor(t, "the reader to deliver the event", func() bool { return seen.Load() == 1 })
}
