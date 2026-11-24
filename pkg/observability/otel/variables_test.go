package otel_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"slices"
	"strings"
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

const ignoredLineText = "standard OpenTelemetry variables are set and have no effect"

func assembledLog(t *testing.T, backends ...core.Module) string {
	t.Helper()
	buf := &bytes.Buffer{}
	dropTime := func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) == 0 && a.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return a
	}
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(fixedConfig{serviceName: "orders"}, config.ModuleName)
	reg.Supply(stubLogger{lg: slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{ReplaceAttr: dropTime}))},
		log.ModuleName)
	reg.Register(otel.Module())
	participants := []string{otel.ModuleName}
	for _, b := range backends {
		reg.Register(b)
		participants = append(participants, b.Name)
	}
	if err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageNew,
		Participants: participants,
	}); err != nil {
		t.Fatalf("drive: %v", err)
	}
	return buf.String()
}

func linesWith(logged, text string) []string {
	var found []string
	for line := range strings.SplitSeq(logged, "\n") {
		if strings.Contains(line, text) {
			found = append(found, line)
		}
	}
	return found
}

var offTheList = map[string]string{
	"OTEL_SERVICE_NAME":               "named-by-the-environment",
	"OTEL_RESOURCE_ATTRIBUTES":        "deployment.environment=staging",
	"OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT": "64",
	"OTEL_METRICS_EXEMPLAR_FILTER":    "always_off",
	"OTEL_EXPORTER_OTLP_TIMEOUT":      "5000",
}

// TestASetStandardVariableThisModuleOverridesIsNamedOnce pins the line
// that tells a deployment its variable has no effect.
func TestASetStandardVariableThisModuleOverridesIsNamedOnce(t *testing.T) {
	overridden := []struct{ name, value, item string }{
		{"OTEL_TRACES_SAMPLER", "parentbased_traceidratio", otel.ModuleName + ".sample-ratio"},
		{"OTEL_TRACES_SAMPLER_ARG", "0.04173", otel.ModuleName + ".sample-ratio"},
		{"OTEL_BSP_SCHEDULE_DELAY", "41731", otel.ModuleName + ".batch.batch-timeout"},
		{"OTEL_BSP_EXPORT_TIMEOUT", "41732", otel.ModuleName + ".batch.export-timeout"},
		{"OTEL_BSP_MAX_QUEUE_SIZE", "41733", otel.ModuleName + ".batch.max-queue-size"},
		{"OTEL_BSP_MAX_EXPORT_BATCH_SIZE", "41734", otel.ModuleName + ".batch.max-export-batch-size"},
		{"OTEL_METRIC_EXPORT_INTERVAL", "41735", otel.ModuleName + ".metric.interval"},
		{"OTEL_METRIC_EXPORT_TIMEOUT", "41736", otel.ModuleName + ".metric.timeout"},
	}
	for _, v := range overridden {
		t.Setenv(v.name, v.value)
	}
	for name, value := range offTheList {
		t.Setenv(name, value)
	}

	for _, assembly := range []struct {
		name     string
		backends []core.Module
	}{
		{"no backend", nil},
		{"both exports", []core.Module{timedBackend(&timedDestination{})}},
	} {
		t.Run(assembly.name, func(t *testing.T) {
			logged := assembledLog(t, assembly.backends...)
			lines := linesWith(logged, ignoredLineText)
			if len(lines) != 1 {
				t.Fatalf("the module logged:\n%s\nwant exactly one line naming the variables it overrides", logged)
			}
			line := lines[0]
			if !strings.Contains(line, "level=WARN") {
				t.Errorf("the line is %q, want a warning", line)
			}
			for _, v := range overridden {
				if !strings.Contains(line, v.name+": "+v.item) {
					t.Errorf("the line is %q, want it to name %s and the item %s", line, v.name, v.item)
				}
			}
			for _, leaked := range []string{"parentbased_traceidratio", "0.04173", "4173"} {
				if strings.Contains(logged, leaked) {
					t.Errorf("the log repeats the value %q:\n%s", leaked, logged)
				}
			}
			for name := range offTheList {
				if strings.Contains(logged, name) {
					t.Errorf("the log names %s, which is not on the list:\n%s", name, logged)
				}
			}
		})
	}
}

// TestNoLineWhenNoOverriddenVariableIsSet pins the other side.
func TestNoLineWhenNoOverriddenVariableIsSet(t *testing.T) {
	for _, name := range []string{
		"OTEL_TRACES_SAMPLER", "OTEL_BSP_SCHEDULE_DELAY", "OTEL_BSP_EXPORT_TIMEOUT",
		"OTEL_BSP_MAX_QUEUE_SIZE", "OTEL_BSP_MAX_EXPORT_BATCH_SIZE", "OTEL_METRIC_EXPORT_TIMEOUT",
	} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unsetting %s: %v", name, err)
		}
	}
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "  ")
	for name, value := range offTheList {
		t.Setenv(name, value)
	}

	if lines := linesWith(assembledLog(t), ignoredLineText); len(lines) != 0 {
		t.Errorf("the module logged %q, want no line: no overridden variable is set", lines)
	}
}

type timedDestination struct {
	mu          sync.Mutex
	spanBatches []int
	spanLeft    []time.Duration
	metricLeft  []time.Duration
}

// left is how long a context has before its deadline, and an hour when it has
// none, which is longer than any bound a case here judges against.
func left(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return time.Until(deadline)
	}
	return time.Hour
}

type timedSpans struct{ d *timedDestination }

func (s timedSpans) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	s.d.spanBatches = append(s.d.spanBatches, len(spans))
	s.d.spanLeft = append(s.d.spanLeft, left(ctx))
	return nil
}

func (timedSpans) Shutdown(context.Context) error { return nil }

type timedMetrics struct{ d *timedDestination }

func (timedMetrics) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(kind)
}

func (timedMetrics) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (m timedMetrics) Export(ctx context.Context, _ *metricdata.ResourceMetrics) error {
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	m.d.metricLeft = append(m.d.metricLeft, left(ctx))
	return nil
}

func (timedMetrics) ForceFlush(context.Context) error { return nil }
func (timedMetrics) Shutdown(context.Context) error   { return nil }

func timedBackend(d *timedDestination) core.Module {
	return core.Module{
		Name: "observability.timed-backend",
		Provides: []core.Provision{
			{Token: (*observability.SpanExporter)(nil)},
			{Token: (*observability.MetricExporter)(nil)},
		},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) {
			return &observability.Exporters{Span: timedSpans{d}, Metric: timedMetrics{d}}, nil
		},
	}
}

func driveTimed(t *testing.T, d *timedDestination, reader config.Reader,
	action func(context.Context, *core.Registry) error,
) {
	t.Helper()
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Supply(stubLogger{lg: slog.New(slog.DiscardHandler)}, log.ModuleName)
	reg.Register(otel.Module())
	reg.Register(timedBackend(d))
	if err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{otel.ModuleName, "observability.timed-backend"},
		Action:       action,
	}); err != nil {
		t.Fatalf("drive: %v", err)
	}
}

const envBound = time.Millisecond

// TestNoUpstreamVariableOverridesTheProviderConfiguration pins the
// premise the warning states.
func TestNoUpstreamVariableOverridesTheProviderConfiguration(t *testing.T) {
	t.Setenv("OTEL_TRACES_SAMPLER", "traceidratio")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0")
	t.Setenv("OTEL_BSP_SCHEDULE_DELAY", "1")
	t.Setenv("OTEL_BSP_EXPORT_TIMEOUT", "1")
	t.Setenv("OTEL_BSP_MAX_QUEUE_SIZE", "1")
	t.Setenv("OTEL_BSP_MAX_EXPORT_BATCH_SIZE", "1")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "1")
	t.Setenv("OTEL_METRIC_EXPORT_TIMEOUT", "1")

	d := &timedDestination{}
	driveTimed(t, d, fixedConfig{serviceName: "orders"}, func(ctx context.Context, reg *core.Registry) error {
		tp, err := core.Resolve[observability.TracerProvider](reg)
		if err != nil {
			return err
		}
		for _, name := range []string{"first", "second", "third"} {
			_, span := tp.Tracer("orders").Start(ctx, name)
			span.End()
		}
		// The configured batch timeout is seconds and the metric interval a
		// minute; under the variables both would have exported many times
		// over by the end of this wait.
		time.Sleep(200 * time.Millisecond)
		d.mu.Lock()
		defer d.mu.Unlock()
		if len(d.spanBatches) != 0 || len(d.metricLeft) != 0 {
			t.Errorf("while the command ran, %d span exports and %d metric exports happened, want none: "+
				"the schedule delay or the metric interval came from the environment",
				len(d.spanBatches), len(d.metricLeft))
		}
		return nil
	})

	d.mu.Lock()
	defer d.mu.Unlock()
	if !slices.Equal(d.spanBatches, []int{3}) {
		t.Errorf("the spans arrived in batches of %v, want one batch of 3: the sampler, the ratio, the queue "+
			"size or the export batch size came from the environment", d.spanBatches)
	}
	for _, remaining := range d.spanLeft {
		if remaining <= envBound {
			t.Errorf("a span export had %v left, want the configured bound: the export timeout came from the "+
				"environment", remaining)
		}
	}
}

// TestNoUpstreamVariableOverridesTheMetricTimeout pins the one overridden
// variable a stop does not reach.
func TestNoUpstreamVariableOverridesTheMetricTimeout(t *testing.T) {
	t.Setenv("OTEL_METRIC_EXPORT_TIMEOUT", "1")

	d := &timedDestination{}
	driveTimed(t, d, tunedConfig{serviceName: "orders", period: 50 * time.Millisecond},
		func(context.Context, *core.Registry) error {
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				d.mu.Lock()
				exported := len(d.metricLeft)
				d.mu.Unlock()
				if exported > 0 {
					return nil
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Error("no periodic metric export happened within 5s at a 50ms interval: the timeout came from " +
				"the environment and every collection ran out of time")
			return nil
		})

	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.metricLeft) == 0 {
		return
	}
	if remaining := d.metricLeft[0]; remaining <= envBound {
		t.Errorf("the periodic metric export had %v left, want the configured timeout: the timeout came "+
			"from the environment", remaining)
	}
}
