package otel

import (
	"fmt"
	"math"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// Config is what this module accepts from configuration.
//
// Every value here is needed before the database is reachable — the providers
// are built in New, which precedes Migrate — so all of it is configuration and
// none of it is a runtime setting. Nothing here is taken per tenant either: the
// service identity and the export destination are facts about the process, and
// a per-tenant endpoint would report one process's signals under two identities.
//
// The fields are values rather than pointers because the defaults ride on the
// prototype: an input item no source gave a value for keeps what the prototype
// holds, and the containers here are single structs, whose leaves are overridden
// one by one rather than replaced wholesale. A pointer would have to be told
// apart from nil at every use to say the same thing.
type Config struct {
	// ServiceName is the upstream service.name resource attribute. An empty
	// value, whether no source gave one or a source gave the empty string, is
	// not a name this module sets: the resource then carries the name the
	// upstream settles on without it, from the environment variables it reads
	// (OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES) or, when they give none,
	// its unknown_service:<executable name> fallback. The two empties cannot be
	// told apart after decoding, and nothing here needs them to be.
	ServiceName string `config:"service-name"`
	// SampleRatio is the fraction of the traces this process starts that are
	// recorded, in [0,1]. A request that arrives with a parent follows the
	// parent's decision instead, so zero is legal and stops this process from
	// starting a trace, not from recording one a caller sampled. SampleRatio is
	// the ratio rather than a switch so that a deployment can dial it down on
	// the way to switching it off.
	SampleRatio float64 `config:"sample-ratio"`
	// Batch configures the span batch processor.
	Batch Batch `config:"batch"`
	// Metric configures the metric reader period.
	Metric Metric `config:"metric"`
	// ShutdownTimeout bounds the flush of both providers at Close. It has to
	// come from configuration: the context a Close callback receives has its
	// cancellation stripped, so a bound cannot be expressed there.
	ShutdownTimeout time.Duration `config:"shutdown-timeout"`
	// Endpoints names the listening endpoints the request layer is registered
	// on. Empty means none, and the module then takes no route registration
	// surface up at all — a process without an entry point is a shape this
	// framework supports and this module must not break.
	Endpoints []string `config:"endpoints"`
	// Events names the event types that are turned into spans. Empty means no
	// subscription, and the module then takes no bus up.
	Events []string `config:"events"`
}

// Batch configures the span batch processor.
//
// The two integer items take zero to mean the upstream default. That is not how
// the upstream reads them — it reads zero as a queue of capacity zero — so this
// module substitutes the upstream's own default constants rather than passing a
// zero through. The constants are named rather than written out, so a change
// upstream moves this module's default with it instead of leaving the two
// disagreeing.
type Batch struct {
	// MaxQueueSize is how many spans may wait before the processor drops. Zero
	// takes the upstream default.
	MaxQueueSize int `config:"max-queue-size"`
	// MaxExportBatchSize is how many spans leave in one batch. Zero takes the
	// upstream default.
	MaxExportBatchSize int `config:"max-export-batch-size"`
	// BatchTimeout is how long a batch may wait before it is sent.
	BatchTimeout time.Duration `config:"batch-timeout"`
	// ExportTimeout bounds one export call.
	ExportTimeout time.Duration `config:"export-timeout"`
}

// Metric configures the periodic reader that pushes metrics.
type Metric struct {
	// Interval is the time between exports.
	Interval time.Duration `config:"interval"`
	// Timeout bounds one export call.
	Timeout time.Duration `config:"timeout"`
}

// Defaults for the durations. The two integer items have no entry here: their
// default is the upstream's, and this module does not restate it.
const (
	defaultSampleRatio     = 1.0
	defaultBatchTimeout    = 5 * time.Second
	defaultExportTimeout   = 30 * time.Second
	defaultMetricInterval  = 60 * time.Second
	defaultMetricTimeout   = 30 * time.Second
	defaultShutdownTimeout = 10 * time.Second
)

// prototype is the carrier the input items are declared from: its field values
// are the defaults, and an item no source supplies keeps them.
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
	}
}

// schema declares this module's input items.
//
// Only the service name is described here, because what it falls back to is not
// readable from its default. The origins of every item are the ordinary pair —
// primary source and environment — which is what the zero value of config.Item
// stands for, and no item of this namespace takes a value from the command line:
// the endpoint locator and the headers would then appear in the process list,
// and the rest describe a deployment, which is what the environment layer is
// for.
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

// read decodes this module's namespace into a fresh carrier.
//
// The copy is what makes Prepare and each of the later stages independent: both
// read, and a shared carrier would have the second read write over the first
// one's values.
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

// validate reports the values this module cannot start with.
//
// Every judgement here is about a value that would otherwise produce a running
// module doing nothing useful: a ratio that samples nothing without saying so,
// a queue that cannot hold a span.
func (c *Config) validate() error {
	// NaN is outside [0,1] and yet compares false against both bounds, so the
	// interval test alone lets it through. A NaN ratio is worth refusing here
	// because it is not the zero the sampler switch-off path announces: it
	// builds a sampler that drops every locally-rooted span while no line
	// reports the module as producing nothing.
	if math.IsNaN(c.SampleRatio) || c.SampleRatio < 0 || c.SampleRatio > 1 {
		return fmt.Errorf("%w: got %v", ErrInvalidSampleRatio, c.SampleRatio)
	}
	if c.Batch.MaxQueueSize < 0 || c.Batch.MaxExportBatchSize < 0 {
		return fmt.Errorf("%w: queue size %d, export batch size %d; zero takes the upstream default",
			ErrInvalidBatchParam, c.Batch.MaxQueueSize, c.Batch.MaxExportBatchSize)
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
