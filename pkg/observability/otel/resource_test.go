package otel_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
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

// TestMain gives the process an environment the upstream's default resource can
// be computed in. The upstream computes that resource once per process, on the
// first call, and keeps whatever OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES
// held then: a case that sets either one and happens to be the first to build a
// provider would leave its value in the default for every case after it, and a
// developer's own shell variable would do the same. The variables are cleared
// and the default is computed here, so that what a case sees of the environment
// is only what it set itself.
func TestMain(m *testing.M) {
	for _, name := range []string{"OTEL_SERVICE_NAME", "OTEL_RESOURCE_ATTRIBUTES"} {
		if err := os.Unsetenv(name); err != nil {
			fmt.Fprintf(os.Stderr, "clearing %s: %v\n", name, err)
			os.Exit(1)
		}
	}
	resource.Default()
	os.Exit(m.Run())
}

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

// runReplica drives one assembly of the service named orders, as one replica of
// it would, records a span and a data point, and returns the resources they were
// exported with.
func runReplica(t *testing.T) *resourceRecorder {
	t.Helper()
	return runNamedReplica(t, "orders")
}

// runNamedReplica is runReplica for a configuration that gives the service the
// name it is passed; the empty string is a configuration that gives none.
func runNamedReplica(t *testing.T, serviceName string) *resourceRecorder {
	t.Helper()
	rec := &resourceRecorder{}
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(fixedConfig{serviceName: serviceName}, config.ModuleName)
	reg.Supply(stubLogger{lg: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}, log.ModuleName)
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

// TestTheEnvironmentAddsToTheResourceButCannotRenameIt pins what the upstream's
// resource variables do to the resource this module builds when configuration
// names the service. The upstream merges the attributes of
// OTEL_RESOURCE_ATTRIBUTES into it when the providers are built, which is how a
// platform adds where a replica runs; on a name both give, this module's value
// wins, so neither that variable nor OTEL_SERVICE_NAME can rename a configured
// service or give two replicas one instance.
func TestTheEnvironmentAddsToTheResourceButCannotRenameIt(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "renamed")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=renamed,service.instance.id=shared,deployment.environment=prod")

	rec := runReplica(t)
	for _, res := range []*resource.Resource{rec.spans[0], rec.metrics[0]} {
		if got := resourceValue(t, res, "service.name"); got != "orders" {
			t.Errorf("the service name is %q, want the configured orders", got)
		}
		if got := resourceValue(t, res, "service.instance.id"); got == "shared" {
			t.Errorf("the instance identity is the environment's %q, want this assembly's own", got)
		}
		if got := resourceValue(t, res, "deployment.environment"); got != "prod" {
			t.Errorf("deployment.environment is %q, want the attribute the environment added", got)
		}
	}
}

// TestAnUnnamedServiceTakesItsNameFromTheUpstream pins what a configuration
// that gives no service name ends up reported as, in the order the upstream
// settles it: OTEL_SERVICE_NAME, then the service.name in
// OTEL_RESOURCE_ATTRIBUTES, then unknown_service:<executable name>. Each case
// is one environment set after the default resource was computed in TestMain,
// so the default still carries the fallback name; a name reaches the resource
// only through the environment layer, and the case that sets both variables
// holds the order between them.
func TestAnUnnamedServiceTakesItsNameFromTheUpstream(t *testing.T) {
	cases := []struct {
		name       string
		serviceVar string
		attrsVar   string
		want       string // a prefix when it ends in a colon
	}{
		{name: "nothing set", want: "unknown_service:"},
		{name: "the service name variable", serviceVar: "fromvar", want: "fromvar"},
		{name: "the resource attributes variable", attrsVar: "service.name=fromattrs,team=ops", want: "fromattrs"},
		{name: "both variables", serviceVar: "fromvar", attrsVar: "service.name=fromattrs", want: "fromvar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_SERVICE_NAME", tc.serviceVar)
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", tc.attrsVar)

			rec := runNamedReplica(t, "")
			for signal, res := range map[string]*resource.Resource{"span": rec.spans[0], "metric": rec.metrics[0]} {
				got := resourceValue(t, res, "service.name")
				if strings.HasSuffix(tc.want, ":") {
					if !strings.HasPrefix(got, tc.want) || len(got) == len(tc.want) {
						t.Errorf("the %s resource's service name is %q, want %s followed by the executable's name",
							signal, got, tc.want)
					}
					continue
				}
				if got != tc.want {
					t.Errorf("the %s resource's service name is %q, want %q", signal, got, tc.want)
				}
			}
		})
	}
}

// TestAnUnnamedServiceStillHasItsOwnInstance pins that leaving the name to the
// upstream leaves the instance identity with this module: the environment's
// service.instance.id does not stand in for it, and the fallback does not
// remove it.
func TestAnUnnamedServiceStillHasItsOwnInstance(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.instance.id=shared")

	rec := runNamedReplica(t, "")
	for _, res := range []*resource.Resource{rec.spans[0], rec.metrics[0]} {
		if got := resourceValue(t, res, "service.instance.id"); got == "" || got == "shared" {
			t.Errorf("the instance identity is %q, want this assembly's own", got)
		}
	}
}

// TestTheResourceNamesTheSDK pins the telemetry.sdk attributes a backend reads
// to tell which library produced a signal. They come with the upstream's default
// resource, and a resource built from this module's attributes alone leaves them
// out, on both signals.
func TestTheResourceNamesTheSDK(t *testing.T) {
	rec := runReplica(t)
	for signal, res := range map[string]*resource.Resource{"span": rec.spans[0], "metric": rec.metrics[0]} {
		if got := resourceValue(t, res, "telemetry.sdk.name"); got != "opentelemetry" {
			t.Errorf("the %s resource's telemetry.sdk.name is %q, want opentelemetry", signal, got)
		}
		if got := resourceValue(t, res, "telemetry.sdk.language"); got != "go" {
			t.Errorf("the %s resource's telemetry.sdk.language is %q, want go", signal, got)
		}
		if got := resourceValue(t, res, "telemetry.sdk.version"); got == "" {
			t.Errorf("the %s resource's telemetry.sdk.version is empty", signal)
		}
	}
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
