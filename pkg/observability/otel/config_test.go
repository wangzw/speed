package otel

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
)

// readThrough loads a JSON document through the real configuration path and
// hands back the reader this module's own section decodes from.
//
// The declaration this module registers is what is exercised: whether an item
// is required is the loader's judgement, made from the schema, and a stub reader
// would prove nothing about it.
func readThrough(t *testing.T, document string) config.Reader {
	t.Helper()
	reader, err := loadThrough(t, document)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	return reader
}

// loadThrough is readThrough with the load's failure handed back rather than
// failing the case.
func loadThrough(t *testing.T, document string) (config.Reader, error) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("writing the config document: %v", err)
	}

	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "OTELTEST", DefaultLocator: "file:" + path}},
	})
	reg.Register(core.Module{Name: "reader", Resources: []any{schema()}})

	return config.Load(t.Context(), reg, nil)
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
	if !slices.Equal(cfg.Endpoints, []string{"*"}) {
		t.Errorf("the endpoint list defaults to %q, want the single name \"*\": an unconfigured module "+
			"instruments every endpoint the entry module declares", cfg.Endpoints)
	}
}

// TestAnEventListIsAnUnknownKey pins that this module declares no event list:
// it subscribes to no event type, so a primary source that names one under this
// namespace is refused at load as a key no module declares, rather than read
// and left to do nothing.
func TestAnEventListIsAnUnknownKey(t *testing.T) {
	_, err := loadThrough(t, `{"observability": {"otel": {"events": ["order.placed"]}}}`)
	if !errors.Is(err, config.ErrUnknownKey) {
		t.Fatalf("loading a configuration with observability.otel.events returned %v, want the unknown-key "+
			"failure", err)
	}
	if !strings.Contains(err.Error(), "observability.otel.events") {
		t.Errorf("the failure %q does not name the key", err)
	}
}

// TestTheServiceNameIsNotRequired pins that a configuration which never names
// the service decodes and validates. Decode refuses a required item no source
// gave before this module's own checks run, so the declaration is what has to
// let it through; and the name that comes out is empty, which is how the module
// knows to leave the choice to the upstream.
func TestTheServiceNameIsNotRequired(t *testing.T) {
	reader := readThrough(t, `{"observability": {"otel": {"sample-ratio": 0.5}}}`)

	cfg := prototype()
	if err := reader.Decode(ModuleName, cfg); err != nil {
		t.Fatalf("decoding a configuration with no service-name: %v", err)
	}
	if cfg.ServiceName != "" {
		t.Errorf("the service name decoded as %q, want none: nothing gave one", cfg.ServiceName)
	}
	if err := cfg.validate(); err != nil {
		t.Errorf("validate refused a configuration with no service name: %v", err)
	}
}

// TestAServiceNameWrittenAsEmptyIsAbsent pins the other way of not naming the
// service. A source that gives the empty string decodes to the same value as no
// source at all, and neither is a failure.
func TestAServiceNameWrittenAsEmptyIsAbsent(t *testing.T) {
	reader := readThrough(t, `{"observability": {"otel": {"service-name": ""}}}`)

	cfg := prototype()
	if err := reader.Decode(ModuleName, cfg); err != nil {
		t.Fatalf("decoding an empty service-name: %v", err)
	}
	if err := cfg.validate(); err != nil {
		t.Errorf("validate refused an empty service name: %v", err)
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
		{"a ratio below zero", func(c *Config) { c.SampleRatio = -0.01 }, ErrInvalidSampleRatio},
		{"a ratio above one", func(c *Config) { c.SampleRatio = 1.01 }, ErrInvalidSampleRatio},
		// NaN is neither below zero nor above one, so the interval comparisons
		// miss it; it must be refused as a ratio outside [0,1] all the same.
		{"a NaN ratio", func(c *Config) { c.SampleRatio = math.NaN() }, ErrInvalidSampleRatio},
		{"a negative queue size", func(c *Config) { c.Batch.MaxQueueSize = -1 }, ErrInvalidBatchParam},
		{"a negative export batch size", func(c *Config) { c.Batch.MaxExportBatchSize = -1 }, ErrInvalidBatchParam},
		{"a zero batch timeout", func(c *Config) { c.Batch.BatchTimeout = 0 }, ErrInvalidDuration},
		{"a zero export timeout", func(c *Config) { c.Batch.ExportTimeout = 0 }, ErrInvalidDuration},
		{"a zero metric interval", func(c *Config) { c.Metric.Interval = 0 }, ErrInvalidDuration},
		{"a zero metric timeout", func(c *Config) { c.Metric.Timeout = 0 }, ErrInvalidDuration},
		{"a zero shutdown timeout", func(c *Config) { c.ShutdownTimeout = 0 }, ErrInvalidDuration},
		{"a star beside a name", func(c *Config) { c.Endpoints = []string{"*", "api"} }, ErrWildcardBesideEndpointNames},
		{"a name beside a star", func(c *Config) { c.Endpoints = []string{"api", "*"} }, ErrWildcardBesideEndpointNames},
		{"a star written twice", func(c *Config) { c.Endpoints = []string{"*", "*"} }, ErrWildcardBesideEndpointNames},
		{"a star between two names", func(c *Config) { c.Endpoints = []string{"api", "*", "admin"} }, ErrWildcardBesideEndpointNames},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := prototype()
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
	cfg := prototype()
	cfg.SampleRatio = 0
	cfg.Batch.MaxQueueSize = 0
	cfg.Batch.MaxExportBatchSize = 0
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate refused a legal configuration: %v", err)
	}
}

// TestValidateAcceptsEachLegalEndpointList pins the three shapes the list may
// take: the star alone, names, and nothing.
func TestValidateAcceptsEachLegalEndpointList(t *testing.T) {
	for _, list := range [][]string{{"*"}, {"api", "admin"}, {}, nil} {
		cfg := prototype()
		cfg.Endpoints = list
		if err := cfg.validate(); err != nil {
			t.Errorf("validate refused the endpoint list %q: %v", list, err)
		}
	}
}

// TestAStarBesideANameNamesTheKeyAndTheCorrectWriting pins what the refusal
// tells its reader: which key, and what to write instead.
func TestAStarBesideANameNamesTheKeyAndTheCorrectWriting(t *testing.T) {
	cfg := prototype()
	cfg.Endpoints = []string{"*", "api"}
	err := cfg.validate()
	if err == nil {
		t.Fatal("validate accepted a star beside a name")
	}
	for _, want := range []string{"observability.otel.endpoints", `["*"] alone`, "without"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q lacks %q", err, want)
		}
	}
}

// TestAnEmptyEndpointListIsNotTheDefault pins that the empty list survives
// decoding, whichever source wrote it, instead of being read as an absent item
// that keeps the prototype's star: the document's empty array and an
// environment variable whose value is empty both mean "none". A null is the
// other way round: it is read as an absent item.
func TestAnEmptyEndpointListIsNotTheDefault(t *testing.T) {
	t.Run("a document", func(t *testing.T) {
		reader := readThrough(t, `{"observability": {"otel": {"endpoints": []}}}`)
		cfg := prototype()
		if err := reader.Decode(ModuleName, cfg); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if len(cfg.Endpoints) != 0 {
			t.Errorf("an empty array decoded to %q, want an empty list", cfg.Endpoints)
		}
	})
	t.Run("an environment variable", func(t *testing.T) {
		t.Setenv("OTELTEST_OBSERVABILITY__OTEL__ENDPOINTS", "")
		reader := readThrough(t, `{"observability": {"otel": {"sample-ratio": 0.5}}}`)
		cfg := prototype()
		if err := reader.Decode(ModuleName, cfg); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if len(cfg.Endpoints) != 0 {
			t.Errorf("an empty variable decoded to %q, want an empty list", cfg.Endpoints)
		}
	})
	t.Run("a list of names", func(t *testing.T) {
		reader := readThrough(t, `{"observability": {"otel": {"endpoints": ["public", "ops"]}}}`)
		cfg := prototype()
		if err := reader.Decode(ModuleName, cfg); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if want := []string{"public", "ops"}; !slices.Equal(cfg.Endpoints, want) {
			t.Errorf("a list of names decoded to %q, want exactly %q: the source's list replaces the default star instead of joining it", cfg.Endpoints, want)
		}
		if err := cfg.validate(); err != nil {
			t.Errorf("the decoded list of names does not validate: %v", err)
		}
	})
	t.Run("a null", func(t *testing.T) {
		reader := readThrough(t, `{"observability": {"otel": {"endpoints": null}}}`)
		cfg := prototype()
		if err := reader.Decode(ModuleName, cfg); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if !slices.Equal(cfg.Endpoints, []string{"*"}) {
			t.Errorf("a null item decoded to %q, want the default star: null is an absent item, not an empty list", cfg.Endpoints)
		}
	})
	t.Run("nothing", func(t *testing.T) {
		reader := readThrough(t, `{"observability": {"otel": {"sample-ratio": 0.5}}}`)
		cfg := prototype()
		if err := reader.Decode(ModuleName, cfg); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if !slices.Equal(cfg.Endpoints, []string{"*"}) {
			t.Errorf("an absent item decoded to %q, want the default star", cfg.Endpoints)
		}
	})
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
