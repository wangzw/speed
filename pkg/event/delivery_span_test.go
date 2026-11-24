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

const telemetryModuleName = "observability.otlp"

// telemetryModule delivers the telemetry capability where the observability
// module's product would, so the drive constructs it in dependency order.
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

// TestADeliverySpanReachesTheAssemblysExporter is the end-to-end shape of
// the telemetry seam.
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
// end-to-end shape of an assembly that carries no telemetry.
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
