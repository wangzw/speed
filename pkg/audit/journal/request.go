package journal

import (
	nethttp "net/http"
	"slices"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func (j *journal) middlewareFor(endpoint string) speedhttp.Middleware {
	return speedhttp.Middleware{
		Name:     ModuleName,
		Provides: []core.Token{(*audit.Audit)(nil)},
		Stage:    speedhttp.StageRecord,
		Handler: func(c *gin.Context) {
			route := c.FullPath()
			if !j.requested(c.Request.Method, route) {
				c.Next()
				return
			}
			r := c.Request
			// The row is written after the inner chain has returned, because the
			// outcome does not exist before then.
			returned := false
			defer func() {
				// A chain that did not return — a panic or runtime.Goexit — ends here
				// with no status to read.
				if !returned {
					j.recordRequest(r, endpoint, route, panicOutcome)
				}
			}()
			c.Next()
			returned = true
			j.recordRequest(r, endpoint, route, strconv.Itoa(answeredWith(c.Writer.Status())))
		},
	}
}

// requested reports whether a request is one the configuration names: its
// method and the route path it was dispatched under equal the method and
// the path of an entry of the requests input item.
func (j *journal) requested(method, route string) bool {
	return slices.Contains(j.settings.requests, requestItem{method: method, path: route})
}

func answeredWith(status int) int {
	if status >= 100 && status < 200 && status != nethttp.StatusSwitchingProtocols {
		return nethttp.StatusOK
	}
	return status
}

// panicOutcome is what the result column holds for a request whose handler
// panicked. The status never arrived, so a status code would be a guess.
const panicOutcome = "panic"

func (j *journal) recordRequest(r *nethttp.Request, endpoint, route, outcome string) {
	ctx := r.Context()
	j.submit(ctx, assemble(ctx, audit.SourceRequest, audit.Entry{
		Action:  r.Method,
		Object:  route,
		Outcome: outcome,
		Detail: map[string]string{
			"endpoint": endpoint,
			"path":     r.URL.Path,
		},
	}, ""))
}
