package memory

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/bussuite"
	"github.com/vislake/speed/pkg/event/internal/tracing"
	"github.com/vislake/speed/pkg/log"
)

func openBus(t *testing.T) event.Bus {
	t.Helper()
	b, err := open(context.Background(), &Config{Replicas: 1}, log.Default(), tracing.Noop())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return b
}

// TestConforms runs the shared contract.
func TestConforms(t *testing.T) {
	bussuite.AssertConforms(t, func(t *testing.T) (event.Bus, func(context.Context)) {
		b := openBus(t)
		// This bus delivers in-process, so putting it into service is only the
		// freeze the drive performs at its start stage.
		return b, func(ctx context.Context) {
			if err := start(ctx, b); err != nil {
				t.Fatalf("start: %v", err)
			}
		}
	})
}

// TestStanceIsAutoWhenOneReplicaSharesTheState checks that one replica states
// StateAuto.
func TestStanceIsAutoWhenOneReplicaSharesTheState(t *testing.T) {
	got, err := stance(&Config{Replicas: 1})
	if err != nil {
		t.Fatalf("stance with one replica failed the startup: %v", err)
	}
	if got.State != core.StateAuto {
		t.Fatalf("stance with one replica = %v, want StateAuto: this implementation is the "+
			"capability's zero-configuration default and has to stand down for an explicitly "+
			"enabled backend", got.State)
	}
}

// TestStanceFailsTheStartupWhenSeveralReplicasShareTheState is the half
// of the enablement rule this module owns.
func TestStanceFailsTheStartupWhenSeveralReplicasShareTheState(t *testing.T) {
	_, err := stance(&Config{Replicas: 2})
	if err == nil {
		t.Fatal("stance with two replicas did not fail the startup: a subscriber on one replica " +
			"would never see an event published on another, while every publish still returned " +
			"success")
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+replicasKey) {
		t.Fatalf("the failure does not name the input that caused it: %v", err)
	}
}

// TestPublishAfterCloseIsRefused covers the one failure this
// implementation adds to the hand-over step.
func TestPublishAfterCloseIsRefused(t *testing.T) {
	b := openBus(t)
	var ran atomic.Int32
	b.Subscribe("t", "test", func(context.Context, event.Event) error {
		ran.Add(1)
		return nil
	})
	if err := b.(*bus).Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := b.Publish(context.Background(), event.Event{Type: "t"}); !errors.Is(err, event.ErrClosed) {
		t.Fatalf("Publish after Close: %v, want ErrClosed", err)
	}
	if n := ran.Load(); n != 0 {
		t.Fatalf("a subscriber ran %d times for a publish the bus refused", n)
	}
}

// TestStartFreezesTheSubscriptionTable covers the stage this
// implementation adds.
func TestStartFreezesTheSubscriptionTable(t *testing.T) {
	b := openBus(t)
	b.Subscribe("before", "test", func(context.Context, event.Event) error { return nil })
	if err := start(context.Background(), b); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Subscribe after the start stage did not panic: a subscription registered that " +
				"late would silently miss everything published before it")
		}
	}()
	b.Subscribe("after", "test", func(context.Context, event.Event) error { return nil })
}

// TestSubscribeWithNoSubscriberNamePanics covers the other refusal at the
// call site.
func TestSubscribeWithNoSubscriberNamePanics(t *testing.T) {
	b := openBus(t)
	defer func() {
		if recover() == nil {
			t.Fatal("Subscribe with no subscriber name did not panic: the name is what the span of " +
				"every delivery to this subscriber is recorded under, and leaving it empty would " +
				"leave those spans unnamed with nothing to say so")
		}
	}()
	b.Subscribe("t", "", func(context.Context, event.Event) error { return nil })
}

// TestPublishRunsHandlersBeforeReturning pins that handlers finish before
// Publish returns, an addition over the transport-backed floor.
func TestPublishRunsHandlersBeforeReturning(t *testing.T) {
	b := openBus(t)
	var ran atomic.Int32
	b.Subscribe("t", "test", func(context.Context, event.Event) error {
		ran.Add(1)
		return nil
	})
	if err := start(context.Background(), b); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := b.Publish(context.Background(), event.Event{Type: "t"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if n := ran.Load(); n != 1 {
		t.Fatalf("the subscriber had run %d times by the time Publish returned, want 1", n)
	}
}

type recordedDelivery struct {
	name   string
	parent trace.SpanContext
	attrs  map[string]string
	endErr error
}

type recordingTracer struct {
	mu    sync.Mutex
	spans []recordedDelivery
}

func (r *recordingTracer) Start(ctx context.Context, name string, attrs map[string]string) (context.Context, event.Span) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans = append(r.spans, recordedDelivery{
		name:   name,
		parent: trace.SpanContextFromContext(ctx),
		attrs:  attrs,
	})
	i := len(r.spans) - 1
	return ctx, endFunc(func(err error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.spans[i].endErr = err
	})
}

func (r *recordingTracer) only(t *testing.T) recordedDelivery {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.spans) != 1 {
		t.Fatalf("the dispatch layer started %d spans for one delivery, want 1", len(r.spans))
	}
	return r.spans[0]
}

type endFunc func(error)

func (f endFunc) End(err error) { f(err) }

func traceOf(traceID trace.TraceID, spanID trace.SpanID) trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	})
}

// TestASubscribersSpanContinuesThePublishersTrace checks that a subscriber's
// span continues the publisher's trace.
func TestASubscribersSpanContinuesThePublishersTrace(t *testing.T) {
	rec := &recordingTracer{}
	b, err := open(context.Background(), &Config{Replicas: 1}, log.Default(), rec)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var ran atomic.Int32
	var deliveredID string
	b.Subscribe("t", "memory.subscriber", func(_ context.Context, evt event.Event) error {
		ran.Add(1)
		deliveredID = evt.ID
		return nil
	})
	if err := start(context.Background(), b); err != nil {
		t.Fatalf("start: %v", err)
	}
	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	spanID := trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}
	publishCtx := trace.ContextWithSpanContext(context.Background(), traceOf(traceID, spanID))
	if err := b.Publish(publishCtx, event.Event{Type: "t"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if ran.Load() != 1 {
		t.Fatal("the subscriber did not run")
	}
	got := rec.only(t)
	if got.name != "memory.subscriber" {
		t.Fatalf("the delivery's span is named %q, want the subscriber's own name", got.name)
	}
	if got.parent.TraceID() != traceID || got.parent.SpanID() != spanID {
		t.Fatalf("the delivery's span is parented on %s/%s, want the publisher's span %s/%s: a "+
			"subscriber's span is a child of the span the event carries, so one trace runs from "+
			"where the fact was produced into every subscriber",
			got.parent.TraceID(), got.parent.SpanID(), traceID, spanID)
	}
	if got.attrs["messaging.destination.name"] != "t" {
		t.Fatalf("messaging.destination.name is %q, want the event type", got.attrs["messaging.destination.name"])
	}
	if got.attrs["messaging.operation"] != "process" {
		t.Fatalf("messaging.operation is %q, want process", got.attrs["messaging.operation"])
	}
	if got.attrs["messaging.system"] != "memory" {
		t.Fatalf("messaging.system is %q, want the backend name memory", got.attrs["messaging.system"])
	}
	if got.attrs["messaging.message.id"] != deliveredID || deliveredID == "" {
		t.Fatalf("messaging.message.id is %q, want the identifier %q the event was assigned and the "+
			"subscriber received", got.attrs["messaging.message.id"], deliveredID)
	}
	if got.endErr != nil {
		t.Fatalf("the delivery's span ended with %v, want no error for a handler that returned nil", got.endErr)
	}
}

// TestAFailedDeliveryEndsItsSpanWithTheError pins the failure half of a
// delivery's span.
func TestAFailedDeliveryEndsItsSpanWithTheError(t *testing.T) {
	sentinel := errors.New("this handler failed on purpose")
	cases := []struct {
		name    string
		handler event.Handler
		want    func(t *testing.T, err error)
	}{
		{
			name:    "returns an error",
			handler: func(context.Context, event.Event) error { return sentinel },
			want: func(t *testing.T, err error) {
				t.Helper()
				if !errors.Is(err, sentinel) {
					t.Fatalf("the span was ended with %v, want the error the subscriber returned", err)
				}
			},
		},
		{
			name:    "panics",
			handler: func(context.Context, event.Event) error { panic("this handler panics on purpose") },
			want: func(t *testing.T, err error) {
				t.Helper()
				if !strings.Contains(err.Error(), "panicked") {
					t.Fatalf("the span was ended with %v, want the recovered panic reported as a failure", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingTracer{}
			b, err := open(context.Background(), &Config{Replicas: 1}, log.Default(), rec)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			b.Subscribe("t", "memory.subscriber", tc.handler)
			if err := start(context.Background(), b); err != nil {
				t.Fatalf("start: %v", err)
			}
			if err := b.Publish(context.Background(), event.Event{Type: "t"}); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			got := rec.only(t)
			if got.endErr == nil {
				t.Fatal("the span of a delivery whose subscriber failed was ended with no error: a " +
					"failed delivery is the very thing the span is recorded to show")
			}
			tc.want(t, got.endErr)
		})
	}
}

// TestTheEventsTracingContextParentsTheSpanNotTheCallbacks pins which
// context the parent comes from.
func TestTheEventsTracingContextParentsTheSpanNotTheCallbacks(t *testing.T) {
	callTrace := trace.TraceID{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9}
	callSpan := trace.SpanID{9, 9, 9, 9, 9, 9, 9, 9}
	callCtx := trace.ContextWithSpanContext(context.Background(), traceOf(callTrace, callSpan))

	// newDeliveringBus builds a frozen bus whose delivery spans are recorded,
	// without Publish's stamping filling the tracing context first.
	newDeliveringBus := func(t *testing.T) (*recordingTracer, *bus) {
		t.Helper()
		rec := &recordingTracer{}
		b, err := open(context.Background(), &Config{Replicas: 1}, log.Default(), rec)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		b.Subscribe("t", "memory.subscriber", func(context.Context, event.Event) error { return nil })
		local := b.(*bus)
		if _, first := local.table.Freeze(context.Background()); !first {
			t.Fatal("the subscription table did not freeze")
		}
		return rec, local
	}

	t.Run("the event's tracing context is the parent", func(t *testing.T) {
		rec, local := newDeliveringBus(t)
		wantTrace := trace.TraceID{16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
		wantSpan := trace.SpanID{8, 7, 6, 5, 4, 3, 2, 1}
		carrier := propagation.MapCarrier{}
		tracing.Propagator().Inject(
			trace.ContextWithSpanContext(context.Background(), traceOf(wantTrace, wantSpan)), carrier)
		local.table.Deliver(callCtx, event.Event{Type: "t", TraceContext: map[string]string(carrier)})
		got := rec.only(t)
		if got.parent.TraceID() != wantTrace || got.parent.SpanID() != wantSpan {
			t.Fatalf("the delivery's span is parented on %s/%s, want the event's tracing context %s/%s, "+
				"not the span the call itself is under %s/%s",
				got.parent.TraceID(), got.parent.SpanID(), wantTrace, wantSpan, callTrace, callSpan)
		}
	})

	t.Run("an event carrying no tracing context leaves a root", func(t *testing.T) {
		rec, local := newDeliveringBus(t)
		local.table.Deliver(callCtx, event.Event{Type: "t"})
		got := rec.only(t)
		if got.parent.IsValid() {
			t.Fatalf("a delivery whose event carries no tracing context is parented on %s/%s, want a "+
				"root: the parent comes from the event, not from the context the delivery is made "+
				"under", got.parent.TraceID(), got.parent.SpanID())
		}
	})
}

// TestTheDeliverySpanCarriesExactlyTheFixedAttributes checks the attributes a
// delivery's span carries.
func TestTheDeliverySpanCarriesExactlyTheFixedAttributes(t *testing.T) {
	rec := &recordingTracer{}
	b, err := open(context.Background(), &Config{Replicas: 1}, log.Default(), rec)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	b.Subscribe("t", "memory.subscriber", func(context.Context, event.Event) error { return nil })
	if err := start(context.Background(), b); err != nil {
		t.Fatalf("start: %v", err)
	}
	// The event carries a tenant, which the span must not record.
	if err := b.Publish(context.Background(), event.Event{Type: "t", TenantID: "tenant-9"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := rec.only(t)

	fixed := []string{
		"messaging.destination.name",
		"messaging.message.id",
		"messaging.operation",
		"messaging.system",
	}
	allowed := make(map[string]bool, len(fixed))
	for _, k := range fixed {
		allowed[k] = true
	}
	for k := range got.attrs {
		if !allowed[k] {
			t.Fatalf("the delivery's span carries the attribute %q, which the design does not fix: "+
				"the attributes are exactly %v. The tenant in particular is left off, its values "+
				"being as many as there are tenants", k, fixed)
		}
	}
	for _, k := range fixed {
		if _, ok := got.attrs[k]; !ok {
			t.Fatalf("the delivery's span is missing the attribute %q: the design fixes exactly %v", k, fixed)
		}
	}
}
