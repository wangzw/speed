package stdout_test

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/observability/stdout"
)

type noConfig struct{}

func (noConfig) Decode(string, any) error { return nil }

// TestTheModuleDeliversBothExportersFromOneProduct is the delivery
// contract for this backend.
func TestTheModuleDeliversBothExportersFromOneProduct(t *testing.T) {
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(noConfig{}, config.ModuleName)
	reg.Register(stdout.Module())

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{stdout.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			got, ok := reg.Enablement(stdout.ModuleName)
			if !ok || got.State != core.StateEnabled {
				t.Fatalf("%s is %v, want enabled", stdout.ModuleName, got.State)
			}
			span, err := core.Resolve[observability.SpanExporter](reg)
			if err != nil {
				t.Fatalf("resolving the span exporter: %v", err)
			}
			metric, err := core.Resolve[observability.MetricExporter](reg)
			if err != nil {
				t.Fatalf("resolving the metric exporter: %v", err)
			}
			if _, ok := span.(sdkmetric.Exporter); !ok {
				t.Errorf("the span exporter is %T, which is not the metric exporter: both capabilities have "+
					"to come from the one product", span)
			}
			if _, ok := metric.(sdktrace.SpanExporter); !ok {
				t.Errorf("the metric exporter is %T, which is not the span exporter", metric)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
}
