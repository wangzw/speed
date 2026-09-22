package otlp

import (
	"context"
	"fmt"
	"net"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/observability"
)

// newExporters builds the two exporters, probing the endpoint first when the
// configuration asks for it.
//
// The probe is here rather than in Prepare because New runs only for a command
// that actually builds this module, while Prepare runs for every module a
// command took in — including one that collects stances and constructs nothing.
// A round trip in Prepare would make such a command fail over an endpoint it
// was never going to send to.
func newExporters(ctx context.Context, reg *core.Registry) (any, error) {
	cfg, err := read(reg)
	if err != nil {
		return nil, err
	}
	if err = cfg.validate(); err != nil {
		return nil, err
	}
	headers, err := cfg.headerMap()
	if err != nil {
		return nil, err
	}
	if cfg.ProbeAtStartup {
		if err = probe(ctx, cfg.Endpoint, cfg.ProbeTimeout); err != nil {
			return nil, err
		}
	}

	span, metric, err := build(ctx, cfg, headers)
	if err != nil {
		return nil, err
	}
	return &observability.Exporters{Span: span, Metric: metric}, nil
}

// build constructs both exporters for the configured protocol.
//
// An exporter that was built before its pair failed is released here rather
// than left open: the failure returns from New, so no provider exists to shut
// it down later, and nothing else holds a reference to it.
func build(ctx context.Context, cfg *Config, headers map[string]string) (sdktrace.SpanExporter, sdkmetric.Exporter, error) {
	switch cfg.Protocol {
	case ProtocolHTTP:
		var spanHTTP []otlptracehttp.Option
		var metricHTTP []otlpmetrichttp.Option
		if cfg.Insecure {
			spanHTTP = append(spanHTTP, otlptracehttp.WithInsecure())
			metricHTTP = append(metricHTTP, otlpmetrichttp.WithInsecure())
		}
		if len(headers) > 0 {
			spanHTTP = append(spanHTTP, otlptracehttp.WithHeaders(headers))
			metricHTTP = append(metricHTTP, otlpmetrichttp.WithHeaders(headers))
		}
		spanHTTP = append(spanHTTP, otlptracehttp.WithEndpoint(cfg.Endpoint))
		metricHTTP = append(metricHTTP, otlpmetrichttp.WithEndpoint(cfg.Endpoint))
		span, err := otlptracehttp.New(ctx, spanHTTP...)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: the http span exporter could not be built: %w", ModuleName, err)
		}
		metric, err := otlpmetrichttp.New(ctx, metricHTTP...)
		if err != nil {
			_ = span.Shutdown(ctx)
			return nil, nil, fmt.Errorf("%s: the http metric exporter could not be built: %w", ModuleName, err)
		}
		return span, metric, nil
	default:
		var spanGrpc []otlptracegrpc.Option
		var metricGrpc []otlpmetricgrpc.Option
		if cfg.Insecure {
			spanGrpc = append(spanGrpc, otlptracegrpc.WithInsecure())
			metricGrpc = append(metricGrpc, otlpmetricgrpc.WithInsecure())
		}
		if len(headers) > 0 {
			spanGrpc = append(spanGrpc, otlptracegrpc.WithHeaders(headers))
			metricGrpc = append(metricGrpc, otlpmetricgrpc.WithHeaders(headers))
		}
		spanGrpc = append(spanGrpc, otlptracegrpc.WithEndpoint(cfg.Endpoint))
		metricGrpc = append(metricGrpc, otlpmetricgrpc.WithEndpoint(cfg.Endpoint))
		span, err := otlptracegrpc.New(ctx, spanGrpc...)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: the gRPC span exporter could not be built: %w", ModuleName, err)
		}
		metric, err := otlpmetricgrpc.New(ctx, metricGrpc...)
		if err != nil {
			_ = span.Shutdown(ctx)
			return nil, nil, fmt.Errorf("%s: the gRPC metric exporter could not be built: %w", ModuleName, err)
		}
		return span, metric, nil
	}
}

// probe opens a TCP connection to the endpoint and closes it again.
//
// A transport-level dial is what "reachable" means here, and it is deliberately
// the weakest thing that answers the question the operator is asking: whether
// the deployment premise holds. Whether the collector accepts what is sent is a
// different question, and one this module cannot answer at startup without
// inventing a request the transport does not otherwise make.
func probe(ctx context.Context, endpoint string, timeout time.Duration) error {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrExporterUnreachable, endpoint, err)
	}
	_ = conn.Close()
	return nil
}
