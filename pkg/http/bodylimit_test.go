package http

import (
	"errors"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestAnOversizedDeclaredBodyIsRefusedAsTooLarge pins the first step of the body
// limit: a request that declares a Content-Length over the endpoint's limit is
// refused with a 413 before any handler runs, and through Fail, so the response
// carries the entry point's error shape rather than a bare status. The cause is
// recorded in the gin context, which is where the observability layer reads it,
// and a log record is written.
func TestAnOversizedDeclaredBodyIsRefusedAsTooLarge(t *testing.T) {
	logger, records := newRecordingLogger()
	s := testSettings("public")
	s.maxBodyBytes = 16

	var ran bool
	var errsAfter []error
	handler := mustAssemble(t, s, logger, logger, func(e Endpoint) {
		// The watcher is in the observe stage, outside the body limit, so it
		// runs whether or not the limit refuses the request and can read the
		// error list the limit left on the shared context.
		e.Use(Middleware{Name: "watcher", Stage: StageObserve, Handler: func(c *gin.Context) {
			c.Next()
			for _, gerr := range c.Errors {
				errsAfter = append(errsAfter, gerr.Err)
			}
		}})
		e.Route("POST", "/things", func(c *gin.Context) {
			ran = true
			c.Status(nethttp.StatusNoContent)
		})
	})

	// A body whose declared length is over the limit, which is the case that is
	// refused outright.
	body := strings.NewReader(strings.Repeat("x", 64))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodPost, "/things", body))

	if recorder.Code != nethttp.StatusRequestEntityTooLarge {
		t.Errorf("a request declaring %d bytes against a limit of %d was answered %d, want %d",
			body.Size(), s.maxBodyBytes, recorder.Code, nethttp.StatusRequestEntityTooLarge)
	}
	if ran {
		t.Error("the handler ran for a request the body limit refused")
	}
	if got := recorder.Header().Get("Content-Type"); got != problemMediaType {
		t.Errorf("the refusal's media type is %q, want the default shape's %q", got, problemMediaType)
	}
	if !slices.ContainsFunc(errsAfter, isMaxBytesError) {
		t.Errorf("the gin context's errors are %v, want the MaxBytesError the observability layer reads",
			errsAfter)
	}
	if written := records.at(slog.LevelWarn); len(written) != 1 {
		t.Errorf("the body limit wrote %d records for the refusal, want one: %v", len(written), written)
	}
}

// TestAnOversizedChunkedBodyIsRecordedAsAMaxBytesError pins the second step: a
// request whose length is not declared is bounded by wrapping its body, and the
// read that crosses the limit ends with a *http.MaxBytesError, is recorded in
// the gin context's error list, and is written down once. A handler is not
// required to cooperate: whatever reads the body ends with the error.
func TestAnOversizedChunkedBodyIsRecordedAsAMaxBytesError(t *testing.T) {
	logger, records := newRecordingLogger()
	s := testSettings("public")
	s.maxBodyBytes = 16

	var readErr error
	var errsAfter []error
	handler := mustAssemble(t, s, logger, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "watcher", Stage: StageObserve, Handler: func(c *gin.Context) {
			c.Next()
			for _, gerr := range c.Errors {
				errsAfter = append(errsAfter, gerr.Err)
			}
		}})
		e.Route("POST", "/things", func(c *gin.Context) {
			_, readErr = io.ReadAll(c.Request.Body)
		})
	})

	// A body whose length is not declared, so the layer wraps it rather than
	// refusing it outright.
	body := io.NopCloser(strings.NewReader(strings.Repeat("x", 64)))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(nethttp.MethodPost, "/things", body))

	var tooLarge *nethttp.MaxBytesError
	if !errors.As(readErr, &tooLarge) {
		t.Fatalf("reading an undeclared-length body over the limit ended with %v, want an "+
			"*http.MaxBytesError", readErr)
	}
	if tooLarge.Limit != s.maxBodyBytes {
		t.Errorf("the error's limit is %d, and the endpoint is configured with %d", tooLarge.Limit, s.maxBodyBytes)
	}
	if !slices.ContainsFunc(errsAfter, isMaxBytesError) {
		t.Errorf("the gin context's errors are %v, want the MaxBytesError in them", errsAfter)
	}
	if written := records.at(slog.LevelWarn); len(written) != 1 {
		t.Errorf("the body limit wrote %d records for the over-limit read, want one: %v", len(written), written)
	}
}

// isMaxBytesError reports whether err is a *http.MaxBytesError, for the
// assertions that the error reached a place rather than that a read produced it.
func isMaxBytesError(err error) bool {
	var tooLarge *nethttp.MaxBytesError
	return errors.As(err, &tooLarge)
}
