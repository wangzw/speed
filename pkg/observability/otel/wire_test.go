package otel

import (
	"context"
	"log/slog"
	nethttp "net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

// wiredConfig configures this module to instrument the endpoint the entry module
// declares, and gives the entry module that endpoint.
type wiredConfig struct{ address string }

func (c wiredConfig) Decode(namespace string, target any) error {
	if namespace == ModuleName {
		cfg := target.(*Config)
		cfg.ServiceName = "orders"
		cfg.Endpoints = []string{entryName}
		return nil
	}
	return jsonConfig(`{"listeners":{"`+entryName+`":{"address":"`+c.address+`"}},`+
		`"endpoints":{"`+entryName+`":{"listener":"`+entryName+`"}}}`).Decode(namespace, target)
}

// recordingMetricExporter keeps the names of the instruments that reached it.
type recordingMetricExporter struct {
	mu    sync.Mutex
	names []string
}

func (*recordingMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(kind)
}

func (*recordingMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (m *recordingMetricExporter) Export(_ context.Context, rm *metricdata.ResourceMetrics) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sm := range rm.ScopeMetrics {
		for _, metric := range sm.Metrics {
			m.names = append(m.names, metric.Name)
		}
	}
	return nil
}

func (*recordingMetricExporter) ForceFlush(context.Context) error { return nil }
func (*recordingMetricExporter) Shutdown(context.Context) error   { return nil }

// TestTheAssemblerPutsTheLayerOnTheEndpointsTheConfigurationNames is the layer's
// registration end to end: the assembler, configured with an endpoint name, takes
// the entry module's router up in Init and registers the layer on that endpoint,
// and a request to a route another module registered there leaves a server span
// and a data point of the request histogram at the export backend. Nothing in the
// case registers the layer by hand, so a layer the entry module refused at
// registration, or a registration that never happened, ends the case.
//
// The batch and the metric interval are the defaults, so nothing is exported while
// the request is served: what reaches the backend got there through the shutdown.
func TestTheAssemblerPutsTheLayerOnTheEndpointsTheConfigurationNames(t *testing.T) {
	spans := &recordingSpanExporter{}
	metrics := &recordingMetricExporter{}
	addr := freeAddress(t)

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(wiredConfig{address: addr}, config.ModuleName)
	reg.Supply(entryLogger{lg: slog.New(slog.NewTextHandler(&syncBuffer{}, nil))}, log.ModuleName)
	reg.Register(speedhttp.Module())
	reg.Register(Module())
	reg.Register(core.Module{
		Name: "test.backend",
		Provides: []core.Provision{
			{Token: (*observability.SpanExporter)(nil)},
			{Token: (*observability.MetricExporter)(nil)},
		},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) {
			return &observability.Exporters{Span: spans, Metric: metrics}, nil
		},
	})
	reg.Register(core.Module{
		Name:     "test.registrant",
		Requires: []core.Requirement{{Token: (*speedhttp.Router)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			ep, err := router.Endpoint(entryName)
			if err != nil {
				return err
			}
			ep.Route(nethttp.MethodGet, "/ping", func(c *gin.Context) { c.Status(nethttp.StatusNoContent) })
			return nil
		},
	})

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageServe,
		Participants: []string{ModuleName, speedhttp.ModuleName, "test.backend", "test.registrant"},
		Action: func(context.Context, *core.Registry) error {
			h := &entryHost{
				base: "http://" + addr,
				client: &nethttp.Client{
					Timeout:   5 * time.Second,
					Transport: &nethttp.Transport{DisableKeepAlives: true},
				},
			}
			if got := h.status(t, nethttp.MethodGet, "/ping"); got != nethttp.StatusNoContent {
				t.Errorf("the request was answered %d, want the 204 the route writes", got)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}

	var names []string
	for _, span := range spans.recorded() {
		names = append(names, span.Name())
	}
	if want := []string{"GET /ping"}; !slices.Equal(names, want) {
		t.Errorf("the backend received the spans %v, want %v: the layer was not on the endpoint", names, want)
	}
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if !slices.Contains(metrics.names, requestDurationName) {
		t.Errorf("the backend received the instruments %v, want %s among them", metrics.names, requestDurationName)
	}
}
