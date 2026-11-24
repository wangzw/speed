package otel_test

import (
	"bytes"
	"context"
	"errors"
	stdlog "log"
	"log/slog"
	"os"
	"strings"
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

// tunedConfig is a reading face that gives this module a service name and short
// export periods, so that a case sees several export attempts in well under a
// second.
type tunedConfig struct {
	serviceName string
	period      time.Duration
}

func (c tunedConfig) Decode(namespace string, target any) error {
	if namespace == otel.ModuleName {
		cfg := target.(*otel.Config)
		cfg.ServiceName = c.serviceName
		cfg.Batch.BatchTimeout = c.period
		cfg.Metric.Interval = c.period
	}
	return nil
}

var errUnreachable = errors.New("collector unreachable")

type unreachableSpans struct{}

func (unreachableSpans) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return errUnreachable
}
func (unreachableSpans) Shutdown(context.Context) error { return nil }

type unreachableMetrics struct{}

func (unreachableMetrics) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(kind)
}

func (unreachableMetrics) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (unreachableMetrics) Export(context.Context, *metricdata.ResourceMetrics) error {
	return errUnreachable
}
func (unreachableMetrics) ForceFlush(context.Context) error { return nil }
func (unreachableMetrics) Shutdown(context.Context) error   { return nil }

// TestAnExportFailureIsReportedOnlyByTheWrapper is the case for the one
// report a failing destination produces.
func TestAnExportFailureIsReportedOnlyByTheWrapper(t *testing.T) {
	// The upstream's default handler writes through the standard library's
	// logger, so that is where its lines are caught.
	var upstream bytes.Buffer
	stdlog.SetOutput(&upstream)
	t.Cleanup(func() { stdlog.SetOutput(os.Stderr) })

	var module bytes.Buffer
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(tunedConfig{serviceName: "orders", period: 10 * time.Millisecond}, config.ModuleName)
	reg.Supply(stubLogger{lg: slog.New(slog.NewTextHandler(&module, nil))}, log.ModuleName)
	reg.Register(otel.Module())
	reg.Register(core.Module{
		Name: "observability.test-unreachable",
		Provides: []core.Provision{
			{Token: (*observability.SpanExporter)(nil)},
			{Token: (*observability.MetricExporter)(nil)},
		},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) {
			return &observability.Exporters{Span: unreachableSpans{}, Metric: unreachableMetrics{}}, nil
		},
	})

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{otel.ModuleName, "observability.test-unreachable"},
		Action: func(ctx context.Context, reg *core.Registry) error {
			tp, err := core.Resolve[observability.TracerProvider](reg)
			if err != nil {
				return err
			}
			mp, err := core.Resolve[observability.MeterProvider](reg)
			if err != nil {
				return err
			}
			counter, err := mp.Meter("orders").Int64Counter("orders.placed")
			if err != nil {
				return err
			}
			// A span and a data point per period, over many periods, so that
			// both the processor and the reader fail repeatedly.
			for range 20 {
				_, span := tp.Tracer("orders").Start(ctx, "place")
				span.End()
				counter.Add(ctx, 1)
				time.Sleep(10 * time.Millisecond)
			}
			return nil
		},
	})
	if err != nil {
		t.Errorf("the stop failed over a destination that was unreachable: %v; an export failure is "+
			"reported by the wrapper's line, not by Close", err)
	}
	// The upstream's own internal errors are allowed here; only the failed
	// export is not. So the check is for this failure's error text, not for the
	// buffer being empty.
	if failure := errUnreachable.Error(); strings.Contains(upstream.String(), failure) {
		t.Errorf("the upstream error handler reported the failed export to standard error:\n%s\n"+
			"want the failing exporter's error (%q) to reach it nowhere: the wrapper absorbs the export "+
			"failure, so a destination that is down cannot write a line here", upstream.String(), failure)
	}
	if got := strings.Count(module.String(), "telemetry export failed"); got != 2 {
		t.Errorf("the module logged %d first-failure lines, want 2, one for each wrapper:\n%s",
			got, module.String())
	}
}
