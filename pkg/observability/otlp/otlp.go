// Package otlp exports telemetry to an OpenTelemetry collector over OTLP.
//
// It is the backend a deployment uses: one endpoint carries both signals, and
// the collector decides where they go from there. The exporter is the upstream
// one for the protocol named in configuration, so the transport's own details —
// retry, backoff, compression — are the library's rather than this module's.
//
// It delivers both exporter capabilities, non-exclusively, so that a host can
// export to a collector and somewhere else at the same time.
package otlp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/observability"
)

// ModuleName is this module's name in the registry, and the namespace its input
// items are mounted under.
const ModuleName = "observability.otlp"

// The protocol names this module accepts.
const (
	// ProtocolGRPC sends over gRPC, the protocol a collector listens for by
	// default.
	ProtocolGRPC = "grpc"
	// ProtocolHTTP sends over HTTP with protobuf payloads.
	ProtocolHTTP = "http"
)

// The failures this module judges for itself.
var (
	// ErrUnknownProtocol reports a protocol name outside the closed set. The
	// text lists the accepted names, which is the whole of the fix.
	ErrUnknownProtocol = errors.New("observability.otlp: protocol must be grpc or http")

	// ErrInvalidHeader reports a header element that is not key=value. Only the
	// key is named: a header carries a credential.
	ErrInvalidHeader = errors.New("observability.otlp: a header must be written key=value")

	// ErrExporterUnreachable reports that the startup probe could not open a
	// connection. It is only ever returned when the probe was asked for.
	ErrExporterUnreachable = errors.New("observability.otlp: the endpoint is not reachable")

	// ErrInvalidDuration reports a probe timeout that is not positive: an
	// enabled probe with no bound is the one thing a probe must not be.
	ErrInvalidDuration = errors.New("observability.otlp: probe-timeout must be positive")
)

// defaultProbeTimeout bounds the startup probe. It is shorter than a request
// timeout on purpose: a probe that hangs holds up a startup.
const defaultProbeTimeout = 3 * time.Second

// Config is what this module accepts from configuration.
type Config struct {
	// Endpoint is the collector's host and port, without a scheme — the form
	// the upstream option takes. It is required, which is what catches a
	// deployment that forgot it; a wrong endpoint is a different problem, and
	// reachability is what the probe below is for.
	Endpoint string `config:"endpoint"`
	// Protocol selects the transport.
	Protocol string `config:"protocol"`
	// Insecure sends over plaintext rather than TLS. It defaults to true
	// because the collector usually sits beside the process, on a network the
	// operator controls.
	Insecure bool `config:"insecure"`
	// Headers are extra headers sent with every request, each written
	// key=value. This is where a collector's credentials go, which is why the
	// item is marked sensitive: it keeps the values out of diagnostics and out
	// of the help output's default column, and the item takes no command-line
	// origin, so a value cannot reach the process list.
	Headers []string `config:"headers"`
	// ProbeAtStartup makes reachability a deployment premise rather than a
	// runtime fact. It is off by default: a collector that starts after the
	// process does is an ordinary deployment, and failing the startup over it
	// would be this module inventing a requirement the transport does not have.
	ProbeAtStartup bool `config:"probe-at-startup"`
	// ProbeTimeout bounds that probe.
	ProbeTimeout time.Duration `config:"probe-timeout"`
}

// prototype carries the defaults.
func prototype() *Config {
	return &Config{
		Protocol:     ProtocolGRPC,
		Insecure:     true,
		ProbeTimeout: defaultProbeTimeout,
	}
}

// init registers this module with the process registry, so a host that imports
// the package has it available.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Provides: []core.Provision{
			// Not exclusive: a destination is not a choice among mutually
			// exclusive implementations, and a host may export to several.
			{Token: (*observability.SpanExporter)(nil)},
			{Token: (*observability.MetricExporter)(nil)},
		},
		Resources: []any{config.Schema{
			Namespace: ModuleName,
			Mounts:    []config.Mount{{Value: prototype()}},
			Items: map[string]config.Item{
				"endpoint": {
					Required:    true,
					Description: "the collector's host and port, without a scheme",
					Placeholder: "collector.example:4317",
				},
				"headers": {
					Sensitive:   true,
					Description: "extra request headers, each written key=value",
				},
			},
		}},
		Prepare: prepare,
		New:     newExporters,
		// No Close: the providers the assembler built drive these exporters and
		// shut them down with themselves.
	}
}

// prepare reads this module's own configuration and states that it starts.
//
// The probe does not happen here. Prepare runs for every module this command
// took in, including a command that only collects stances and builds nothing,
// and a network round trip in this stage would make such a command fail over an
// endpoint it was never going to use.
func prepare(_ context.Context, reg *core.Registry) (core.Enablement, error) {
	cfg, err := read(reg)
	if err != nil {
		return core.Enablement{}, err
	}
	if err := cfg.validate(); err != nil {
		return core.Enablement{}, err
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// read decodes this module's namespace into a fresh carrier.
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
func (c *Config) validate() error {
	switch c.Protocol {
	case ProtocolGRPC, ProtocolHTTP:
	default:
		return fmt.Errorf("%w: got %q", ErrUnknownProtocol, c.Protocol)
	}
	if c.ProbeTimeout <= 0 {
		return fmt.Errorf("%w: got %v", ErrInvalidDuration, c.ProbeTimeout)
	}
	_, err := c.headerMap()
	return err
}

// headerMap parses the header list.
//
// The element shape is key=value rather than a mapping because the item has to
// be able to take its value from the environment: a mapping is a container, and
// a container cannot be declared with the environment and command-line origins
// at all. A list of strings can, which is what lets a credential come from the
// environment rather than from a file.
//
// An element that does not parse is reported by its position, and no part of it
// is repeated in the text. Naming the key would be the useful thing to do and
// is not safe: an element with no separator has no key to name — the whole
// string would be echoed, credential included — and a key is not reliably the
// harmless half either, which is the same reason the redaction design does not
// treat a key as content-free. The position is enough to find the element in a
// configuration file or an environment variable.
func (c *Config) headerMap() (map[string]string, error) {
	if len(c.Headers) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(c.Headers))
	for i, item := range c.Headers {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("%w: element %d of headers does not parse, and the element is left "+
				"out of this text because a header carries a credential", ErrInvalidHeader, i+1)
		}
		headers[key] = value
	}
	return headers, nil
}
