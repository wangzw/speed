package event_test

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/memory"
	"github.com/vislake/speed/pkg/observability"
)

// observing is a module that subscribes one named handler to one event type in
// its Init, so an end-to-end case can carry a subscriber through a drive without
// reaching into the implementation's own bus. onDelivery is called from the
// handler with the received event and the context the delivery runs under.
func observing(name, eventType, subscriber string, onDelivery func(ctx context.Context, evt event.Event)) core.Module {
	return core.Module{
		Name:     name,
		Requires: []core.Requirement{{Token: (*event.Bus)(nil)}},
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return err
			}
			bus.Subscribe(eventType, subscriber, func(ctx context.Context, evt event.Event) error {
				if onDelivery != nil {
					onDelivery(ctx, evt)
				}
				return nil
			})
			return nil
		},
	}
}

// telemetryModuleName is the name the telemetry stand-in registers under. It
// has to sort after every event.* module — "o" comes after "e" — standing where
// the assembler's own telemetry module stands: a module the bus does not come
// before by name alone. The only thing that orders it ahead of the bus is the
// optional tracer requirement event.memory appends to its descriptor, which is
// the edge this case exists to make load-bearing.
const telemetryModuleName = "observability.otlp"

// telemetryModule is a module that delivers the telemetry capability, standing
// where the observability module's own product would: it declares the capability
// and its New builds the tracer provider the assembly carries, so the drive
// constructs it at the place the dependency order gives it the way a real
// assembly does, rather than a case handing a provider in ahead of time.
//
// The provider exports on exp, so a case holds what the assembly's own provider
// received. Building it here rather than supplying it is what makes the case
// depend on the bus's tracer requirement ordering this module ahead of the bus:
// supplied, it would be present from the start of the drive and the ordering
// would not matter, and the silent loss the requirement exists to prevent would
// go unnoticed.
func telemetryModule(exp *tracetest.InMemoryExporter) core.Module {
	return core.Module{
		Name:     telemetryModuleName,
		Provides: []core.Provision{{Token: (*observability.TracerProvider)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return sdktrace.NewTracerProvider(
				sdktrace.WithSampler(sdktrace.AlwaysSample()),
				sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
			), nil
		},
	}
}

// TestADeliverySpanReachesTheAssemblysExporter is the end-to-end shape of the
// telemetry seam: a registry that carries a real tracer provider, a bus built
// through the implementation's own module descriptor, and one publish — and the
// span the dispatch layer records for that delivery is exported on the
// assembly's own provider, named after the subscriber.
//
// It is the whole path from module.New taking up what spec.Trace resolves to a
// subscriber's span leaving the process, which the isolated adapter cases cannot
// cover: a New that ignored what spec.Trace returned and fell back to the
// provider that exports nothing would leave every one of them green while the
// bus ran exporting no signal and saying nothing — the silent failure the
// optional tracer requirement exists to prevent.
//
// The telemetry module is named to sort after event.memory and the drive
// constructs it rather than a case supplying it, so it is the tracer
// requirement — and nothing else — that orders it ahead of the bus. Take that
// requirement away and the bus is built first, spec.Trace resolves no provider,
// and this case observes no exported span: the case holds the consequence the
// requirement exists for, not merely the requirement's presence.
func TestADeliverySpanReachesTheAssemblysExporter(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(declared{replicas: 1}, config.ModuleName)
	reg.Register(telemetryModule(exp))
	reg.Register(memory.Module())
	reg.Register(observing("case.delivery", "case.published", "case.subscriber", nil))

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageStart,
		Participants: []string{telemetryModuleName, memory.ModuleName, "case.delivery"},
		Action: func(ctx context.Context, reg *core.Registry) error {
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return err
			}
			return bus.Publish(ctx, event.Event{Type: "case.published"})
		},
	})
	if err != nil {
		t.Fatalf("an assembly carrying telemetry failed the startup or the publish: %v", err)
	}
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("publishing one event to one subscriber exported %d spans on the assembly's own provider, "+
			"want 1: the provider spec.Trace resolved did not reach the dispatch layer the implementation "+
			"built, so the bus ran exporting no signal", len(spans))
	}
	if spans[0].Name != "case.subscriber" {
		t.Fatalf("the exported span is named %q, want the subscriber's name %q", spans[0].Name, "case.subscriber")
	}
}

// TestWithNoTelemetryADeliveryCarriesNoTracingContextAndNoSignal is the
// end-to-end shape of an assembly that carries no telemetry: spec.Trace falls
// back to the provider that exports nothing, so a publish in it leaves the
// event's tracing context empty — the injection spins idle — and the
// subscriber's span is started on the provider that drops it, carrying no valid
// span context.
//
// It is the whole path a host with no telemetry runs, which the isolated no-op
// case cannot show: a fallback that fabricated a non-empty carrier, or one that
// started a real span, would leave it green while raising the false promise that
// a trace reaches the subscriber from an assembly that produces none.
func TestWithNoTelemetryADeliveryCarriesNoTracingContextAndNoSignal(t *testing.T) {
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(declared{replicas: 1}, config.ModuleName)
	reg.Register(memory.Module())
	var delivered bool
	var carried map[string]string
	var spanValid bool
	reg.Register(observing("case.delivery", "case.published", "case.subscriber",
		func(ctx context.Context, evt event.Event) {
			delivered = true
			carried = evt.TraceContext
			spanValid = trace.SpanFromContext(ctx).SpanContext().IsValid()
		}))

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageStart,
		Participants: []string{memory.ModuleName, "case.delivery"},
		Action: func(_ context.Context, reg *core.Registry) error {
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return err
			}
			// A publisher in an assembly with no telemetry is in no span: the
			// context it publishes from carries none.
			return bus.Publish(context.Background(), event.Event{Type: "case.published"})
		},
	})
	if err != nil {
		t.Fatalf("an assembly with no telemetry failed the startup or the publish: %v", err)
	}
	if !delivered {
		t.Fatal("the subscriber did not run, so nothing of the delivery was observed")
	}
	if len(carried) != 0 {
		t.Fatalf("an event published in an assembly with no telemetry carried the tracing context %v, want "+
			"none: with no telemetry the injection spins idle and never fills the carrier", carried)
	}
	if spanValid {
		t.Fatal("the subscriber's span carries a valid span context in an assembly with no telemetry, so a " +
			"signal is produced: the fallback provider has to be one that exports nothing")
	}
}
