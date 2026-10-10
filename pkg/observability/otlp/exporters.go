package otlp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
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
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

// newExporters builds the two exporters, probing the endpoint first when the
// configuration asks for it.
//
// The probe is here rather than in Prepare because New runs only for a command
// that actually builds this module, while Prepare runs for every module a
// command took in (except one the host supplied an instance for) — including a
// command that collects stances and constructs nothing. A round trip in Prepare
// would make such a command fail over an endpoint it was never going to send to.
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
	warnPlaintextHeaders(reg, cfg, headers)
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

// warnPlaintextHeaders writes one line when the headers would cross a network
// in the clear: there are headers, the exporters are insecure, and the
// endpoint's host is not a loopback one.
//
// It is a warning and not a failure because insecure defaults to true for a
// collector beside the process, and a collector reached over a network the
// operator controls is a deployment this module cannot tell apart from one it
// does not. The headers are where a collector's credential goes, so the line
// names neither a header nor a value, and not the endpoint either: the line
// says what to change, and the configuration says where.
//
// The logger is an optional dependency. Without one the line is not written,
// rather than written through the process root logger.
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

// loopbackHost reports whether an endpoint's host is localhost or a loopback
// address. The port is optional, and an IPv6 address is in brackets either way.
// Only those names count: an empty host, as in ":4317", or an unspecified
// address such as 0.0.0.0 is not one of them, and is warned about.
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

// The paths a collector serves the two signals at over OTLP/HTTP.
const (
	httpTracesPath  = "/v1/traces"
	httpMetricsPath = "/v1/metrics"
)

// build constructs both exporters for the configured protocol.
//
// Every setting the configuration declares, and every part of the wire shape
// the module documents, is given to the upstream explicitly, including the
// ones whose explicit value is the upstream default. The upstream exporters
// read the standard OTEL_EXPORTER_OTLP_* variables for whatever they are not
// given, and they apply those variables first and the options after, so an
// option is the only way to keep a variable out: left implicit, the endpoint's
// path, the TLS switch and its certificates, the headers, the encoding, and the
// metric temporality and aggregation would each have a second source outside
// this module's prefix, its diagnostics and its sensitive marking — a header
// variable injecting a credential the configuration never gave, an insecure
// variable turning TLS off under a configuration that asked for it.
//
// What the configuration leaves to the library stays the library's, variables
// included: compression, the per-request timeout, retry and backoff.
//
// Both exporters of either protocol carry the one TLS configuration the
// certificate items loaded to.
//
// An exporter that was built before its pair failed is released here rather
// than left open: the failure returns from New, so no provider exists to shut
// it down later, and nothing else holds a reference to it.
func build(ctx context.Context, cfg *Config, headers map[string]string, material tlsMaterial) (sdktrace.SpanExporter, sdkmetric.Exporter, error) {
	// A nil map would leave the header variables in force; an empty one
	// replaces them with nothing.
	if headers == nil {
		headers = map[string]string{}
	}
	switch cfg.Protocol {
	case ProtocolHTTP:
		// The scheme in the URL is what sets the upstream's TLS switch either
		// way; the endpoint is then given verbatim, and the TLS configuration is
		// replaced outright — with none at all over plaintext, where a
		// certificate variable would otherwise fail the construction.
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
		// The transport credentials are given in both modes: credentials
		// built from a certificate variable take precedence over the
		// upstream's insecure switch, so only credentials of this module's
		// own can say plaintext and mean it.
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

// clientTLS is the TLS configuration an exporter that is not insecure uses: the
// roots and the client certificate the certificate items loaded to, and with
// none configured the host's root certificates and no client certificate,
// which is what the upstream uses when it is given nothing. It is given even
// then so that a certificate variable cannot replace it.
func clientTLS(material tlsMaterial) *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: material.roots}
	if material.client != nil {
		cfg.Certificates = []tls.Certificate{*material.client}
	}
	return cfg
}

// probe opens a TCP connection to the endpoint and closes it again.
//
// A transport-level dial is what "reachable" means here, and it is deliberately
// the weakest thing that answers the question the operator is asking: whether
// the deployment premise holds. Whether the collector accepts what is sent is a
// different question, and one this module cannot answer at startup without
// inventing a request the transport does not otherwise make.
//
// The address dialed is the one the exporters connect to, so an endpoint
// written without a port is probed at the port the upstream falls back to.
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

// dialAddress is the endpoint with the port the upstream connects to when none
// is written: 443 over gRPC, whose resolver falls back to it whatever the
// transport security, and over HTTP the scheme's own port, 80 over plaintext
// and 443 over TLS. An endpoint with a port is returned as written.
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
