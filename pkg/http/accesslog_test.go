package http

import (
	"bytes"
	"log/slog"
	nethttp "net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/log"
)

// TestTheAccessLogCarriesTheFieldsAnInnerLayerBound pins why the access log reads c.Request again
// after c.Next.
func TestTheAccessLogCarriesTheFieldsAnInnerLayerBound(t *testing.T) {
	var buf bytes.Buffer
	injected := slog.New(slog.NewTextHandler(&buf, nil))
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), injected, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "binder", Stage: StageObserve, Handler: func(c *gin.Context) {
			c.Request = c.Request.WithContext(log.WithAttrs(c.Request.Context(),
				"trace_id", "trace-abc123", "span_id", "span-def456"))
			c.Next()
		}})
		e.Route("GET", "/things", filler)
	})

	serveOnce(handler, nethttp.MethodGet, "/things")

	out := buf.String()
	mustContain(t, out, `msg="an HTTP request was served"`, "the access log's record")
	mustContain(t, out, "trace_id=trace-abc123", "the trace identifier the inner layer bound")
	mustContain(t, out, "span_id=span-def456", "the span identifier the inner layer bound")
}

// TestAnsweredWithFoldsAnInformationalStatus pins the status the access log records for a response
// that ends without a final status.
func TestAnsweredWithFoldsAnInformationalStatus(t *testing.T) {
	for _, c := range []struct{ status, want int }{
		{nethttp.StatusEarlyHints, nethttp.StatusOK},
		{nethttp.StatusProcessing, nethttp.StatusOK},
		{nethttp.StatusSwitchingProtocols, nethttp.StatusSwitchingProtocols},
		{nethttp.StatusOK, nethttp.StatusOK},
		{nethttp.StatusNotFound, nethttp.StatusNotFound},
	} {
		if got := answeredWith(c.status); got != c.want {
			t.Errorf("answeredWith(%d) is %d, want %d", c.status, got, c.want)
		}
	}
}
