//go:build integration

package postgres

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/tracing"
	"github.com/vislake/speed/pkg/internal/pgpool"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/log"
)

// recordedDelivery is one span the dispatch layer started for a subscriber: the
// name it was recorded under, the parent it was handed and the attributes it
// carried.
type recordedDelivery struct {
	name   string
	parent trace.SpanContext
	attrs  map[string]string
}

// recordingTracer keeps every span the dispatch layer starts, so a case can
// hold a delivery's parent and attributes to what the design fixes without an
// exporter.
type recordingTracer struct {
	mu    sync.Mutex
	spans []recordedDelivery
}

func (r *recordingTracer) Start(ctx context.Context, name string, attrs map[string]string) (context.Context, event.Span) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans = append(r.spans, recordedDelivery{name: name, parent: trace.SpanContextFromContext(ctx), attrs: attrs})
	return ctx, deliveredSpan{}
}

type deliveredSpan struct{}

func (deliveredSpan) End(error) {}

// deliveriesOf lists every span started for the named subscriber.
func (r *recordingTracer) deliveriesOf(name string) []recordedDelivery {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []recordedDelivery
	for _, s := range r.spans {
		if s.name == name {
			out = append(out, s)
		}
	}
	return out
}

// spanContext is a valid span context with the given identifiers, so a case can
// name the span a delivery is expected to be parented on.
func spanContext(traceID trace.TraceID, spanID trace.SpanID) trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	})
}

// openBusTracing builds a bus over the given database whose delivery spans are
// recorded on tracer.
func openBusTracing(t *testing.T, dsn string, tracer event.TracerProvider) *bus {
	t.Helper()
	opened, err := open(context.Background(), &pgpool.Config{DSN: dsn, MaxConns: 4}, log.Default(), tracer)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	local := opened.(*bus)
	t.Cleanup(func() { _ = local.Close(context.Background()) })
	return local
}

// TestDeliveryContinuesThePublishersTrace is the cross-replica case for this
// backend: an event published inside a span by one replica carries that span's
// tracing context through the outbox table, and the other replica's subscriber
// records its span as a child of it — the same trace id, not a trace of its own.
func TestDeliveryContinuesThePublishersTrace(t *testing.T) {
	dsn := pgtest.Acquire(t)
	rec := &recordingTracer{}
	observer := openBusTracing(t, dsn, rec)
	publisher := openBusTracing(t, dsn, tracing.Noop())
	delivered := make(chan struct{}, 1)
	observer.Subscribe("t", "postgres.subscriber", func(context.Context, event.Event) error {
		delivered <- struct{}{}
		return nil
	})
	if err := observer.begin(context.Background()); err != nil {
		t.Fatalf("begin on the observing replica: %v", err)
	}
	if err := publisher.begin(context.Background()); err != nil {
		t.Fatalf("begin on the publishing replica: %v", err)
	}
	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	spanID := trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}
	carrier := propagation.MapCarrier{}
	tracing.Propagator().Inject(trace.ContextWithSpanContext(context.Background(), spanContext(traceID, spanID)), carrier)
	if err := publisher.Publish(context.Background(), event.Event{
		Type: "t", TraceContext: map[string]string(carrier),
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case <-delivered:
	case <-time.After(10 * time.Second):
		t.Fatal("the other replica's subscriber did not receive the event")
	}
	deliveries := rec.deliveriesOf("postgres.subscriber")
	if len(deliveries) != 1 {
		t.Fatalf("the subscriber's delivery started %d spans, want 1", len(deliveries))
	}
	got := deliveries[0]
	if got.parent.TraceID() != traceID || got.parent.SpanID() != spanID {
		t.Fatalf("the subscriber's span is parented on %s/%s, want the publisher's span %s/%s: the "+
			"tracing context did not survive the outbox table", got.parent.TraceID(), got.parent.SpanID(), traceID, spanID)
	}
	if v := got.attrs["messaging.system"]; v != "postgres" {
		t.Fatalf("messaging.system is %q, want the backend name postgres", v)
	}
}

// TestAnEventPublishedOutsideAnySpanDeliversAsARoot pins the empty case: an
// event published with no tracing context stores the empty value in
// trace_context, and the subscriber's span is then a trace of its own. The
// delivery runs under a context that carries a span — exactly the reader's
// handling context, which the start stage derives from the context it is handed —
// so the parent has to be taken from the event, which carries none, and not from
// the span the handler happens to run under.
func TestAnEventPublishedOutsideAnySpanDeliversAsARoot(t *testing.T) {
	dsn := pgtest.Acquire(t)
	rec := &recordingTracer{}
	b := openBusTracing(t, dsn, rec)
	delivered := make(chan struct{}, 1)
	b.Subscribe("t", "postgres.subscriber", func(context.Context, event.Event) error {
		delivered <- struct{}{}
		return nil
	})
	// The reader handles deliveries on a context derived from this one, and this
	// one carries a span; an event that carries none must still deliver as a root.
	callTrace := trace.TraceID{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9}
	callSpan := trace.SpanID{9, 9, 9, 9, 9, 9, 9, 9}
	beginCtx := trace.ContextWithSpanContext(context.Background(), spanContext(callTrace, callSpan))
	if err := b.begin(beginCtx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := b.Publish(context.Background(), event.Event{Type: "t"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case <-delivered:
	case <-time.After(10 * time.Second):
		t.Fatal("the subscriber did not receive the event")
	}
	deliveries := rec.deliveriesOf("postgres.subscriber")
	if len(deliveries) != 1 {
		t.Fatalf("the subscriber's delivery started %d spans, want 1", len(deliveries))
	}
	if got := deliveries[0].parent; got.IsValid() {
		t.Fatalf("the subscriber's span is parented on %s/%s, want no parent: an event published "+
			"outside any span leaves the delivery's span a root, not a child of the span the delivery "+
			"ran under %s/%s", got.TraceID(), got.SpanID(), callTrace, callSpan)
	}
}

// TestOpeningAddsTheTraceContextColumnToAnOlderOutbox pins the upgrade of a
// table built before the tracing column existed: opening the bus adds it, and
// the rows already there keep the default empty value rather than failing the
// read.
func TestOpeningAddsTheTraceContextColumnToAnOlderOutbox(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	// The outbox as it was before the tracing column: the same columns as
	// createOutbox, less trace_context.
	const olderOutbox = `CREATE TABLE ` + table + ` (
		id         BIGSERIAL PRIMARY KEY,
		type       TEXT NOT NULL,
		tenant     TEXT NOT NULL DEFAULT '',
		payload    TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		xact       xid8 NOT NULL DEFAULT pg_current_xact_id(),
		identifier TEXT NOT NULL DEFAULT ''
	)`
	if _, err := conn.Exec(ctx, olderOutbox); err != nil {
		t.Fatalf("building the older table: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO `+table+` (type, payload) VALUES ('t', '{}')`); err != nil {
		t.Fatalf("writing a row before the column existed: %v", err)
	}
	// Opening the bus prepares the tables, adding what the older one lacks.
	b := openBus(t, dsn)
	var present bool
	if err := b.pool.QueryRow(ctx, columnPresent, table, "trace_context").Scan(&present); err != nil {
		t.Fatalf("asking the catalog for the column: %v", err)
	}
	if !present {
		t.Fatalf("opening the bus on an outbox that predates the tracing column left %s without "+
			"trace_context, so every read of it would fail", table)
	}
	var stored string
	if err := b.pool.QueryRow(ctx, `SELECT trace_context FROM `+table+` WHERE type = 't'`).Scan(&stored); err != nil {
		t.Fatalf("reading the older row: %v", err)
	}
	if stored != "" {
		t.Fatalf("the row written before the column existed reads its tracing context as %q, want the "+
			"empty value", stored)
	}
}
