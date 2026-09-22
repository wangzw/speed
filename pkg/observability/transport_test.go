package observability_test

import (
	"context"
	nethttp "net/http"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/vislake/speed/pkg/observability"
)

// roundTripperFunc stands in for a client's transport and records what it was
// handed.
type roundTripperFunc func(*nethttp.Request) (*nethttp.Response, error)

func (f roundTripperFunc) RoundTrip(r *nethttp.Request) (*nethttp.Response, error) { return f(r) }

// TestTransportInjectsTheSpanContextAndLeavesTheCallersRequestAlone pins both
// halves of what the wrapper owes a caller: the receiving process can continue
// the trace, and the request the caller still holds is unchanged. The second
// half is what makes it usable on a client that retries — a transport that
// wrote into the caller's headers would have every retry carry the first
// attempt's span.
func TestTransportInjectsTheSpanContextAndLeavesTheCallersRequestAlone(t *testing.T) {
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	ctx, span := tp.Tracer("test").Start(context.Background(), "outbound")
	defer span.End()

	var seen nethttp.Header
	inner := roundTripperFunc(func(r *nethttp.Request) (*nethttp.Response, error) {
		seen = r.Header
		return &nethttp.Response{StatusCode: nethttp.StatusOK, Body: nethttp.NoBody, Header: nethttp.Header{}}, nil
	})
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, "http://example.test/", nil)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}

	if _, err := observability.Transport(inner).RoundTrip(req); err != nil {
		t.Fatalf("round trip: %v", err)
	}

	injected := seen.Get("traceparent")
	if injected == "" {
		t.Fatal("no traceparent header reached the transport, so the receiving process cannot continue the trace")
	}
	if got := span.SpanContext().TraceID().String(); !strings.Contains(injected, got) {
		t.Errorf("the injected header %q does not carry the current trace id %s", injected, got)
	}
	if got := req.Header.Get("traceparent"); got != "" {
		t.Errorf("the caller's request was mutated: its traceparent is %q", got)
	}
}

// TestTransportWithoutASpanPassesTheRequestThrough pins the other half of the
// contract: background work and startup reach the wrapper with no span in the
// context, and the answer for them is to send the request as it was. Injecting
// a header naming an invalid span would be worse than sending none.
func TestTransportWithoutASpanPassesTheRequestThrough(t *testing.T) {
	var seen nethttp.Header
	inner := roundTripperFunc(func(r *nethttp.Request) (*nethttp.Response, error) {
		seen = r.Header
		return &nethttp.Response{StatusCode: nethttp.StatusOK, Body: nethttp.NoBody}, nil
	})
	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, "http://example.test/", nil)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	req.Header.Set("X-Kept", "yes")

	if _, err := observability.Transport(inner).RoundTrip(req); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if got := seen.Get("traceparent"); got != "" {
		t.Errorf("a traceparent header was injected with no span in the context: %q", got)
	}
	if got := seen.Get("X-Kept"); got != "yes" {
		t.Errorf("the request's own headers were not passed through: X-Kept is %q", got)
	}
}

// TestPropagatorIsStable pins that both sides of a boundary get one propagator.
// The upstream default is an empty composite that injects nothing and reports
// no error, so a second construction that happened to be empty would be
// invisible until a trace was found to be broken across a hop.
func TestPropagatorIsStable(t *testing.T) {
	carrier := propagation.HeaderCarrier{}
	carrier.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	ctx := observability.Propagator().Extract(context.Background(), carrier)
	if sc := trace.SpanContextFromContext(ctx); !sc.IsValid() {
		t.Fatal("the propagator did not extract a W3C trace context, so a link through it would break")
	}
}
