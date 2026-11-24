// Package tracing is the one place in this module that touches the
// telemetry module's tracer provider and its propagator.
package tracing

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/observability"
)

const scope = "github.com/vislake/speed/pkg/event"

// Provider resolves the assembly's tracer provider and adapts it to the
// narrow face pkg/event defines.
func Provider(reg *core.Registry) (event.TracerProvider, error) {
	upstream, err := core.Resolve[observability.TracerProvider](reg)
	switch {
	case errors.Is(err, core.ErrMissingProvider):
		return Noop(), nil
	case err != nil:
		return nil, fmt.Errorf("event: taking up the tracer provider that records a delivery's span: %w", err)
	}
	return provider{upstream: upstream}, nil
}

// Noop is the tracer provider an assembly with no telemetry resolves to: every
// span starts and ends on it and no signal leaves the process. Provider hands it
// back when the registry carries no telemetry provider.
func Noop() event.TracerProvider { return provider{upstream: noop.NewTracerProvider()} }

// Propagator is the trace-context propagator both sides of a process boundary
// use: the publisher injects with it and the dispatch layer extracts with it, so
// the two ends of one link cannot settle on different formats.
func Propagator() propagation.TextMapPropagator { return observability.Propagator() }

// Requirement is the optional tracer dependency an implementation appends
// to the descriptor NewModule hands back.
func Requirement() core.Requirement {
	return core.Requirement{Token: (*observability.TracerProvider)(nil), Optional: true}
}

type provider struct{ upstream observability.TracerProvider }

// Start begins a delivery's span. The span kind is left at the SDK's default:
// the narrow face carries none, and a delivery is neither a client nor a server
// span of a remote call.
func (p provider) Start(ctx context.Context, name string, attrs map[string]string) (context.Context, event.Span) {
	var opts []trace.SpanStartOption
	if len(attrs) > 0 {
		kv := make([]attribute.KeyValue, 0, len(attrs))
		for k, v := range attrs {
			kv = append(kv, attribute.String(k, v))
		}
		opts = append(opts, trace.WithAttributes(kv...))
	}
	ctx, span := p.upstream.Tracer(scope).Start(ctx, name, opts...)
	return ctx, spanAdapter{span: span}
}

type spanAdapter struct{ span trace.Span }

func (s spanAdapter) End(err error) {
	if err != nil {
		kind := fmt.Sprintf("%T", err)
		s.span.SetAttributes(attribute.String("error.type", kind))
		s.span.SetStatus(codes.Error, kind)
	}
	s.span.End()
}
