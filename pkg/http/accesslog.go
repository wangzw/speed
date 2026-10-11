package http

import (
	nethttp "net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/log"
)

// accessLogName is the access log layer as it appears in the assembled chain's
// description.
const accessLogName = "http:access-log"

// accessLog is the built-in layer that writes one record per request. It sits
// outside every registered layer — between holdAllow and the observe stage — so
// it sees every request, including the ones a layer refuses and the ones that
// match no route, and it reads the outcome after the rest of the chain has run.
//
// The record is written to the logger out of the request context
// (log.FromContext), which is the one the entry point injected, so the trace and
// span identifiers an observability layer bound come through as attributes of
// that logger without this layer naming them.
//
// The request is read again after c.Next rather than the one captured before it:
// a layer inside this one — an observability guard binding the trace and span
// identifiers among them — replaces c.Request wholesale with one carrying a
// derived context, so the pointer captured on the way in would not carry the
// fields those layers bound. Reading c.Request after the call takes the context
// the inner chain left, and the identifiers with it. This is the opposite of
// what the audit layer does: it sits inside the layer that binds the fields, so
// it keeps the request it received.
func accessLog(endpoint string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		r := c.Request
		log.FromContext(r.Context()).Info("an HTTP request was served",
			"endpoint", endpoint,
			"method", r.Method,
			"route", c.FullPath(),
			"path", r.URL.Path,
			"status", answeredWith(c.Writer.Status()),
			"size", c.Writer.Size(),
		)
	}
}

// answeredWith is the final status a caller is answered with, given the one the
// writer holds.
//
// An informational status the handler set — 103 Early Hints, say — is not a
// final one: net/http sends it ahead of the response and answers with the status
// that follows, so a record naming the informational status would say the caller
// was answered 103 when the answer that arrived was the 200 that follows.
// 101 Switching Protocols is the exception net/http makes as well: it is the last
// thing written before the connection changes hands.
//
// The audit module's journal keeps its own copy of this rule. The two cannot
// share one: audit imports this package (its layer is registered through the
// surface), so this package importing audit back is a cycle, and the rule is two
// lines rather than a surface worth lifting into a third package.
func answeredWith(status int) int {
	if status >= 100 && status < 200 && status != nethttp.StatusSwitchingProtocols {
		return nethttp.StatusOK
	}
	return status
}
