package tracing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/dispatch"
	"github.com/vislake/speed/pkg/observability"
)

func recordingProvider(t *testing.T) (provider, *tracetest.InMemoryExporter) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
	)
	return provider{upstream: tp}, exp
}

func attrOf(attrs []attribute.KeyValue, key string) (string, bool) {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value.AsString(), true
		}
	}
	return "", false
}

// TestStartCarriesTheSubscriberNameAndTheAttributes pins what the adapter
// does with what the dispatch layer hands the narrow Start.
func TestStartCarriesTheSubscriberNameAndTheAttributes(t *testing.T) {
	p, exp := recordingProvider(t)
	_, span := p.Start(context.Background(), "audit.journal", map[string]string{
		"messaging.system": "redis", "messaging.operation": "process",
	})
	span.End(nil)
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("starting one span recorded %d, want 1", len(spans))
	}
	if spans[0].Name != "audit.journal" {
		t.Fatalf("the span is named %q, want the subscriber's name", spans[0].Name)
	}
	if v, ok := attrOf(spans[0].Attributes, "messaging.system"); !ok || v != "redis" {
		t.Fatalf("messaging.system is (%q, %v), want redis", v, ok)
	}
	if v, ok := attrOf(spans[0].Attributes, "messaging.operation"); !ok || v != "process" {
		t.Fatalf("messaging.operation is (%q, %v), want process", v, ok)
	}
	if spans[0].Status.Code == codes.Error {
		t.Fatal("a delivery that returned no error was marked failed")
	}
}

// TestEndMarksAFailedDeliveryWithTheErrorsType pins what a failed
// delivery's span looks like.
func TestEndMarksAFailedDeliveryWithTheErrorsType(t *testing.T) {
	p, exp := recordingProvider(t)
	_, span := p.Start(context.Background(), "audit.journal", nil)
	span.End(errors.New("the payload carried a secret value"))
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("starting one span recorded %d, want 1", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Fatalf("a delivery that returned an error was marked %v, want Error", spans[0].Status.Code)
	}
	v, ok := attrOf(spans[0].Attributes, "error.type")
	if !ok {
		t.Fatal("a failed delivery's span carries no error.type")
	}
	if want := "*errors.errorString"; v != want {
		t.Fatalf("error.type is %q, want the error's type %q", v, want)
	}
	if spans[0].Status.Description == "the payload carried a secret value" {
		t.Fatal("the error's text was written into the span, where it can quote a payload value")
	}
}

// TestProviderTakesUpTheTelemetryProviderTheAssemblyDelivers pins the
// branch an assembly with telemetry reaches.
func TestProviderTakesUpTheTelemetryProviderTheAssemblyDelivers(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	upstream := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
	)
	reg := core.New()
	reg.Register(core.Module{
		Name:     "test.telemetry",
		Provides: []core.Provision{{Token: (*observability.TracerProvider)(nil)}},
	})
	reg.Supply(upstream, "test.telemetry")

	p, err := Provider(reg)
	if err != nil {
		t.Fatalf("Provider over an assembly that delivers a telemetry provider: %v", err)
	}
	_, span := p.Start(context.Background(), "tracing.subscriber",
		map[string]string{"messaging.system": "memory"})
	span.End(nil)
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("starting a span on the provider Provider handed back recorded %d spans on the "+
			"assembly's exporter, want 1: Provider did not take up the telemetry provider the registry "+
			"holds", len(spans))
	}
	if spans[0].Name != "tracing.subscriber" {
		t.Fatalf("the recorded span is named %q, want the subscriber's name", spans[0].Name)
	}
}

// TestProviderFallsBackToAUsableNoOpProvider pins the assembly with no
// telemetry.
func TestProviderFallsBackToAUsableNoOpProvider(t *testing.T) {
	p, err := Provider(core.New())
	if err != nil {
		t.Fatalf("Provider over an assembly with no telemetry: %v", err)
	}
	if p == nil {
		t.Fatal("Provider over an assembly with no telemetry handed back nothing to record a span on")
	}
	ctx, span := p.Start(context.Background(), "sub", map[string]string{"messaging.system": "memory"})
	if trace.SpanFromContext(ctx).IsRecording() {
		t.Fatal("the fallback provider's span is recording, so it would export a signal: the fallback " +
			"is the provider that exports nothing")
	}
	if sc := trace.SpanFromContext(ctx).SpanContext(); sc.IsValid() {
		t.Fatalf("the fallback provider's span carries the valid span context %s/%s, want none: it is "+
			"a no-op", sc.TraceID(), sc.SpanID())
	}
	span.End(errors.New("ignored: no signal is exported"))
}

func deliveryTable(t *testing.T) (*dispatch.Table, *tracetest.InMemoryExporter) {
	t.Helper()
	p, exp := recordingProvider(t)
	return dispatch.New("event/test", "memory",
		slog.New(slog.NewTextHandler(io.Discard, nil)), p, Propagator()), exp
}

// deliveryFailure is the error a failing subscriber returns, so the span's
// error.type is a type of this case's own and cannot be confused with the
// error's text.
type deliveryFailure struct{ reason string }

func (e *deliveryFailure) Error() string { return e.reason }

// TestAFailedDeliveryIsRecordedAsAnError checks that a failed delivery's span
// is exported as an error.
func TestAFailedDeliveryIsRecordedAsAnError(t *testing.T) {
	failure := &deliveryFailure{reason: "the payload carried a secret value"}
	const panicText = "this handler panics on purpose"
	// The dispatch layer wraps a panicking subscriber in an error of its own,
	// so the failure the span is ended with on that path is this wrap — its
	// type, not the panic's text, is what error.type has to carry.
	panicFailure := fmt.Errorf("the handler panicked: %v", panicText)
	cases := []struct {
		name    string
		handler event.Handler
		// failed is the error the delivery reports: what the handler returned,
		// or the wrap the dispatch layer builds around a panic.
		failed error
	}{
		{"returns an error", func(context.Context, event.Event) error { return failure }, failure},
		{"panics", func(context.Context, event.Event) error { panic(panicText) }, panicFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tbl, exp := deliveryTable(t)
			tbl.Subscribe("t", "tracing.subscriber", tc.handler)
			tbl.Freeze(context.Background())
			tbl.Deliver(context.Background(), event.Event{Type: "t", ID: "e1"})
			spans := exp.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("one delivery recorded %d spans, want 1", len(spans))
			}
			if spans[0].Status.Code != codes.Error {
				t.Fatalf("the span of a delivery whose subscriber failed is %v, want Error: a failed "+
					"delivery is the thing the span is recorded to show", spans[0].Status.Code)
			}
			v, ok := attrOf(spans[0].Attributes, "error.type")
			if !ok {
				t.Fatal("a failed delivery's span carries no error.type")
			}
			if want := fmt.Sprintf("%T", tc.failed); v != want {
				t.Fatalf("error.type is %q, want the failure's own type %q", v, want)
			}
			if v == tc.failed.Error() {
				t.Fatal("error.type is the failure's text, which can quote a payload value")
			}
		})
	}
}

// TestTwoSubscribersOfOneEventTypeMayShareAName checks that two subscribers of
// one event type may share the span name.
func TestTwoSubscribersOfOneEventTypeMayShareAName(t *testing.T) {
	tbl, exp := deliveryTable(t)
	delivered := 0
	for i := 0; i < 2; i++ {
		tbl.Subscribe("t", "shared.subscriber", func(context.Context, event.Event) error {
			delivered++
			return nil
		})
	}
	tbl.Freeze(context.Background())
	tbl.Deliver(context.Background(), event.Event{Type: "t", ID: "e1"})
	if delivered != 2 {
		t.Fatalf("a publish to one event type with two same-named subscribers ran %d of them, want 2: "+
			"a repeated name must not drop a subscriber", delivered)
	}
	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("a publish to two same-named subscribers exported %d spans, want 2: each delivery gets "+
			"a span of its own whatever the name", len(spans))
	}
	for _, span := range spans {
		if span.Name != "shared.subscriber" {
			t.Fatalf("a delivery's span is named %q, want the shared subscriber name %q",
				span.Name, "shared.subscriber")
		}
		if v, ok := attrOf(span.Attributes, "messaging.destination.name"); !ok || v != "t" {
			t.Fatalf("a same-named span carries messaging.destination.name (%q, %v), want the event "+
				"type %q: the type is what tells two same-named deliveries apart", v, ok, "t")
		}
	}
}

// TestTheHandlerContextCarriesTheDeliverySpan pins what the context
// handed to a subscriber carries.
func TestTheHandlerContextCarriesTheDeliverySpan(t *testing.T) {
	tbl, exp := deliveryTable(t)
	seen := make(chan trace.SpanContext, 1)
	tbl.Subscribe("t", "tracing.subscriber", func(ctx context.Context, _ event.Event) error {
		seen <- trace.SpanFromContext(ctx).SpanContext()
		return nil
	})
	tbl.Freeze(context.Background())
	tbl.Deliver(context.Background(), event.Event{Type: "t", ID: "e1"})
	got := <-seen
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("one delivery recorded %d spans, want 1", len(spans))
	}
	if !got.IsValid() {
		t.Fatal("the context handed to the subscriber carried no span, so work it starts under it " +
			"cannot continue the delivery's part of the trace")
	}
	if want := spans[0].SpanContext; got.TraceID() != want.TraceID() || got.SpanID() != want.SpanID() {
		t.Fatalf("the context handed to the subscriber carries the span %s/%s, want the delivery's "+
			"span %s/%s — the one End is called on",
			got.TraceID(), got.SpanID(), want.TraceID(), want.SpanID())
	}
}

// TestADeliverySpanCarriesNoSpanKindCategory pins the clause the narrow
// Start leaves out.
func TestADeliverySpanCarriesNoSpanKindCategory(t *testing.T) {
	tbl, exp := deliveryTable(t)
	tbl.Subscribe("t", "tracing.subscriber", func(context.Context, event.Event) error { return nil })
	tbl.Freeze(context.Background())
	tbl.Deliver(context.Background(), event.Event{Type: "t", ID: "e1"})
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("one delivery recorded %d spans, want 1", len(spans))
	}
	// The SDK maps an unset span kind to SpanKindInternal.
	if got := spans[0].SpanKind; got != trace.SpanKindInternal {
		t.Fatalf("a delivery's exported span is of kind %v, want SpanKindInternal: the narrow Start "+
			"carries no span kind, so a delivery is neither a client nor a server span of a remote "+
			"call", got)
	}
}

// TestThePropagatorIsTheTelemetryModulesOne pins the single definition of
// the propagation format.
func TestThePropagatorIsTheTelemetryModulesOne(t *testing.T) {
	got, want := Propagator(), observability.Propagator()
	if got == nil {
		t.Fatal("Propagator handed back nothing, so the publisher's injection and the dispatch " +
			"layer's extraction have no format to share")
	}
	if reflect.TypeOf(got) != reflect.TypeOf(want) {
		t.Fatalf("Propagator is a %T, want the telemetry module's %T: a propagator of another "+
			"type is a second definition of the propagation format", got, want)
	}
	if got != want {
		t.Fatalf("Propagator is %v, want the telemetry module's own value %v: the format is defined "+
			"once and taken up from there, not built again here", got, want)
	}
}

// TestAFailedSubscriberLeavesTheOtherSubscribersSpansClean pins the
// clause that a subscriber's failure is handled in isolation.
func TestAFailedSubscriberLeavesTheOtherSubscribersSpansClean(t *testing.T) {
	tbl, exp := deliveryTable(t)
	failure := &deliveryFailure{reason: "the payload carried a secret value"}
	tbl.Subscribe("t", "before.subscriber", func(context.Context, event.Event) error { return nil })
	tbl.Subscribe("t", "failing.subscriber", func(context.Context, event.Event) error { return failure })
	tbl.Subscribe("t", "after.subscriber", func(context.Context, event.Event) error { return nil })
	tbl.Freeze(context.Background())
	tbl.Deliver(context.Background(), event.Event{Type: "t", ID: "e1"})
	spans := exp.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("one delivery to three subscribers exported %d spans, want 3", len(spans))
	}
	// Keyed by name rather than read in order: what tells the three apart is
	// the subscriber each is named after, not the order they happen to be
	// exported in.
	code := map[string]codes.Code{}
	for _, span := range spans {
		code[span.Name] = span.Status.Code
	}
	for _, name := range []string{"before.subscriber", "after.subscriber"} {
		got, ok := code[name]
		if !ok {
			t.Fatalf("the delivery to %q exported no span, so its span was not observed at all", name)
		}
		if got == codes.Error {
			t.Fatalf("the span of %q is marked failed though its handler returned nil: a subscriber's "+
				"failure ends that delivery's span alone and does not reach the other subscribers'", name)
		}
	}
	if got := code["failing.subscriber"]; got != codes.Error {
		t.Fatalf("the failing subscriber's span is %v, want Error: the delivery that failed is the "+
			"one whose span is marked", got)
	}
}
