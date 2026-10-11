package http

import (
	"bytes"
	"log/slog"
	nethttp "net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/log"
)

// TestTheAccessLogCarriesTheFieldsAnInnerLayerBound pins why the access log
// reads c.Request again after c.Next. The trace and span identifiers an
// observability layer binds go onto the request context through the logger's
// attributes, and the layer replaces c.Request wholesale with one carrying the
// derived context; a pointer captured before c.Next would carry none of them.
//
// The injected logger is a text handler so the bound attributes are readable in
// the record, which the shared recording handler would drop: it answers WithAttrs
// with itself and keeps nothing.
func TestTheAccessLogCarriesTheFieldsAnInnerLayerBound(t *testing.T) {
	var buf bytes.Buffer
	injected := slog.New(slog.NewTextHandler(&buf, nil))
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), injected, logger, func(e Endpoint) {
		// The binder is in the observe stage, inside the access log, so it runs
		// after the access log has started and before the access log reads the
		// request back.
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

// TestAnsweredWithFoldsAnInformationalStatus pins the status the access log
// records for a response that ends without a final status. net/http follows an
// informational status with the one that comes after it, so recording the
// informational status would say the caller was answered 103 when the answer that
// arrived was the 200. 101 is the exception: it is the last thing written before
// the connection changes hands, so it is the final status.
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
