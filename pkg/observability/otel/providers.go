package otel

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

// serviceNameKey and serviceInstanceIDKey are the resource attributes the
// process is identified under: which service it is, and which running instance
// of it. They are written out rather than taken from a semantic-conventions
// package: the keys are what every backend reads, and pinning them here keeps
// this module out of the version churn those packages carry.
const (
	serviceNameKey       = attribute.Key("service.name")
	serviceInstanceIDKey = attribute.Key("service.instance.id")
)

// newInstanceID returns an identity for one assembly of this module: a random
// version 4 UUID, the form the upstream semantic conventions recommend.
//
// Replicas of one service export the same metric streams with the same
// attributes, and a resource carrying the service name alone gives two
// replicas' cumulative streams one identity: a collector or a metric store then
// sees a single series flapping between their values. A random identity is
// enough to keep them apart, and it changes on every start, which is what an
// instance is. It is generated rather than configured because nothing in a
// deployment's configuration differs between replicas.
func newInstanceID() string {
	var b [16]byte
	// rand.Read never returns an error and always fills the buffer.
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// providers is this module's product.
//
// It carries both capabilities, because the two are built together and handed
// out together: a consumer that resolves either one gets this value, and the
// registry's delivery check requires one product to satisfy every capability
// the descriptor declares.
//
// The two upstream interfaces are embedded rather than implemented. Writing the
// two methods out would not work: each interface embeds an internal one whose
// single method is unexported, so a type outside the library can satisfy them
// only by embedding, never by defining them — the unexported method is not
// something any other package can declare. Embedding also means the product
// gains a method if the library adds one, instead of failing to satisfy the
// interface after an upgrade.
//
// The concrete providers are kept as well, because the interfaces expose no way
// to shut one down: Close needs the SDK types, and an assertion back out of the
// embedded interface would be a worse way to say the same thing.
type providers struct {
	cfg *Config

	observability.TracerProvider
	observability.MeterProvider

	tp *sdktrace.TracerProvider
	mp *sdkmetric.MeterProvider
	// exporters are the backends' exporters as collected, unwrapped, which
	// Close releases once both providers have flushed through them.
	exporters []releasable
	// lg is the logger this module was given, held for the lines the wiring
	// stage writes about a configuration that names the same thing twice.
	lg *slog.Logger
	// duration is the one instrument this module records on. It is built here
	// rather than at registration because building an instrument can report an
	// error, and New is the stage where an error can still be returned.
	duration metric.Float64Histogram
}

// newProviders collects the exporters, wraps them, and builds the two
// providers over the wrapped set.
//
// The wrapping happens before either provider is built, and that order is the
// whole reason the collection is a separate step: a processor or reader holds
// the exporter it was given, so a wrapper applied afterwards would sit outside
// the export path and its log lines would never be written.
//
// A missing logger is a startup failure rather than a fall back to the process
// root logger: a line written there would go to standard output instead of the
// destinations configuration named, and nothing would report the difference.
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

	spanExporters := core.ResolveAll[observability.SpanExporter](reg)
	metricExporters := core.ResolveAll[observability.MetricExporter](reg)
	exporters := make([]releasable, 0, len(spanExporters)+len(metricExporters))
	for _, e := range spanExporters {
		exporters = append(exporters, e)
	}
	for _, e := range metricExporters {
		exporters = append(exporters, e)
	}

	res := resource.NewSchemaless(
		serviceNameKey.String(cfg.ServiceName),
		serviceInstanceIDKey.String(newInstanceID()),
	)

	traceOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		// ParentBased over the ratio, so that a sampled parent keeps its
		// children: sampling per process would break a trace apart mid-flight.
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	}
	for _, e := range spanExporters {
		traceOpts = append(traceOpts, sdktrace.WithBatcher(
			&spanExporter{SpanExporter: e, note: &notifier{lg: lg}},
			sdktrace.WithMaxQueueSize(queueSize(cfg.Batch.MaxQueueSize, sdktrace.DefaultMaxQueueSize)),
			sdktrace.WithMaxExportBatchSize(queueSize(cfg.Batch.MaxExportBatchSize, sdktrace.DefaultMaxExportBatchSize)),
			sdktrace.WithBatchTimeout(cfg.Batch.BatchTimeout),
			sdktrace.WithExportTimeout(cfg.Batch.ExportTimeout),
		))
	}

	metricOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}
	for _, e := range metricExporters {
		metricOpts = append(metricOpts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
			&metricExporter{Exporter: e, note: &notifier{lg: lg}},
			sdkmetric.WithInterval(cfg.Metric.Interval),
			sdkmetric.WithTimeout(cfg.Metric.Timeout),
		)))
	}

	// The two conditions below do not fail, and that is deliberate: a module
	// that produces nothing looks from outside exactly like one that is broken,
	// and a line is the only thing that tells them apart. Neither is visible in
	// the startup diagnostics, which cover modules outside the enabled set
	// rather than a module whose configuration switched it off.
	switch {
	case len(spanExporters) == 0 && len(metricExporters) == 0:
		lg.Warn("no export backend is configured: spans and metrics are produced and dropped")
	case len(spanExporters) == 0:
		lg.Warn("no span export backend is configured: spans are produced and dropped")
	case len(metricExporters) == 0:
		lg.Warn("no metric export backend is configured: metrics are produced and dropped")
	}
	if cfg.SampleRatio == 0 {
		lg.Warn("sample-ratio is 0: no trace is recorded")
	}

	mp := sdkmetric.NewMeterProvider(metricOpts...)
	duration, err := newRequestDuration(mp)
	if err != nil {
		// The meter provider is running its readers, and nothing holds it or
		// the exporters: this failure returns from New, so no product exists
		// for Close to release them through.
		_ = mp.Shutdown(ctx)
		_ = releaseExporters(ctx, exporters)
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(traceOpts...)
	return &providers{
		cfg:            cfg,
		TracerProvider: tp,
		MeterProvider:  mp,
		tp:             tp,
		mp:             mp,
		exporters:      exporters,
		lg:             lg,
		duration:       duration,
	}, nil
}

// queueSize reads a batch size that takes zero to mean the upstream default.
//
// The upstream does not read it that way — it builds a queue of the size given,
// so a zero is a queue that holds nothing — and the substitution happens here
// so that the documented meaning is the implemented one. The fallback is the
// upstream's own constant rather than a number written out, so that this
// module's default moves with the library's.
func queueSize(configured, upstreamDefault int) int {
	if configured == 0 {
		return upstreamDefault
	}
	return configured
}

// releasable is the one method both exporter interfaces share, and the one the
// assembler calls on every exporter it collected.
type releasable interface {
	Shutdown(ctx context.Context) error
}

// releaseExporters shuts down every exporter the assembler collected, after the
// providers have made their final export through them.
//
// The release is here rather than in the providers because a backend's product
// is collected under both capabilities and has one Shutdown for both: a
// provider releasing it would release the other provider's exporter too, ahead
// of that provider's final export. The wrappers therefore hold the providers'
// shutdown calls back, and this runs after both providers are shut down.
//
// A product collected under both capabilities appears here twice. An
// observability.Exporters value releases its pair once and answers the second
// call with the result of the first, which is what the backends hand over.
func releaseExporters(ctx context.Context, exporters []releasable) error {
	var errs []error
	for _, e := range exporters {
		if err := e.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
