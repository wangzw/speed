package otel_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/observability/otel"
)

// errShutDown is what a destination answers once it has been released, the way
// the upstream exporters do: an export after Shutdown is refused, not queued.
var errShutDown = errors.New("exporter shutdown")

// destination is one backend's receiving end, shared by its two exporters so
// that a case can read what reached it and in which order it was released.
type destination struct {
	mu      sync.Mutex
	spans   []string
	metrics []string
	// released lists the halves in the order they were shut down, and an
	// export that arrived after its half was released is refused and counted
	// in late.
	released []string
	late     int
}

func (d *destination) isReleased(half string) bool {
	for _, r := range d.released {
		if r == half {
			return true
		}
	}
	return false
}

type destinationSpans struct{ d *destination }

func (s destinationSpans) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	if s.d.isReleased("span") {
		s.d.late++
		return errShutDown
	}
	for _, span := range spans {
		s.d.spans = append(s.d.spans, span.Name())
	}
	return nil
}

func (s destinationSpans) Shutdown(context.Context) error {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	s.d.released = append(s.d.released, "span")
	return nil
}

type destinationMetrics struct{ d *destination }

func (m destinationMetrics) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(kind)
}

func (m destinationMetrics) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (m destinationMetrics) Export(_ context.Context, rm *metricdata.ResourceMetrics) error {
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	if m.d.isReleased("metric") {
		m.d.late++
		return errShutDown
	}
	for _, sm := range rm.ScopeMetrics {
		for _, metric := range sm.Metrics {
			m.d.metrics = append(m.d.metrics, metric.Name)
		}
	}
	return nil
}

func (m destinationMetrics) ForceFlush(context.Context) error { return nil }

func (m destinationMetrics) Shutdown(context.Context) error {
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	m.d.released = append(m.d.released, "metric")
	return nil
}

// backendModule is an export backend shaped the way the documentation asks one
// to be: both exporter capabilities, non-exclusive, delivered as one
// observability.Exporters, and no Close of its own.
func backendModule(name string, d *destination) core.Module {
	return core.Module{
		Name: name,
		Provides: []core.Provision{
			{Token: (*observability.SpanExporter)(nil)},
			{Token: (*observability.MetricExporter)(nil)},
		},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) {
			return &observability.Exporters{Span: destinationSpans{d}, Metric: destinationMetrics{d}}, nil
		},
	}
}

// driveAndRecord runs one command through Init with the given backends, records
// one span and one metric while it is up, and returns what the drive returned.
// The configured intervals are the defaults, a batch timeout of seconds and a
// metric interval of a minute, so nothing is exported while the action runs:
// whatever reaches a destination got there through the shutdown.
func driveAndRecord(t *testing.T, backends map[string]*destination) (*bytes.Buffer, error) {
	t.Helper()
	buf := &bytes.Buffer{}
	reg := core.New()
	reg.Supply((*config.Reader)(nil), fixedConfig{serviceName: "orders"})
	reg.Supply((*log.Logger)(nil), stubLogger{lg: slog.New(slog.NewTextHandler(buf, nil))})
	reg.Register(otel.Module())
	participants := []string{otel.ModuleName}
	for name, d := range backends {
		reg.Register(backendModule(name, d))
		participants = append(participants, name)
	}
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: participants,
		Action: func(ctx context.Context, reg *core.Registry) error {
			mp, err := core.Resolve[observability.MeterProvider](reg)
			if err != nil {
				return err
			}
			counter, err := mp.Meter("orders").Int64Counter("orders.placed")
			if err != nil {
				return err
			}
			counter.Add(ctx, 1)

			tp, err := core.Resolve[observability.TracerProvider](reg)
			if err != nil {
				return err
			}
			_, span := tp.Tracer("orders").Start(ctx, "place")
			span.End()
			return nil
		},
	})
	return buf, err
}

// TestCloseDeliversTheLastBatchOfBothSignals is the case for the flush the
// module promises at shutdown. A backend hands one product for both
// capabilities, so the two providers share one Shutdown: releasing the backend
// when the first provider is done would leave the second provider's final
// export — the metrics recorded since the last interval — landing on an
// exporter that has already gone, and the drive would fail with it.
func TestCloseDeliversTheLastBatchOfBothSignals(t *testing.T) {
	d := &destination{}
	buf, err := driveAndRecord(t, map[string]*destination{"observability.test-backend": d})
	if err != nil {
		t.Fatalf("a graceful stop failed: %v\nthe module logged:\n%s", err, buf)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.spans) != 1 || d.spans[0] != "place" {
		t.Errorf("the destination received spans %v, want the one span ended before the stop", d.spans)
	}
	if !contains(d.metrics, "orders.placed") {
		t.Errorf("the destination received metrics %v, want orders.placed: the final collection "+
			"of the meter provider is the only export the metric reached", d.metrics)
	}
	if d.late != 0 {
		t.Errorf("%d export(s) reached the destination after it was released", d.late)
	}
	if !d.isReleased("span") || !d.isReleased("metric") {
		t.Errorf("the destination released %v, want both halves released by the stop", d.released)
	}
}

// TestCloseDeliversTheLastBatchToEveryBackend is the same promise with two
// destinations, each of which is collected once per capability and must still
// receive both signals before either is released.
func TestCloseDeliversTheLastBatchToEveryBackend(t *testing.T) {
	first, second := &destination{}, &destination{}
	buf, err := driveAndRecord(t, map[string]*destination{
		"observability.test-first":  first,
		"observability.test-second": second,
	})
	if err != nil {
		t.Fatalf("a graceful stop failed: %v\nthe module logged:\n%s", err, buf)
	}
	for name, d := range map[string]*destination{"first": first, "second": second} {
		d.mu.Lock()
		if len(d.spans) != 1 || !contains(d.metrics, "orders.placed") || d.late != 0 {
			t.Errorf("the %s destination received spans %v and metrics %v with %d late export(s), "+
				"want one span, orders.placed and none late", name, d.spans, d.metrics, d.late)
		}
		d.mu.Unlock()
	}
}

// boundedConfig gives this module a service name and a short shutdown bound.
type boundedConfig struct{ bound time.Duration }

func (c boundedConfig) Decode(namespace string, target any) error {
	if namespace == otel.ModuleName {
		cfg := target.(*otel.Config)
		cfg.ServiceName = "orders"
		cfg.ShutdownTimeout = c.bound
	}
	return nil
}

// stuckSpans is a destination whose export never returns before it is
// cancelled.
type stuckSpans struct{}

func (stuckSpans) ExportSpans(ctx context.Context, _ []sdktrace.ReadOnlySpan) error {
	<-ctx.Done()
	return ctx.Err()
}
func (stuckSpans) Shutdown(context.Context) error { return nil }

// TestAShutdownPastItsBoundFailsTheStop pins what a flush that does not finish
// within shutdown-timeout reports: Close stops waiting at the bound and returns
// the deadline, so the drive fails and the host sees that the stop did not
// complete. What was lost is not reported.
func TestAShutdownPastItsBoundFailsTheStop(t *testing.T) {
	reg := core.New()
	reg.Supply((*config.Reader)(nil), boundedConfig{bound: 50 * time.Millisecond})
	reg.Supply((*log.Logger)(nil), stubLogger{lg: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))})
	reg.Register(otel.Module())
	reg.Register(core.Module{
		Name:     "observability.test-stuck",
		Provides: []core.Provision{{Token: (*observability.SpanExporter)(nil)}},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) { return stuckSpans{}, nil },
	})

	start := time.Now()
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{otel.ModuleName, "observability.test-stuck"},
		Action: func(ctx context.Context, reg *core.Registry) error {
			tp, err := core.Resolve[observability.TracerProvider](reg)
			if err != nil {
				return err
			}
			_, span := tp.Tracer("orders").Start(ctx, "place")
			span.End()
			return nil
		},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a flush past its bound ended the drive with %v, want the deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the stop took %v against a bound of 50ms", elapsed)
	}
}

func contains(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}
