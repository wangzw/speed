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

// middlewareFor builds the capture layer registered on one endpoint.
//
// Provides carries the same token the module descriptor delivers the capability
// under. That is the only anchor another layer has for placing itself inside or
// outside audit, which makes it this module's contract surface on the chain.
//
// It sits in the recording stage, which runs after the identifying and the
// throttling stages. That placement is the stage's: the identity and the tenant
// it records are the ones those earlier stages settled, and a request that a
// rate limiting layer refused never reaches here, so an over-limit request is
// not recorded. An assembly without authentication runs this layer with an
// entry whose actor — and, without tenant resolution, whose tenant — is empty.
//
// The layer runs inside the engine, after the route is matched, so it compares
// what the engine settled and builds no reading of route syntax of its own. It
// puts nothing in the writer's place: the status it records is the one the
// engine's writer holds, and the writer's own optional interfaces are the
// handlers' to reach as they are.
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
			// The request is the one this layer received: its context carries
			// the identity and the tenant the layers outside this one settled,
			// and a layer inside may hand the chain another request in its
			// place.
			r := c.Request
			// The row is written after the inner chain has returned, because
			// the outcome does not exist before then. A layer that wrote on the
			// way in could fill that column with a constant and nothing else,
			// and a result column holding a constant is worth nothing to
			// whoever reads it.
			returned := false
			defer func() {
				// A chain that did not return is a request an audit trail least
				// wants to lose: a handler that panicked, one that stopped its
				// goroutine with runtime.Goexit, and one that panicked with nil
				// under GODEBUG=panicnil=1 all end here with no status to read,
				// and a status code would be a guess. Nothing is recovered, so
				// whatever is unwinding the stack goes on exactly as it was: this
				// layer's job is to record what happened, not to change what
				// happens. The entry point's recovery layer, which sits outside
				// this one and inside the observe stage, answers it.
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
// method and the route path it was dispatched under equal the method and the
// path of an entry of the requests input item.
//
// The route path is empty when the engine matched no route — a path nothing is
// registered on, or a method the matched path does not allow — and no entry has
// an empty path, so such a request is never recorded.
func (j *journal) requested(method, route string) bool {
	return slices.Contains(j.settings.requests, requestItem{method: method, path: route})
}

// answeredWith is the final status a caller is answered with, given the one the
// writer holds.
//
// An informational status the handler set — 103 Early Hints, say — is not a
// final one: net/http sends it ahead of the response and answers with the status
// that follows. The writer holds the status until the header is committed and
// then hands it on as an interim response, so a handler that goes on to write a
// body is answered with the 200 net/http supplies while the writer still reports
// the informational status. A status the handler writes before the commit
// replaces the one in the writer, and is the one reported. 101 Switching
// Protocols is the exception net/http makes as well: it is the last thing
// written before the connection changes hands, so it is the final status.
func answeredWith(status int) int {
	if status >= 100 && status < 200 && status != nethttp.StatusSwitchingProtocols {
		return nethttp.StatusOK
	}
	return status
}

// panicOutcome is what the result column holds for a request whose handler
// panicked. The status never arrived, so a status code would be a guess.
const panicOutcome = "panic"

// recordRequest writes the row for one matched request.
//
// The action is the method and the object is the route path: which kind of
// request it was. The path the client asked for names which one, and goes into
// the detail beside the endpoint name, because neither is one of the entry's
// fields and the layer does know where it was registered and what was asked.
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
