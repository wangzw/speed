package otel_test

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/observability/otel"
)

// resourceRecorder keeps the resource each signal arrived with.
type resourceRecorder struct {
	mu      sync.Mutex
	spans   []*resource.Resource
	metrics []*resource.Resource
}

type resourceSpans struct{ r *resourceRecorder }

func (s resourceSpans) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	for _, span := range spans {
		s.r.spans = append(s.r.spans, span.Resource())
	}
	return nil
}
func (resourceSpans) Shutdown(context.Context) error { return nil }

type resourceMetrics struct{ r *resourceRecorder }

func (resourceMetrics) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(kind)
}

func (resourceMetrics) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (m resourceMetrics) Export(_ context.Context, rm *metricdata.ResourceMetrics) error {
	m.r.mu.Lock()
	defer m.r.mu.Unlock()
	if len(rm.ScopeMetrics) > 0 {
		m.r.metrics = append(m.r.metrics, rm.Resource)
	}
	return nil
}
func (resourceMetrics) ForceFlush(context.Context) error { return nil }
func (resourceMetrics) Shutdown(context.Context) error   { return nil }

// runReplica drives one assembly, as one replica of a service would, records a
// span and a data point, and returns the resources they were exported with.
func runReplica(t *testing.T) *resourceRecorder {
	t.Helper()
	rec := &resourceRecorder{}
	reg := core.New()
	reg.Supply((*config.Reader)(nil), fixedConfig{serviceName: "orders"})
	reg.Supply((*log.Logger)(nil), stubLogger{lg: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))})
	reg.Register(otel.Module())
	reg.Register(core.Module{
		Name: "observability.test-resource",
		Provides: []core.Provision{
			{Token: (*observability.SpanExporter)(nil)},
			{Token: (*observability.MetricExporter)(nil)},
		},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) {
			return &observability.Exporters{Span: resourceSpans{rec}, Metric: resourceMetrics{rec}}, nil
		},
	})
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{otel.ModuleName, "observability.test-resource"},
		Action: func(ctx context.Context, reg *core.Registry) error {
			tp, err := core.Resolve[observability.TracerProvider](reg)
			if err != nil {
				return err
			}
			mp, err := core.Resolve[observability.MeterProvider](reg)
			if err != nil {
				return err
			}
			_, span := tp.Tracer("orders").Start(ctx, "place")
			span.End()
			counter, err := mp.Meter("orders").Int64Counter("orders.placed")
			if err != nil {
				return err
			}
			counter.Add(ctx, 1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.spans) == 0 || len(rec.metrics) == 0 {
		t.Fatalf("the replica exported %d span and %d metric batches, want both", len(rec.spans), len(rec.metrics))
	}
	return rec
}

func resourceValue(t *testing.T, res *resource.Resource, key string) string {
	t.Helper()
	v, ok := res.Set().Value(attribute.Key(key))
	if !ok {
		t.Fatalf("the resource carries no %s among %v", key, res.Attributes())
	}
	return v.AsString()
}

// TestEachReplicaExportsUnderItsOwnInstance is the case for telling two
// replicas of one service apart. Both export the same metric streams with the
// same attributes; a resource that carried the service name alone would give
// the two cumulative streams one identity, and a collector or a metric store
// would see a single series flapping between two replicas' values. Each
// assembly therefore reports an instance identity of its own, on both signals.
func TestEachReplicaExportsUnderItsOwnInstance(t *testing.T) {
	first, second := runReplica(t), runReplica(t)

	ids := make([]string, 0, 2)
	for _, rec := range []*resourceRecorder{first, second} {
		spanID := resourceValue(t, rec.spans[0], "service.instance.id")
		metricID := resourceValue(t, rec.metrics[0], "service.instance.id")
		if spanID == "" || spanID != metricID {
			t.Errorf("one replica reported instance %q on spans and %q on metrics, want one non-empty identity",
				spanID, metricID)
		}
		if got := resourceValue(t, rec.spans[0], "service.name"); got != "orders" {
			t.Errorf("the service name is %q, want the configured orders", got)
		}
		ids = append(ids, spanID)
	}
	if ids[0] == ids[1] {
		t.Errorf("two replicas both reported instance %q, want each its own", ids[0])
	}
}
