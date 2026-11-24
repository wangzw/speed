package observability

import (
	nethttp "net/http"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Transport wraps base so that an outbound request carries the span
// context of the request it was made for, letting the receiving process
// continue the same trace.
func Transport(base nethttp.RoundTripper) nethttp.RoundTripper {
	if base == nil {
		base = nethttp.DefaultTransport
	}
	return spanTransport{base: base}
}

type spanTransport struct{ base nethttp.RoundTripper }

func (t spanTransport) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) {
	if !trace.SpanContextFromContext(req.Context()).IsValid() {
		return t.base.RoundTrip(req)
	}
	// Clone copies the header, so injecting into the copy leaves the caller's
	// header alone.
	clone := req.Clone(req.Context())
	propagator.Inject(req.Context(), propagation.HeaderCarrier(clone.Header))
	return t.base.RoundTrip(clone)
}

func (t spanTransport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}
