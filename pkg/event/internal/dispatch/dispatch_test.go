package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/event"
)

type noopTracer struct{}

func (noopTracer) Start(ctx context.Context, _ string, _ map[string]string) (context.Context, event.Span) {
	return ctx, noopSpan{}
}

type noopSpan struct{}

func (noopSpan) End(error) {}

func newTable() *Table {
	return New("event/test", "test", slog.New(slog.NewTextHandler(io.Discard, nil)),
		noopTracer{}, propagation.TraceContext{})
}

// TestAStopWhoseGraceRunsOutCancelsTheSubscribers pins the bound on the
// stop stage.
func TestAStopWhoseGraceRunsOutCancelsTheSubscribers(t *testing.T) {
	previousGrace := Grace
	Grace = time.Hour
	t.Cleanup(func() { Grace = previousGrace })

	tbl := newTable()
	const grace = 100 * time.Millisecond
	tbl.grace = grace
	released := make(chan struct{})
	defer close(released)
	sawCancel := make(chan error, 1)
	tbl.Subscribe("t", "test", func(ctx context.Context, _ event.Event) error {
		<-ctx.Done()
		sawCancel <- ctx.Err()
		<-released
		return nil
	})
	tbl.Freeze(context.Background())
	tbl.Go(func(_, handling context.Context) { tbl.Deliver(handling, event.Event{Type: "t"}) })

	bound := coretest.DeclaredAgainst(t, 0, grace+settle+time.Second, Grace)
	stopped := make(chan error, 1)
	go func() { stopped <- tbl.Stop() }()
	var err error
	select {
	case err = <-stopped:
	case <-time.After(bound):
		t.Fatalf("the stop stage had not returned within %v, past its grace and the moment it gives a "+
			"cancelled subscriber", bound)
	}
	if err == nil {
		t.Fatal("a stop whose subscribers outlived the grace reported a clean stop")
	}
	select {
	case cancelled := <-sawCancel:
		if !errors.Is(cancelled, context.Canceled) {
			t.Fatalf("the subscriber running past the grace was handed %v, want a cancelled context", cancelled)
		}
	case <-time.After(bound):
		t.Fatal("the subscriber running past the grace was not handed a cancelled context")
	}
	if again := tbl.Stop(); again == nil || again.Error() != err.Error() {
		t.Fatalf("a second Stop reported %v, want what the first reported: %v", again, err)
	}
}

// TestAStopBeforeTheStartStageIsClean covers the rollback shape: a startup that
// failed part-way stops every module it built, whatever stage it reached.
func TestAStopBeforeTheStartStageIsClean(t *testing.T) {
	tbl := newTable()
	if err := tbl.Stop(); err != nil {
		t.Fatalf("Stop on a table that never froze: %v", err)
	}
	if err := tbl.Close(); err != nil {
		t.Fatalf("Close on a table that never froze: %v", err)
	}
	if _, first := tbl.Freeze(context.Background()); first {
		t.Fatal("a table that has stopped opened to publishing")
	}
	if err := tbl.Accepting(); !errors.Is(err, event.ErrClosed) {
		t.Fatalf("Accepting after the stop stage: %v, want ErrClosed", err)
	}
}

// TestNoReaderStartsAfterTheStopStage pins what keeps the wait for the
// readers sound.
func TestNoReaderStartsAfterTheStopStage(t *testing.T) {
	tbl := newTable()
	tbl.Freeze(context.Background())
	if err := tbl.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	var ran atomic.Bool
	tbl.Go(func(context.Context, context.Context) { ran.Store(true) })
	time.Sleep(50 * time.Millisecond)
	if ran.Load() {
		t.Fatal("a reader started after the stop stage")
	}
}

// TestTheDrivesCancellationDoesNotReachTheBus pins where the contexts a
// bus reads and handles under come from.
func TestTheDrivesCancellationDoesNotReachTheBus(t *testing.T) {
	tbl := newTable()
	type key struct{}
	drive, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "carried"))
	tbl.Freeze(drive)
	seen := make(chan [2]context.Context, 1)
	tbl.Go(func(reading, handling context.Context) { seen <- [2]context.Context{reading, handling} })
	got := <-seen
	cancel()
	for i, ctx := range got {
		if ctx.Err() != nil {
			t.Fatalf("context %d ended with the drive's: %v", i, ctx.Err())
		}
		if ctx.Value(key{}) != "carried" {
			t.Fatalf("context %d lost the values the drive's context carried", i)
		}
	}
	if err := tbl.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got[0].Err() == nil {
		t.Fatal("the reading context outlived the stop stage")
	}
}

// TestPayloadKeepsEveryNumberAsItsText pins the transport-side decoding.
func TestPayloadKeepsEveryNumberAsItsText(t *testing.T) {
	got, err := Payload([]byte(`{"id":9007199254740993,"deal":{"amount":9223372036854775807},"ratio":0.25}`))
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	fields, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("the payload decoded as %T, want the ordinary map", got)
	}
	if id := fields["id"]; id != json.Number("9007199254740993") {
		t.Fatalf("id decoded as %#v, want its text", id)
	}
	nested, _ := fields["deal"].(map[string]any)
	if amount := nested["amount"]; amount != json.Number("9223372036854775807") {
		t.Fatalf("the nested amount decoded as %#v, want its text", amount)
	}
	if ratio := fields["ratio"]; ratio != json.Number("0.25") {
		t.Fatalf("ratio decoded as %#v, want its text", ratio)
	}
}

// TestPayloadRefusesDataAfterItsValue holds the decoder to a payload being one
// value, with nothing after it but whitespace.
func TestPayloadRefusesDataAfterItsValue(t *testing.T) {
	if _, err := Payload([]byte(`{"a":1} {"b":2}`)); err == nil {
		t.Fatal("a payload carrying a second value decoded without an error")
	}
	if _, err := Payload([]byte(" null \n")); err != nil {
		t.Fatalf("a payload of null with surrounding whitespace: %v", err)
	}
}

type nameRecordingTracer struct {
	mu    sync.Mutex
	names []string
}

func (r *nameRecordingTracer) Start(ctx context.Context, name string, _ map[string]string) (context.Context, event.Span) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names = append(r.names, name)
	return ctx, noopSpan{}
}

func (r *nameRecordingTracer) started() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.names)
}

// TestEverySubscriberOfOneEventGetsItsOwnNamedSpan checks that a delivery
// starts one named span per subscriber.
func TestEverySubscriberOfOneEventGetsItsOwnNamedSpan(t *testing.T) {
	rec := &nameRecordingTracer{}
	tbl := New("event/test", "test", slog.New(slog.NewTextHandler(io.Discard, nil)), rec, propagation.TraceContext{})
	subscribers := []string{"audit.journal", "webhooks.dispatcher", "case.third"}
	var ran atomic.Int32
	for _, name := range subscribers {
		tbl.Subscribe("t", name, func(context.Context, event.Event) error {
			ran.Add(1)
			return nil
		})
	}
	tbl.Freeze(context.Background())
	tbl.Deliver(context.Background(), event.Event{Type: "t", ID: "e1"})
	if n := ran.Load(); n != int32(len(subscribers)) {
		t.Fatalf("one delivery ran %d subscribers, want %d", n, len(subscribers))
	}
	if got := rec.started(); !slices.Equal(got, subscribers) {
		t.Fatalf("one delivery to %d subscribers started the spans %v, want one span per subscriber named %v, "+
			"in subscription order", len(subscribers), got, subscribers)
	}
}

// TestStampCarriesANonEmptyTracingContext pins the "carried as it is"
// half of what the bus writes into an event's tracing context.
func TestStampCarriesANonEmptyTracingContext(t *testing.T) {
	tbl := newTable()
	publisher := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
	}))
	carried := map[string]string{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	got := tbl.Stamp(publisher, event.Event{Type: "t", TraceContext: carried})
	if got.TraceContext["traceparent"] != carried["traceparent"] {
		t.Fatalf("Stamp wrote the tracing context %v into an event that already carried %v: a "+
			"publisher's own tracing context is carried as it is, not overwritten with the span the "+
			"publisher happens to be in", got.TraceContext, carried)
	}
}
