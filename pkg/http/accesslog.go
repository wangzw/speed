package http

import (
	nethttp "net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/log"
)

const accessLogName = "http:access-log"

func accessLog(endpoint string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		// Read c.Request again: an inner layer replaces it with a request whose
		// context carries its bindings.
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

// answeredWith folds an informational status to 200, except 101: a 1xx is
// followed by a final status, and recording it would log a 200 as 103.
func answeredWith(status int) int {
	if status >= 100 && status < 200 && status != nethttp.StatusSwitchingProtocols {
		return nethttp.StatusOK
	}
	return status
}
