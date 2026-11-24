package otlp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/literal"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

func newExporters(ctx context.Context, reg *core.Registry) (any, error) {
	cfg, err := read(reg)
	if err != nil {
		return nil, err
	}
	material, err := cfg.checked()
	if err != nil {
		return nil, err
	}
	headers, err := cfg.headerMap()
	if err != nil {
		return nil, err
	}
	// The values are registered before the two warnings, which name a logger,
	// because Named binds the module attribute and a redaction rule governs only
	// what is bound after it.
	registerHeaderSecrets(reg, headers)
	warnPlaintextHeaders(reg, cfg, headers)
	warnIgnoredVariables(reg, os.LookupEnv)
	if cfg.ProbeAtStartup {
		if err = probe(ctx, cfg); err != nil {
			return nil, err
		}
	}

	span, metric, err := build(ctx, cfg, headers, material)
	if err != nil {
		return nil, err
	}
	return &observability.Exporters{Span: span, Metric: metric}, nil
}

// registerHeaderSecrets registers each header value as a literal
// redaction rule with the logging capability, when the assembly carries
// one and any header has a value.
func registerHeaderSecrets(reg *core.Registry, headers map[string]string) {
	if len(headers) == 0 {
		return
	}
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		return
	}
	values := make([]string, 0, len(headers))
	for _, value := range headers {
		values = append(values, value)
	}
	literal.Register(delivered.Redaction(), ModuleName+".headers", values...)
}

// warnPlaintextHeaders writes one line when the headers would cross a
// network in the clear: there are headers, the exporters are insecure,
// and the endpoint's host is not a loopback one.
func warnPlaintextHeaders(reg *core.Registry, cfg *Config, headers map[string]string) {
	if len(headers) == 0 || !cfg.Insecure || loopbackHost(cfg.Endpoint) {
		return
	}
	logger, err := core.Resolve[log.Logger](reg)
	if err != nil {
		return
	}
	logger.Named(ModuleName).Warn("headers are sent to the collector over plaintext; set " +
		ModuleName + ".insecure to false to send them over TLS")
}

func loopbackHost(endpoint string) bool {
	host := endpoint
	if h, _, err := net.SplitHostPort(endpoint); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

const (
	httpTracesPath  = "/v1/traces"
	httpMetricsPath = "/v1/metrics"
)

func build(ctx context.Context, cfg *Config, headers map[string]string, material tlsMaterial) (sdktrace.SpanExporter, sdkmetric.Exporter, error) {
	// A nil map would leave the header variables in force; an empty one
	// replaces them with nothing.
	if headers == nil {
		headers = map[string]string{}
	}
	switch cfg.Protocol {
	case ProtocolHTTP:
		// The URL scheme sets the upstream's TLS switch; the TLS configuration is
		// replaced outright, with none over plaintext.
		scheme, tlsConfig := "https", clientTLS(material)
		if cfg.Insecure {
			scheme, tlsConfig = "http", nil
		}
		span, err := otlptracehttp.New(ctx,
			otlptracehttp.WithEndpointURL(scheme+"://"+cfg.Endpoint+httpTracesPath),
			otlptracehttp.WithEndpoint(cfg.Endpoint),
			otlptracehttp.WithURLPath(httpTracesPath),
			otlptracehttp.WithTLSClientConfig(tlsConfig),
			otlptracehttp.WithHeaders(headers),
			otlptracehttp.WithEncoding(otlptracehttp.EncodingProtobuf),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: the http span exporter could not be built: %w", ModuleName, err)
		}
		metric, err := otlpmetrichttp.New(ctx,
			otlpmetrichttp.WithEndpointURL(scheme+"://"+cfg.Endpoint+httpMetricsPath),
			otlpmetrichttp.WithEndpoint(cfg.Endpoint),
			otlpmetrichttp.WithURLPath(httpMetricsPath),
			otlpmetrichttp.WithTLSClientConfig(tlsConfig),
			otlpmetrichttp.WithHeaders(headers),
			otlpmetrichttp.WithTemporalitySelector(sdkmetric.DefaultTemporalitySelector),
			otlpmetrichttp.WithAggregationSelector(sdkmetric.DefaultAggregationSelector),
		)
		if err != nil {
			_ = span.Shutdown(ctx)
			return nil, nil, fmt.Errorf("%s: the http metric exporter could not be built: %w", ModuleName, err)
		}
		return span, metric, nil
	default:
		// The transport credentials are given in both modes: credentials from a
		// certificate variable take precedence over the insecure switch.
		creds := insecure.NewCredentials()
		if !cfg.Insecure {
			creds = credentials.NewTLS(clientTLS(material))
		}
		span, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
			otlptracegrpc.WithTLSCredentials(creds),
			otlptracegrpc.WithHeaders(headers),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: the gRPC span exporter could not be built: %w", ModuleName, err)
		}
		metric, err := otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
			otlpmetricgrpc.WithTLSCredentials(creds),
			otlpmetricgrpc.WithHeaders(headers),
			otlpmetricgrpc.WithTemporalitySelector(sdkmetric.DefaultTemporalitySelector),
			otlpmetricgrpc.WithAggregationSelector(sdkmetric.DefaultAggregationSelector),
		)
		if err != nil {
			_ = span.Shutdown(ctx)
			return nil, nil, fmt.Errorf("%s: the gRPC metric exporter could not be built: %w", ModuleName, err)
		}
		return span, metric, nil
	}
}

// clientTLS is the TLS configuration a secure exporter uses: the configured roots
// and client certificate, or the host's roots and none.
func clientTLS(material tlsMaterial) *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: material.roots}
	if material.client != nil {
		cfg.Certificates = []tls.Certificate{*material.client}
	}
	return cfg
}

func probe(ctx context.Context, cfg *Config) error {
	address := dialAddress(cfg)
	dialer := net.Dialer{Timeout: cfg.ProbeTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrExporterUnreachable, address, err)
	}
	_ = conn.Close()
	return nil
}

// dialAddress is the endpoint with the port the upstream uses when none is
// written: 443 over gRPC, and the scheme's port over HTTP.
func dialAddress(cfg *Config) string {
	if _, _, err := net.SplitHostPort(cfg.Endpoint); err == nil {
		return cfg.Endpoint
	}
	port := "443"
	if cfg.Protocol == ProtocolHTTP && cfg.Insecure {
		port = "80"
	}
	host := strings.TrimSuffix(strings.TrimPrefix(cfg.Endpoint, "["), "]")
	return net.JoinHostPort(host, port)
}
