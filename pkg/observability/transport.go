package observability

import (
	nethttp "net/http"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Transport wraps base so that an outbound request carries the span context of
// the request it was made for, letting the receiving process continue the same
// trace.
//
// It takes no provider and consults no registry: what it injects is the span
// the caller already put in the context, so wrapping a client is a local
// decision. Nothing hands it out automatically — a call site that does not wrap
// its client breaks the trace across that hop, and no layer reports it, which
// is one of the properties the module documents rather than guarantees.
//
// A nil base means net/http's default transport, matching what a nil Transport
// field on a Client means.
func Transport(base nethttp.RoundTripper) nethttp.RoundTripper {
	if base == nil {
		base = nethttp.DefaultTransport
	}
	return spanTransport{base: base}
}

type spanTransport struct{ base nethttp.RoundTripper }

// RoundTrip injects into a copy of the request.
//
// The copy is what keeps this a pure decoration: the caller's request, and the
// headers it handed in, are left as they were, so a RoundTripper that retries or
// that inspects its own request afterwards sees what it wrote rather than a
// header this layer added.
//
// A context with no valid span passes through untouched. Background work,
// startup and anything else outside a traced call site reaches here without a
// span, and injecting nothing is the right answer for them: a trace header
// naming an invalid span would be worse than no header at all.
func (t spanTransport) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) {
	if !trace.SpanContextFromContext(req.Context()).IsValid() {
		return t.base.RoundTrip(req)
	}
	// Clone already copies the header, so injecting into the copy's header
	// leaves the caller's alone.
	clone := req.Clone(req.Context())
	propagator.Inject(req.Context(), propagation.HeaderCarrier(clone.Header))
	return t.base.RoundTrip(clone)
}

// CloseIdleConnections forwards to the wrapped transport when it has the method.
//
// http.Client finds it by assertion and does nothing at all when it is missing,
// so a client whose transport was wrapped would quietly keep its connections
// past the point the caller asked for them to go — the same class of silent
// loss the request layer avoids on the server side by exposing the writer
// underneath.
func (t spanTransport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}
