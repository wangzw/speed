package otel

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// Config is what this module accepts from configuration.
type Config struct {
	// ServiceName is the upstream service.name resource attribute.
	ServiceName string `config:"service-name"`
	// SampleRatio is the fraction of the traces this process starts that are
	// recorded, in [0,1].
	SampleRatio float64 `config:"sample-ratio"`
	// Batch configures the span batch processor.
	Batch Batch `config:"batch"`
	// Metric configures the metric reader period.
	Metric Metric `config:"metric"`
	// ShutdownTimeout bounds the flush of both providers at Close, the
	// exports made from the start of Close included, and not the release of
	// the exporters after it, which has a bound of its own.
	ShutdownTimeout time.Duration `config:"shutdown-timeout"`
	// Endpoints names the listening endpoints the request layer is registered
	// on.
	Endpoints []string `config:"endpoints"`
}

// Batch configures the span batch processor.
type Batch struct {
	// MaxQueueSize is how many spans may wait before the processor drops. Zero
	// takes the upstream default.
	MaxQueueSize int `config:"max-queue-size"`
	// MaxExportBatchSize is how many spans leave in one batch. Zero takes the
	// upstream default.
	MaxExportBatchSize int `config:"max-export-batch-size"`
	// BatchTimeout is how long a batch may wait before it is sent.
	BatchTimeout time.Duration `config:"batch-timeout"`
	// ExportTimeout bounds one export call. Once Close has begun, the cutoff
	// inside ShutdownTimeout may end the call sooner.
	ExportTimeout time.Duration `config:"export-timeout"`
}

// Metric configures the periodic reader that pushes metrics.
type Metric struct {
	// Interval is the time between exports.
	Interval time.Duration `config:"interval"`
	// Timeout bounds one export call. Once Close has begun, the cutoff inside
	// ShutdownTimeout may end the call sooner.
	Timeout time.Duration `config:"timeout"`
}

const allEndpoints = "*"

const (
	defaultSampleRatio     = 1.0
	defaultBatchTimeout    = 5 * time.Second
	defaultExportTimeout   = 30 * time.Second
	defaultMetricInterval  = 60 * time.Second
	defaultMetricTimeout   = 30 * time.Second
	defaultShutdownTimeout = 10 * time.Second
)

func prototype() *Config {
	return &Config{
		SampleRatio: defaultSampleRatio,
		Batch: Batch{
			BatchTimeout:  defaultBatchTimeout,
			ExportTimeout: defaultExportTimeout,
		},
		Metric: Metric{
			Interval: defaultMetricInterval,
			Timeout:  defaultMetricTimeout,
		},
		ShutdownTimeout: defaultShutdownTimeout,
		Endpoints:       []string{allEndpoints},
	}
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ModuleName,
		Mounts:    []config.Mount{{Value: prototype()}},
		Items: map[string]config.Item{
			"service-name": {
				Description: "the service.name reported to the telemetry backend; when unset, " +
					"the upstream settles the name from OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES, " +
					"else unknown_service:<executable name>",
			},
		},
	}
}

func read(reg *core.Registry) (*Config, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: this module's input items are read from configuration, and the host "+
			"handed no reading face into the registry before assembly began: %w", ModuleName, err)
	}
	cfg := prototype()
	if err := reader.Decode(ModuleName, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	// NaN is outside [0,1] and yet compares false against both bounds, so the
	// interval test alone lets it through.
	if math.IsNaN(c.SampleRatio) || c.SampleRatio < 0 || c.SampleRatio > 1 {
		return fmt.Errorf("%w: got %v", ErrInvalidSampleRatio, c.SampleRatio)
	}
	if c.Batch.MaxQueueSize < 0 || c.Batch.MaxExportBatchSize < 0 {
		return fmt.Errorf("%w: queue size %d, export batch size %d; zero takes the upstream default",
			ErrInvalidBatchParam, c.Batch.MaxQueueSize, c.Batch.MaxExportBatchSize)
	}
	if len(c.Endpoints) > 1 && slices.Contains(c.Endpoints, allEndpoints) {
		return fmt.Errorf("%w: got %q; write [%q] alone for every declared endpoint, or list the endpoint "+
			"names without %q", ErrWildcardBesideEndpointNames, c.Endpoints, allEndpoints, allEndpoints)
	}
	durations := []struct {
		name  string
		value time.Duration
	}{
		{"batch.batch-timeout", c.Batch.BatchTimeout},
		{"batch.export-timeout", c.Batch.ExportTimeout},
		{"metric.interval", c.Metric.Interval},
		{"metric.timeout", c.Metric.Timeout},
		{"shutdown-timeout", c.ShutdownTimeout},
	}
	for _, d := range durations {
		if d.value <= 0 {
			return fmt.Errorf("%w: %s is %v", ErrInvalidDuration, d.name, d.value)
		}
	}
	return nil
}
