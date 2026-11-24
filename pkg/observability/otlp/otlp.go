// Package otlp exports telemetry to an OpenTelemetry collector over OTLP.
package otlp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
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
	// ProtocolHTTPProtobuf is the name the OTLP specification gives the same
	// transport as ProtocolHTTP. It is accepted as written and read as
	// ProtocolHTTP.
	ProtocolHTTPProtobuf = "http/protobuf"
)

const (
	endpointKey       = ModuleName + ".endpoint"
	protocolKey       = ModuleName + ".protocol"
	insecureKey       = ModuleName + ".insecure"
	headersKey        = ModuleName + ".headers"
	caFileKey         = ModuleName + ".ca-file"
	clientCertFileKey = ModuleName + ".client-cert-file"
	clientKeyFileKey  = ModuleName + ".client-key-file"
)

// The failures this module judges for itself.
var (
	// ErrUnknownProtocol reports a protocol name outside the closed set. The
	// text lists the accepted names, which is the whole of the fix.
	ErrUnknownProtocol = errors.New("observability.otlp: protocol must be grpc, http or http/protobuf")

	// ErrInvalidEndpoint reports an endpoint the shape check refuses: one that
	// is empty, or carries a scheme, a path or whitespace. Other malformed
	// values, a port that is not a number among them, pass the check.
	ErrInvalidEndpoint = errors.New("observability.otlp: the endpoint is a host and an optional port, " +
		"with no scheme and no path")

	// ErrInvalidHeader reports a header element that is not key=value, or whose
	// key is empty or contains whitespace. Nothing of the element is repeated:
	// a header carries a credential.
	ErrInvalidHeader = errors.New("observability.otlp: a header must be written key=value, " +
		"with a key that is not empty and has no whitespace in it")

	// ErrUnreadableCertificate reports a certificate item whose file cannot
	// be read or does not parse: a ca-file with no PEM certificate in it, or
	// a client certificate and key that do not load as a pair.
	ErrUnreadableCertificate = errors.New("observability.otlp: a certificate file could not be loaded")

	// ErrUnpairedClientCertificate reports a client certificate given without
	// its key, or a key without its certificate.
	ErrUnpairedClientCertificate = errors.New("observability.otlp: client-cert-file and client-key-file " +
		"are given together or not at all")

	// ErrCertificateOverPlaintext reports a certificate item set while
	// insecure is true: the files configure TLS, and insecure sends without
	// it.
	ErrCertificateOverPlaintext = errors.New("observability.otlp: ca-file, client-cert-file and " +
		"client-key-file configure TLS, and insecure sends over plaintext")

	// ErrExporterUnreachable reports that the startup probe could not open a
	// connection. It is only ever returned when the probe was asked for.
	ErrExporterUnreachable = errors.New("observability.otlp: the endpoint is not reachable")

	// ErrInvalidDuration reports a probe timeout that is not positive: an
	// enabled probe with no bound is the one thing a probe must not be.
	ErrInvalidDuration = errors.New("observability.otlp: probe-timeout must be positive")
)

const defaultProbeTimeout = 3 * time.Second

// Config is what this module accepts from configuration.
type Config struct {
	// Endpoint is the collector's host and optional port, without a scheme or
	// a path — the form the upstream option takes. An IPv6 address is
	// written in brackets.
	Endpoint string `config:"endpoint"`
	// Protocol selects the transport. http/protobuf is read as http.
	Protocol string `config:"protocol"`
	// Insecure sends over plaintext rather than TLS. It defaults to true
	// because the collector usually sits beside the process, on a network the
	// operator controls.
	Insecure bool `config:"insecure"`
	// Headers are extra headers sent with every request, each written
	// key=value; whitespace around an element, its key and its value is
	// dropped.
	Headers []string `config:"headers"`
	// CAFile is a PEM file of the certificates a collector's certificate is
	// verified against, in place of the host's root certificates rather than
	// beside them. Empty is the host's root certificates.
	CAFile string `config:"ca-file"`
	// ClientCertFile is the PEM certificate the exporters present to a collector
	// that asks for one. It is given together with ClientKeyFile or not at all.
	ClientCertFile string `config:"client-cert-file"`
	// ClientKeyFile is the PEM key paired with ClientCertFile.
	ClientKeyFile string `config:"client-key-file"`
	// ProbeAtStartup makes reachability a deployment premise rather than a
	// runtime fact.
	ProbeAtStartup bool `config:"probe-at-startup"`
	// ProbeTimeout bounds that probe.
	ProbeTimeout time.Duration `config:"probe-timeout"`
}

func prototype() *Config {
	return &Config{
		Protocol:     ProtocolGRPC,
		Insecure:     true,
		ProbeTimeout: defaultProbeTimeout,
	}
}

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			// Optional: the logger carries two warnings, about headers sent over
			// plaintext and about standard variables that are set and have no effect,
			// and nothing here needs it to export.
			{Token: (*log.Logger)(nil), Optional: true},
		},
		// Not exclusive: a destination is not a choice among mutually exclusive
		// implementations, and a host may export to several.
		Provides: []core.Provision{
			{Token: (*observability.SpanExporter)(nil)},
			{Token: (*observability.MetricExporter)(nil)},
		},
		Resources: []any{config.Schema{
			Namespace: ModuleName,
			Mounts:    []config.Mount{{Value: prototype()}},
			Items: map[string]config.Item{
				"endpoint": {
					Required: true,
					Description: "the collector's host and optional port, without a scheme or a path; a collector " +
						"listens on 4317 for grpc and on 4318 for http, so the port goes with protocol",
					Placeholder: "HOST:4317|HOST:4318",
				},
				"headers": {
					Sensitive: true,
					Description: "extra request headers, each written key=value and separated by commas; " +
						"a header whose value contains a comma is written in the main configuration source",
				},
				"ca-file": {
					Description: "a PEM file of the certificates the collector's certificate is verified " +
						"against, in place of the host's root certificates; requires insecure false",
					Placeholder: "PATH",
				},
				"client-cert-file": {
					Description: "a PEM client certificate presented to the collector; given together " +
						"with client-key-file, and requires insecure false",
					Placeholder: "PATH",
				},
				"client-key-file": {
					Description: "the PEM key of client-cert-file; given together with it, and requires " +
						"insecure false",
					Placeholder: "PATH",
				},
			},
		}},
		Prepare: prepare,
		New:     newExporters,
		// No Close: the providers the assembler built drive these exporters,
		// and the assembler releases them once both providers have flushed.
	}
}

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
	// The specification's name for the HTTP transport is read as this
	// module's, so that everything past here knows two protocols.
	if cfg.Protocol == ProtocolHTTPProtobuf {
		cfg.Protocol = ProtocolHTTP
	}
	return cfg, nil
}

func (c *Config) validate() error {
	_, err := c.checked()
	return err
}

// checked is validate that also hands back what the certificate items loaded
// to, so that New reads the files once rather than once to judge them and once
// to use them.
func (c *Config) checked() (tlsMaterial, error) {
	if err := validateEndpoint(c.Endpoint); err != nil {
		return tlsMaterial{}, err
	}
	switch c.Protocol {
	case ProtocolGRPC, ProtocolHTTP:
	default:
		return tlsMaterial{}, fmt.Errorf("%w: got %q", ErrUnknownProtocol, c.Protocol)
	}
	if c.ProbeTimeout <= 0 {
		return tlsMaterial{}, fmt.Errorf("%w: got %v", ErrInvalidDuration, c.ProbeTimeout)
	}
	if _, err := c.headerMap(); err != nil {
		return tlsMaterial{}, err
	}
	return c.loadTLS()
}

type tlsMaterial struct {
	roots  *x509.CertPool
	client *tls.Certificate
}

func (c *Config) loadTLS() (tlsMaterial, error) {
	if c.Insecure {
		var set []string
		for _, item := range []struct{ key, path string }{
			{caFileKey, c.CAFile},
			{clientCertFileKey, c.ClientCertFile},
			{clientKeyFileKey, c.ClientKeyFile},
		} {
			if item.path != "" {
				set = append(set, item.key)
			}
		}
		if len(set) > 0 {
			return tlsMaterial{}, fmt.Errorf("%w: %s set while %s is true; set %s to false to send "+
				"over TLS, or leave the files out", ErrCertificateOverPlaintext, strings.Join(set, " and "),
				insecureKey, insecureKey)
		}
		return tlsMaterial{}, nil
	}
	switch {
	case c.ClientCertFile != "" && c.ClientKeyFile == "":
		return tlsMaterial{}, fmt.Errorf("%w: %s is set and %s is not", ErrUnpairedClientCertificate,
			clientCertFileKey, clientKeyFileKey)
	case c.ClientCertFile == "" && c.ClientKeyFile != "":
		return tlsMaterial{}, fmt.Errorf("%w: %s is set and %s is not", ErrUnpairedClientCertificate,
			clientKeyFileKey, clientCertFileKey)
	}

	var m tlsMaterial
	if c.CAFile != "" {
		data, err := os.ReadFile(c.CAFile)
		if err != nil {
			return tlsMaterial{}, fmt.Errorf("%w: %s cannot be read: %w", ErrUnreadableCertificate, caFileKey, err)
		}
		m.roots = x509.NewCertPool()
		if !m.roots.AppendCertsFromPEM(data) {
			return tlsMaterial{}, fmt.Errorf("%w: %s holds no PEM certificate that parses",
				ErrUnreadableCertificate, caFileKey)
		}
	}
	if c.ClientCertFile != "" {
		cert, err := os.ReadFile(c.ClientCertFile)
		if err != nil {
			return tlsMaterial{}, fmt.Errorf("%w: %s cannot be read: %w", ErrUnreadableCertificate,
				clientCertFileKey, err)
		}
		key, err := os.ReadFile(c.ClientKeyFile)
		if err != nil {
			return tlsMaterial{}, fmt.Errorf("%w: %s cannot be read: %w", ErrUnreadableCertificate,
				clientKeyFileKey, err)
		}
		// The upstream's text is left out: it can quote a PEM block's type label,
		// which is the file's content.
		pair, err := tls.X509KeyPair(cert, key)
		if err != nil {
			return tlsMaterial{}, fmt.Errorf("%w: %s and %s do not load as a PEM certificate and its key",
				ErrUnreadableCertificate, clientCertFileKey, clientKeyFileKey)
		}
		m.client = &pair
	}
	return m, nil
}

// validateEndpoint refuses the shapes of an endpoint that are not a host
// and an optional port: the empty string, a scheme, a path and
// whitespace.
func validateEndpoint(endpoint string) error {
	var defect string
	switch {
	case endpoint == "":
		defect = "is empty"
	case strings.Contains(endpoint, "://"):
		defect = "carries a scheme"
	case strings.Contains(endpoint, "/"):
		defect = "carries a path"
	case strings.IndexFunc(endpoint, unicode.IsSpace) >= 0:
		defect = "contains whitespace"
	default:
		return nil
	}
	return fmt.Errorf("%w: %s %s; write it host:port, such as collector.example:4317, with the port "+
		"that goes with protocol and an IPv6 address in brackets", ErrInvalidEndpoint, endpointKey, defect)
}

func (c *Config) headerMap() (map[string]string, error) {
	if len(c.Headers) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(c.Headers))
	for i, item := range c.Headers {
		key, value, ok := strings.Cut(item, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.IndexFunc(key, unicode.IsSpace) >= 0 {
			return nil, fmt.Errorf("%w: element %d of headers does not parse. The elements are separated "+
				"by commas, so a header whose value contains a comma has to be written in the main "+
				"configuration source; the element is left out of this text because a header carries "+
				"a credential", ErrInvalidHeader, i+1)
		}
		headers[key] = strings.TrimSpace(value)
	}
	return headers, nil
}
