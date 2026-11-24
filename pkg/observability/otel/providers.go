package otel

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

const (
	serviceNameKey       = attribute.Key("service.name")
	serviceInstanceIDKey = attribute.Key("service.instance.id")
)

// newInstanceID returns an identity for one assembly of this module: a
// random version 4 UUID, the form the upstream semantic conventions
// recommend.
func newInstanceID() string {
	var b [16]byte
	// rand.Read never returns an error and always fills the buffer.
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// newResource builds the resource both providers carry, in three layers,
// each overriding the one before it: the upstream's default resource, the
// environment as it is now, and the attributes this module gives.
func newResource(serviceName string) (*resource.Resource, error) {
	own := []attribute.KeyValue{serviceInstanceIDKey.String(newInstanceID())}
	if serviceName != "" {
		own = append(own, serviceNameKey.String(serviceName))
	}
	return layered(resource.Default(), resource.Environment(), resource.NewSchemaless(own...))
}

// layered merges the resources so that each overrides the ones before it.
func layered(layers ...*resource.Resource) (*resource.Resource, error) {
	merged := resource.Empty()
	for _, layer := range layers {
		var err error
		if merged, err = resource.Merge(merged, layer); err != nil {
			return nil, err
		}
	}
	return merged, nil
}

type providers struct {
	cfg *Config

	observability.TracerProvider
	observability.MeterProvider

	tp *sdktrace.TracerProvider
	mp *sdkmetric.MeterProvider
	// exporters are the backends' exporters as collected, unwrapped, which
	// Close releases once both providers have flushed through them.
	exporters []releasable
	cut       *cutoff
	lg        *slog.Logger
}

func newProviders(ctx context.Context, reg *core.Registry) (any, error) {
	logger, err := core.Resolve[log.Logger](reg)
	if err != nil {
		return nil, err
	}
	cfg, err := read(reg)
	if err != nil {
		return nil, err
	}
	if err = cfg.validate(); err != nil {
		return nil, err
	}
	lg := logger.Named(ModuleName)

	// The resource does not depend on the exporters, so it is built before they
	// are collected: a failure here has nothing to release.
	res, err := newResource(cfg.ServiceName)
	if err != nil {
		return nil, fmt.Errorf("%s: building the resource both providers carry: %w", ModuleName, err)
	}

	spanExporters := core.ResolveAll[observability.SpanExporter](reg)
	metricExporters := core.ResolveAll[observability.MetricExporter](reg)
	exporters := make([]releasable, 0, len(spanExporters)+len(metricExporters))
	for _, e := range spanExporters {
		exporters = append(exporters, e)
	}
	for _, e := range metricExporters {
		exporters = append(exporters, e)
	}

	traceOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		// ParentBased over the ratio, so that a sampled parent keeps its
		// children: sampling per process would break a trace apart mid-flight.
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	}
	cut := newCutoff()
	for _, e := range spanExporters {
		traceOpts = append(traceOpts, sdktrace.WithBatcher(
			&spanExporter{SpanExporter: e, note: &notifier{lg: lg}, cut: cut},
			sdktrace.WithMaxQueueSize(queueSize(cfg.Batch.MaxQueueSize, sdktrace.DefaultMaxQueueSize)),
			sdktrace.WithMaxExportBatchSize(queueSize(cfg.Batch.MaxExportBatchSize, sdktrace.DefaultMaxExportBatchSize)),
			sdktrace.WithBatchTimeout(cfg.Batch.BatchTimeout),
			sdktrace.WithExportTimeout(cfg.Batch.ExportTimeout),
		))
	}

	metricOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}
	for _, e := range metricExporters {
		metricOpts = append(metricOpts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
			&metricExporter{Exporter: e, note: &notifier{lg: lg}, cut: cut},
			sdkmetric.WithInterval(cfg.Metric.Interval),
			sdkmetric.WithTimeout(cfg.Metric.Timeout),
		)))
	}

	// The two conditions below do not fail, and that is deliberate: a module
	// that produces nothing looks from outside exactly like one that is
	// broken, and a line is the only thing that tells them apart.
	switch {
	case len(spanExporters) == 0 && len(metricExporters) == 0:
		lg.Warn("no export backend is configured: spans and metrics are produced and dropped")
	case len(spanExporters) == 0:
		lg.Warn("no span export backend is configured: spans are produced and dropped")
	case len(metricExporters) == 0:
		lg.Warn("no metric export backend is configured: metrics are produced and dropped")
	}
	if cfg.SampleRatio == 0 {
		lg.Warn("sample-ratio is 0: no trace is started here, and a request whose caller sampled its trace is still recorded")
	}
	// A standard variable set for a setting given above is not an error: the
	// configured value is in force, and the line says so.
	warnIgnoredVariables(lg, os.LookupEnv)

	mp := sdkmetric.NewMeterProvider(metricOpts...)
	tp := sdktrace.NewTracerProvider(traceOpts...)
	return &providers{
		cfg:            cfg,
		TracerProvider: tp,
		MeterProvider:  mp,
		tp:             tp,
		mp:             mp,
		exporters:      exporters,
		cut:            cut,
		lg:             lg,
	}, nil
}

// queueSize reads a batch size that takes zero to mean the upstream
// default.
func queueSize(configured, upstreamDefault int) int {
	if configured == 0 {
		return upstreamDefault
	}
	return configured
}

// releaseTimeout bounds the release of every exporter the assembler
// collected.
const releaseTimeout = 3 * time.Second

// cutoffMarginDivisor places a stop's cutoff inside shutdown-timeout: the
// exports are cut a tenth of the bound before the bound ends
// (exportCutoff).
const cutoffMarginDivisor = 10

// exportCutoff is how long after Close begins the exports are cut, for a
// shutdown bound of the given length.
func exportCutoff(bound time.Duration) time.Duration {
	return bound - bound/cutoffMarginDivisor
}

type releasable interface {
	Shutdown(ctx context.Context) error
}

// releaseExporters shuts down every exporter the assembler collected,
// after the providers have made their final export through them.
func releaseExporters(ctx context.Context, exporters []releasable) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()

	var errs []error
	for _, e := range exporters {
		if err := e.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
