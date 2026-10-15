// Package tracing is the one place in this module that touches the telemetry
// module's tracer provider and its propagator. Every implementation takes its
// narrow provider from Provider and its propagator from Propagator, so the
// adaptation happens once rather than drifting into a copy per backend.
//
// The root package defines the narrow face pkg/event consumes and never imports
// the telemetry interface package; this unit, the implementation subpackages and
// the dispatch layer are where the telemetry types appear. Nothing here reaches
// the root package's dependency face, which is the point of the split.
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

// scope names the instrumentation these spans belong to. It is this module, not
// the backend that carried the event: the span describes a subscriber running,
// and the backend is recorded as the messaging.system attribute instead.
const scope = "github.com/vislake/speed/pkg/event"

// Provider resolves the assembly's tracer provider and adapts it to the narrow
// face pkg/event defines.
//
// An assembly with no telemetry delivers no tracer provider. That is a legal
// assembly, told apart from a failed lookup by the sentinel, and answers with
// one that exports nothing: the bus starts and stops as it would, every span is
// recorded on a provider that drops it, and no signal is produced.
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

// Requirement is the optional tracer dependency an implementation appends to
// the descriptor NewModule hands back.
//
// The root package cannot name (*observability.TracerProvider)(nil) without
// importing that interface package, which its own dependency gate forbids, so
// the implementation declares the edge instead. Without it the two modules are
// ordered by name alone, Provider finds no provider and falls back to one that
// exports nothing, and the bus runs exporting no span and saying nothing.
func Requirement() core.Requirement {
	return core.Requirement{Token: (*observability.TracerProvider)(nil), Optional: true}
}

// provider adapts a telemetry tracer provider to the narrow face pkg/event
// defines.
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

// spanAdapter turns the telemetry span into the narrow Span, marking a delivery
// that failed.
type spanAdapter struct{ span trace.Span }

// End closes the span. A non-nil err marks it failed and names an error.type:
// the error's own type, not its text, since a subscriber's message can quote a
// payload value and a trace carries its status far from the process that wrote
// it.
func (s spanAdapter) End(err error) {
	if err != nil {
		kind := fmt.Sprintf("%T", err)
		s.span.SetAttributes(attribute.String("error.type", kind))
		s.span.SetStatus(codes.Error, kind)
	}
	s.span.End()
}
