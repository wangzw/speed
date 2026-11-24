package log

import (
	"errors"
	"net/http"
	"net/url"
	"time"
)

const (
	outcomeOK     = "ok"
	outcomeFailed = "failed"
)

// Transport wraps base so that every outbound HTTP request it carries leaves a record, whether it
// succeeded or failed.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return loggingTransport{base: base}
}

type loggingTransport struct{ base http.RoundTripper }

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

func (t loggingTransport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

func targetOf(req *http.Request) (method, host, path string) {
	method = req.Method
	if req.URL == nil {
		return method, "", ""
	}
	return method, req.URL.Host, req.URL.Path
}

func causeOf(err error) string {
	var u *url.Error
	if errors.As(err, &u) {
		return u.Err.Error()
	}
	return err.Error()
}
