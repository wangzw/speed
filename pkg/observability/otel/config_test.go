package otel

import (
	"errors"
	"testing"
)

// named is the prototype with the one required item filled in, so that a case
// about some other field is not answered by the service name first.
func named() *Config {
	cfg := prototype()
	cfg.ServiceName = "orders"
	return cfg
}

// TestThePrototypeCarriesTheDefaults pins what an absent item decodes to. The
// defaults ride on the prototype because Decode leaves a field no source gave a
// value for as it found it, so a field left at its zero value here is a field
// whose default is the upstream's rather than one this module restates.
func TestThePrototypeCarriesTheDefaults(t *testing.T) {
	cfg := prototype()

	if cfg.SampleRatio != 1.0 {
		t.Errorf("sample-ratio defaults to %v, want 1.0: an unconfigured module records everything", cfg.SampleRatio)
	}
	if cfg.Batch.MaxQueueSize != 0 || cfg.Batch.MaxExportBatchSize != 0 {
		t.Errorf("the batch sizes default to %d and %d, want zero: zero is what the upstream default is "+
			"substituted for, and writing the upstream's number here would be a second copy of it",
			cfg.Batch.MaxQueueSize, cfg.Batch.MaxExportBatchSize)
	}
	if cfg.ShutdownTimeout <= 0 {
		t.Error("shutdown-timeout has no default, so a Close would have no bound")
	}
	if len(cfg.Endpoints) != 0 || len(cfg.Events) != 0 {
		t.Error("the endpoint and event lists default to non-empty, so an unconfigured module would claim " +
			"an entry module and a bus it was never told about")
	}
}

// TestValidateNamesEachFailure walks the sentinels one at a time. Each case
// perturbs exactly one field of an otherwise valid carrier, so a case that
// starts failing for another reason shows up as the wrong sentinel rather than
// passing.
func TestValidateNamesEachFailure(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		want   error
	}{
		{"an empty service name", func(c *Config) { c.ServiceName = "" }, ErrEmptyServiceName},
		{"a ratio below zero", func(c *Config) { c.SampleRatio = -0.01 }, ErrInvalidSampleRatio},
		{"a ratio above one", func(c *Config) { c.SampleRatio = 1.01 }, ErrInvalidSampleRatio},
		{"a negative queue size", func(c *Config) { c.Batch.MaxQueueSize = -1 }, ErrInvalidBatchParam},
		{"a negative export batch size", func(c *Config) { c.Batch.MaxExportBatchSize = -1 }, ErrInvalidBatchParam},
		{"a zero batch timeout", func(c *Config) { c.Batch.BatchTimeout = 0 }, ErrInvalidDuration},
		{"a zero export timeout", func(c *Config) { c.Batch.ExportTimeout = 0 }, ErrInvalidDuration},
		{"a zero metric interval", func(c *Config) { c.Metric.Interval = 0 }, ErrInvalidDuration},
		{"a zero metric timeout", func(c *Config) { c.Metric.Timeout = 0 }, ErrInvalidDuration},
		{"a zero shutdown timeout", func(c *Config) { c.ShutdownTimeout = 0 }, ErrInvalidDuration},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := named()
			tc.mutate(cfg)
			err := cfg.validate()
			if !errors.Is(err, tc.want) {
				t.Fatalf("validate returned %v, want %v", err, tc.want)
			}
		})
	}
}

// TestValidateAcceptsWhatTheDesignCallsLegal pins the values that look like
// failures and are not: a zero ratio records no traces, which the module
// announces rather than refusing, and a zero batch size means the upstream
// default rather than a queue that holds nothing.
func TestValidateAcceptsWhatTheDesignCallsLegal(t *testing.T) {
	cfg := named()
	cfg.SampleRatio = 0
	cfg.Batch.MaxQueueSize = 0
	cfg.Batch.MaxExportBatchSize = 0
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate refused a legal configuration: %v", err)
	}
}

// TestZeroBatchSizesTakeTheUpstreamDefault pins the substitution that keeps the
// documented meaning and the implemented one the same. The upstream reads a
// zero as a queue of that size, so passing it through would produce a processor
// that cannot hold a span — while the configuration's own documentation says
// zero means the upstream's default.
func TestZeroBatchSizesTakeTheUpstreamDefault(t *testing.T) {
	if got := queueSize(0, 512); got != 512 {
		t.Errorf("a configured zero became %d, want the default it stands for", got)
	}
	if got := queueSize(256, 512); got != 256 {
		t.Errorf("a configured 256 became %d, want 256", got)
	}
}
