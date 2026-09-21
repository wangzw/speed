package nats

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/bussuite"
	"github.com/vislake/speed/pkg/event/internal/natstest"
)

// errFailedOnPurpose is what a subscriber returns in the cases that check what
// the publisher is told about it.
var errFailedOnPurpose = errors.New("this handler failed on purpose")

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
	waitFor(t, "the publishing assembly's subscriber to run", func() bool { return gotSecond.Load() == 1 })
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

func TestStanceStandsDownWithoutAServer(t *testing.T) {
	got := stance(&Config{})
	if got.State != core.StateDisabled {
		t.Fatalf("stance with no server = %v, want StateDisabled", got.State)
	}
	if got.Reason == "" {
		t.Fatal("the stand-down carries no reason")
	}
}

func TestStanceEnablesOnceAServerIsNamed(t *testing.T) {
	got := stance(&Config{URL: "nats://127.0.0.1:4222"})
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
	waitFor(t, "the publishing replica's subscriber to run", func() bool { return gotSecond.Load() == 1 })
	waitFor(t, "the other replica's subscriber to run", func() bool { return gotFirst.Load() == 1 })
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
	waitFor(t, "one of the two to take the event", func() bool { return total.Load() >= 1 })
	time.Sleep(500 * time.Millisecond)
	if n := total.Load(); n != 1 {
		t.Fatalf("the two instances handled the event %d times between them, want exactly 1: they "+
			"share a consumer, so JetStream balances the stream between them", n)
	}
}

// TestPublishReportsNothingAboutSubscribers pins the capability's floor against
// a real server.
func TestPublishReportsNothingAboutSubscribers(t *testing.T) {
	url := natstest.URL(t)
	ctx := context.Background()
	eventType := nextType()
	b := openBus(t, url, "")
	var ran atomic.Int32
	b.Subscribe(eventType, func(context.Context, event.Event) error {
		ran.Add(1)
		return errFailedOnPurpose
	})
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := b.Publish(ctx, event.Event{Type: eventType}); err != nil {
		t.Fatalf("Publish reported a subscriber's failure: %v", err)
	}
	waitFor(t, "the failing subscriber to run", func() bool { return ran.Load() == 1 })
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
