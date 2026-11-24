package relay

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/dispatch"
	"github.com/vislake/speed/pkg/event/internal/tracing"
)

func spanContext(traceID trace.TraceID, spanID trace.SpanID) trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	})
}

func stageInSpan(t *testing.T, ctx context.Context, database db.Database, r *relay, eventID string) {
	t.Helper()
	err := database.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.Stage(tx, event.Event{
			ID: eventID, Type: "order.placed", TenantID: "acme", Payload: placed{OrderID: eventID},
		})
	})
	if err != nil {
		t.Fatalf("staging the event under %s: %v", eventID, err)
	}
}

func traceContextOf(t *testing.T, database db.Database, eventID string) string {
	t.Helper()
	var stored string
	if err := database.DB().Raw(`SELECT trace_context FROM `+tableName+` WHERE event_id = ?`, eventID).
		Row().Scan(&stored); err != nil {
		t.Fatalf("reading the tracing context staged under %s: %v", eventID, err)
	}
	return stored
}

func parentOf(t *testing.T, stored string) trace.SpanContext {
	t.Helper()
	return trace.SpanContextFromContext(
		tracing.Propagator().Extract(context.Background(), propagation.MapCarrier(dispatch.DecodeTraceContext(stored))))
}

// TestTheStagedRowCarriesTheSpanTheWriteWasIn pins the tracing context
// the relay stores.
func TestTheStagedRowCarriesTheSpanTheWriteWasIn(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		r := newQuick(t, database, newRecordingBus("only", &ledger{}), quick)

		traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
		spanID := trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}
		ctx := trace.ContextWithSpanContext(t.Context(), spanContext(traceID, spanID))
		stageInSpan(t, ctx, database, r, "e1")

		got := parentOf(t, traceContextOf(t, database, "e1"))
		if got.TraceID() != traceID || got.SpanID() != spanID {
			t.Fatalf("the staged row names the span %s/%s, want the span the write was in, %s/%s",
				got.TraceID(), got.SpanID(), traceID, spanID)
		}
	})
}

// TestAStagedEventThatAlreadyCarriesTracingContextKeepsIt pins that Stage
// leaves an event's existing tracing context as it is.
func TestAStagedEventThatAlreadyCarriesTracingContextKeepsIt(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		r := newQuick(t, database, newRecordingBus("only", &ledger{}), quick)

		// The span the write is in, which must not overwrite the carried one.
		writeTrace := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
		writeSpan := trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}
		ctx := trace.ContextWithSpanContext(t.Context(), spanContext(writeTrace, writeSpan))

		// The span the event already carries, which must survive staging.
		carriedTrace := trace.TraceID{16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
		carriedSpan := trace.SpanID{8, 7, 6, 5, 4, 3, 2, 1}
		carrier := propagation.MapCarrier{}
		tracing.Propagator().Inject(
			trace.ContextWithSpanContext(context.Background(), spanContext(carriedTrace, carriedSpan)), carrier)

		err := database.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return r.Stage(tx, event.Event{
				ID: "e1", Type: "order.placed", TenantID: "acme", Payload: placed{OrderID: "e1"},
				TraceContext: map[string]string(carrier),
			})
		})
		if err != nil {
			t.Fatalf("staging an event that already carries a tracing context: %v", err)
		}

		got := parentOf(t, traceContextOf(t, database, "e1"))
		if got.TraceID() != carriedTrace || got.SpanID() != carriedSpan {
			t.Fatalf("the staged row names the span %s/%s, want the one the event already carried %s/%s: "+
				"a tracing context the event carries is kept as it is, not overwritten with the span the "+
				"write was in, %s/%s", got.TraceID(), got.SpanID(), carriedTrace, carriedSpan,
				writeTrace, writeSpan)
		}
	})
}

// TestThePublishedEventContinuesTheSpansTrace is the case for the whole
// outbox path.
func TestThePublishedEventContinuesTheSpansTrace(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		l := &ledger{}
		r := newQuick(t, database, newRecordingBus("only", l), quick)
		r.start(t.Context())

		traceID := trace.TraceID{16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
		spanID := trace.SpanID{8, 7, 6, 5, 4, 3, 2, 1}
		ctx := trace.ContextWithSpanContext(t.Context(), spanContext(traceID, spanID))
		stageInSpan(t, ctx, database, r, "e1")

		if !eventually(func() bool { return l.count() > 0 }) {
			t.Fatalf("the staged event was never published")
		}
		published := l.events()[0]
		got := trace.SpanContextFromContext(
			tracing.Propagator().Extract(context.Background(), propagation.MapCarrier(published.TraceContext)))
		if got.TraceID() != traceID || got.SpanID() != spanID {
			t.Fatalf("the published event names the span %s/%s, want the span the write was in, %s/%s",
				got.TraceID(), got.SpanID(), traceID, spanID)
		}
	})
}

// TestAStagedEventWithNoTracingContextIsPublishedWithoutOne pins the
// empty case.
func TestAStagedEventWithNoTracingContextIsPublishedWithoutOne(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		l := &ledger{}
		r := newQuick(t, database, newRecordingBus("only", l), quick)
		if err := database.DB().Exec(
			`INSERT INTO ` + tableName + ` (event_id, type, tenant_id, payload, staged_at, available_at) ` +
				`VALUES ('old', 't', 'acme', '{}', 0, 0)`).Error; err != nil {
			t.Fatalf("writing a row before the column existed: %v", err)
		}
		r.start(t.Context())

		if !eventually(func() bool { return l.count() > 0 }) {
			t.Fatalf("the row with no tracing context was never published")
		}
		published := l.events()[0]
		got := trace.SpanContextFromContext(
			tracing.Propagator().Extract(context.Background(), propagation.MapCarrier(published.TraceContext)))
		if got.IsValid() {
			t.Fatalf("the published event carries the tracing context %v, want none: the row held the "+
				"empty default", published.TraceContext)
		}
	})
}

// TestTheMigrationAddsTheTraceContextColumn holds both dialects'
// migrations to the column the relay reads and writes.
func TestTheMigrationAddsTheTraceContextColumn(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		if err := database.DB().Raw(`SELECT trace_context FROM ` + tableName + ` WHERE 1 = 0`).Error; err != nil {
			t.Fatalf("the staged table has no trace_context column after the migration: %v", err)
		}
	})
}
