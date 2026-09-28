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

// TestAnInboundBaggageIsNotForwarded pins what crosses the boundary in both
// directions: the span context and nothing else. The request layer extracts
// with this propagator ahead of authentication, so whatever an unauthenticated
// caller writes into a baggage header would otherwise sit in the request's
// context and be re-emitted by Transport to every third party the request goes
// on to call, under this service's name.
func TestAnInboundBaggageIsNotForwarded(t *testing.T) {
	inbound := nethttp.Header{}
	inbound.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	inbound.Set("baggage", "injected=by-the-caller")
	ctx := observability.Propagator().Extract(context.Background(), propagation.HeaderCarrier(inbound))

	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	ctx, span := tp.Tracer("test").Start(ctx, "handler")
	defer span.End()

	var seen nethttp.Header
	inner := roundTripperFunc(func(r *nethttp.Request) (*nethttp.Response, error) {
		seen = r.Header
		return &nethttp.Response{StatusCode: nethttp.StatusOK, Body: nethttp.NoBody, Header: nethttp.Header{}}, nil
	})
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, "http://webhook.example.test/", nil)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	if _, err := observability.Transport(inner).RoundTrip(req); err != nil {
		t.Fatalf("round trip: %v", err)
	}

	if !strings.Contains(seen.Get("traceparent"), "0af7651916cd43dd8448eb211c80319c") {
		t.Errorf("the outbound traceparent %q does not continue the caller's trace", seen.Get("traceparent"))
	}
	if got := seen.Get("baggage"); got != "" {
		t.Errorf("the caller's baggage reached a third party as %q, want no baggage header", got)
	}
	for _, field := range observability.Propagator().Fields() {
		if field == "baggage" {
			t.Errorf("the propagator reads and writes %v, want the trace context headers alone",
				observability.Propagator().Fields())
		}
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

// TestTransportForwardsCloseIdleConnections pins the one optional method a
// client looks for by assertion. http.Client does nothing at all when it is
// missing, so a wrapped transport that did not forward it would keep the pool
// alive past the point the caller asked for it to go, with nothing to say the
// wrapper was why.
func TestTransportForwardsCloseIdleConnections(t *testing.T) {
	underneath := &closeIdleTransport{}
	closer, ok := observability.Transport(underneath).(interface{ CloseIdleConnections() })
	if !ok {
		t.Fatal("the wrapper does not satisfy the interface http.Client looks for, so CloseIdleConnections " +
			"on a client would silently do nothing")
	}
	closer.CloseIdleConnections()
	if !underneath.closed {
		t.Error("CloseIdleConnections did not reach the transport underneath")
	}

	// A transport that cannot close idle connections is not an error: the call
	// is the same no-op it would have been without the wrapper.
	plain := observability.Transport(roundTripperFunc(func(*nethttp.Request) (*nethttp.Response, error) {
		return nil, nil
	})).(interface{ CloseIdleConnections() })
	plain.CloseIdleConnections()
}

// closeIdleTransport is a transport that can close its idle connections.
type closeIdleTransport struct{ closed bool }

func (c *closeIdleTransport) RoundTrip(*nethttp.Request) (*nethttp.Response, error) { return nil, nil }

func (c *closeIdleTransport) CloseIdleConnections() { c.closed = true }

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
