package log

import (
	"errors"
	"net/http"
	"net/url"
	"time"
)

// The outcome values of an outbound request record. The set is closed: a
// dashboard or an alert written against it may rely on these two words.
const (
	// outcomeOK is a request the peer answered with a 2xx status.
	outcomeOK = "ok"
	// outcomeFailed is a request that did not get a 2xx answer: the call
	// itself failed, or the peer answered with a status outside 2xx.
	outcomeFailed = "failed"
)

// Transport wraps base so that every outbound HTTP request it carries leaves a
// record, whether it succeeded or failed.
//
// The record names the exchange and never its contents: the method, the host,
// the path without the query, the status the peer answered with where there was
// one, the outcome, and how long the call took in milliseconds. A call that
// never produced a response carries the underlying failure instead of a status.
// The query, the headers and both bodies are never recorded: an authorization
// code, a token, a state, a cookie, a signed assertion and a message body all
// travel there, and a record that carried one would put it in every destination
// the logging chain writes to.
//
// The record is written through the logger the request's context carries, the
// one FromContext returns, so it lands on the configured destinations and joins
// whatever the caller has bound for that request. A request whose context
// carries no logger still writes the record, through Default.
//
// A nil base means net/http's default transport, matching what a nil Transport
// field on a Client means.
//
// A call site wraps the transport its client already holds, so that every
// request the client makes passes through one wrapper: a client whose
// transport was not wrapped makes its calls unrecorded, and no layer reports
// the omission.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return loggingTransport{base: base}
}

type loggingTransport struct{ base http.RoundTripper }

// RoundTrip calls the wrapped transport and records the exchange. The response
// and the error are handed back untouched.
//
// A 2xx status is the success the record reports. Any other status is a
// failure, redirects included: the clients this repository assembles are built
// to follow no redirect, so a 3xx says the request ended somewhere other than
// the resource it named, which is the thing an operator has to see.
func (t loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	started := time.Now()
	resp, err := t.base.RoundTrip(req)
	elapsed := time.Since(started).Milliseconds()

	method, host, path := targetOf(req)
	logger := FromContext(req.Context())
	switch {
	case err != nil:
		logger.Error("log: an outbound HTTP request failed",
			"method", method,
			"host", host,
			"path", path,
			"outcome", outcomeFailed,
			"duration_ms", elapsed,
			"cause", causeOf(err))
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		logger.Error("log: an outbound HTTP request failed",
			"method", method,
			"host", host,
			"path", path,
			"http_status", resp.StatusCode,
			"outcome", outcomeFailed,
			"duration_ms", elapsed)
	default:
		logger.Info("log: an outbound HTTP request completed",
			"method", method,
			"host", host,
			"path", path,
			"http_status", resp.StatusCode,
			"outcome", outcomeOK,
			"duration_ms", elapsed)
	}
	return resp, err
}

// CloseIdleConnections forwards to the wrapped transport when it has the method.
//
// http.Client finds it by assertion and does nothing at all when it is missing,
// so a client whose transport was wrapped would quietly keep its connections
// past the point the caller asked for them to go.
func (t loggingTransport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// targetOf reads the three request fields a record carries. The query and the
// fragment are deliberately not among them: a query carries authorization
// codes, tokens and state values, and the path is enough to name the endpoint
// that was called.
func targetOf(req *http.Request) (method, host, path string) {
	method = req.Method
	if req.URL == nil {
		return method, "", ""
	}
	return method, req.URL.Host, req.URL.Path
}

// causeOf is the failure text a record carries: the error underneath the
// url.Error net/http wraps a failed call in, because that wrapper's own text
// quotes the request URL — query included, and the query is exactly where a
// code or a token travels. The wrapped error names the failure class (a refused
// connection, a certificate, a refused scheme) and no part of the request's
// address beyond the host it tried.
func causeOf(err error) string {
	var u *url.Error
	if errors.As(err, &u) {
		return u.Err.Error()
	}
	return err.Error()
}
