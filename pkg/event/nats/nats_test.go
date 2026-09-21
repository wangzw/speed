package nats

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/bussuite"
	"github.com/vislake/speed/pkg/natstest"
)

// eventTypeSerial names each case's event type, so two cases on one server
// cannot see each other's streams.
var eventTypeSerial atomic.Int32

func nextType() string {
	return fmt.Sprintf("case.%d", eventTypeSerial.Add(1))
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
	})
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
	first.Subscribe(eventType, func(context.Context, event.Event) error {
		gotFirst.Add(1)
		return nil
	})
	second.Subscribe(eventType, func(context.Context, event.Event) error {
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
	bussuite.WaitFor(t, "the publishing assembly's subscriber to run", func() bool { return gotSecond.Load() == 1 })
	// Long enough for a delivery that should not happen to have happened.
	time.Sleep(2 * time.Second)
	if n := gotFirst.Load(); n != 0 {
		t.Fatalf("the other assembly's subscriber ran %d times: the two share a stream, so each is "+
			"receiving the other's events", n)
	}
}

// TestANamespaceASubjectCannotCarryIsRefused covers the other half: a namespace
// that would silently widen or move the subject space fails at open rather than
// later, when an assembly is receiving strangers' events.
func TestANamespaceASubjectCannotCarryIsRefused(t *testing.T) {
	for _, bad := range []string{"has.dot", "has space", "has*wildcard", "has>wildcard", "has\ttab"} {
		if err := checkNamespace(bad); err == nil {
			t.Errorf("checkNamespace(%q) accepted it, and a subject token may not hold a dot, "+
				"whitespace or a wildcard", bad)
		}
	}
	for _, good := range []string{"speed_events", "prod-1", "assemblyOne"} {
		if err := checkNamespace(good); err != nil {
			t.Errorf("checkNamespace(%q) refused it: %v", good, err)
		}
	}
}

func TestConforms(t *testing.T) {
	url := natstest.URL(t)
	bussuite.AssertConforms(t, func(t *testing.T) (event.Bus, func()) {
		b := openBus(t, url, "")
		return b, func() {
			if err := b.begin(context.Background()); err != nil {
				t.Fatalf("begin: %v", err)
			}
		}
	})
}

// TestStanceFailsWithoutAServer pins the enablement rule at this backend's own
// precondition: a module taken in at run level has to start, so an implementation
// the host did not point at a server fails the startup naming the item. It is not
// a stance that lets the run continue without an event bus.
func TestStanceFailsWithoutAServer(t *testing.T) {
	got, err := stance(&Config{})
	if err == nil {
		t.Fatal("stance with no server did not fail the startup")
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+urlKey) {
		t.Fatalf("the failure does not name the input that caused it: %v", err)
	}
	if got.State == core.StateDisabled {
		t.Fatal("the stance stands this module down, and StateDisabled is not a stance a module " +
			"may state: resolution is its only source")
	}
}

// TestStanceTreatsABlankServerAsNone covers the case an equality check on the
// raw setting misses: a URL that is only whitespace is not a server, and the
// stance is what turns it into a startup failure naming the input rather than a
// connect refusal at the first publish.
func TestStanceTreatsABlankServerAsNone(t *testing.T) {
	for _, url := range []string{"", " ", "\t", "\n"} {
		_, err := stance(&Config{URL: url})
		if err == nil {
			t.Errorf("stance with url %q started anyway", url)
			continue
		}
		if want := ConfigNamespace + "." + urlKey; !strings.Contains(err.Error(), want) {
			t.Errorf("the error for url %q does not name %q: %v", url, want, err)
		}
	}
}

func TestStanceEnablesOnceAServerIsNamed(t *testing.T) {
	got, err := stance(&Config{URL: "nats://127.0.0.1:4222"})
	if err != nil {
		t.Fatalf("stance with a server failed the startup: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("stance with a server = %v, want StateEnabled", got.State)
	}
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
	first.Subscribe(eventType, func(context.Context, event.Event) error {
		gotFirst.Add(1)
		return nil
	})
	second.Subscribe(eventType, func(context.Context, event.Event) error {
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
		b.Subscribe(eventType, func(context.Context, event.Event) error {
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
	bussuite.WaitFor(t, "one of the two to take the event", func() bool { return total.Load() >= 1 })
	time.Sleep(500 * time.Millisecond)
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

// TestReadRecoversWhenTheConsumerAppearsLate pins the retry around taking the
// consumer: a reader that gives up there leaves its event type undelivered for
// the rest of the process's life on the strength of one transient failure, and
// only a single startup log line says so — while Publish keeps returning nil,
// because the event really was written to the stream.
func TestReadRecoversWhenTheConsumerAppearsLate(t *testing.T) {
	url := natstest.URL(t)
	ctx := context.Background()
	eventType := nextType()
	b := openBus(t, url, "")
	var seen atomic.Int32
	b.Subscribe(eventType, func(context.Context, event.Event) error {
		seen.Add(1)
		return nil
	})

	// The stream exists and the consumer does not: the state a reader lands in
	// when the take fails transiently. begin is not called — its consumer
	// creation is exactly what this case withholds — so the fields it would
	// set are set here.
	stream := b.streamName(eventType)
	if err := b.ensureStream(ctx, eventType); err != nil {
		t.Fatalf("ensureStream: %v", err)
	}
	b.readCtx, b.cancel = context.WithCancel(ctx)
	b.readers.Add(1)
	go b.read(stream, eventType)

	// The consumer appears only now. A reader that gave up on the first take
	// has already returned and will never deliver; one that retries picks it
	// up on a later attempt.
	time.Sleep(300 * time.Millisecond)
	if _, err := b.js.CreateOrUpdateConsumer(ctx, stream, jetstream.ConsumerConfig{
		Durable:       b.consumer,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
	}); err != nil {
		t.Fatalf("creating the consumer: %v", err)
	}

	if err := b.Publish(ctx, event.Event{Type: eventType}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	bussuite.WaitFor(t, "the reader to recover and deliver the event",
		func() bool { return seen.Load() == 1 })
}
