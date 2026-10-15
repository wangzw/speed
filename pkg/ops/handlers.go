package ops

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// product is what the ops module builds: this module's configuration, the
// merged document the routes answer with, the router the readiness probe reads
// the endpoints from, and the optional logger and database.
type product struct {
	config   moduleConfig
	logger   *slog.Logger
	router   speedhttp.Router
	database db.Database
	document []byte
}

// health is what the liveness route answers with.
type health struct {
	Status string `json:"status"`
}

// readiness is what the readiness route answers with: the verdict and, when it
// is unavailable, which parts failed.
type readiness struct {
	Status   string   `json:"status"`
	Failures []string `json:"failures,omitempty"`
}

// reportHealth answers that the process is alive. It reads nothing.
func reportHealth(c *gin.Context) {
	c.JSON(http.StatusOK, health{Status: "ok"})
}

// reportReady answers whether this process can serve: every declared endpoint
// accepting requests and, when one is assembled, the database answering. Each
// failure is named, so a probe's log says which half was down. The verdict is
// read fresh on every request — no endpoint state is cached and the database is
// pinged each time — so a reader sees the state as it is now.
func (p *product) reportReady(c *gin.Context) {
	var failures []string
	for _, name := range p.router.Endpoints() {
		endpoint, err := p.router.Endpoint(name)
		switch {
		case err != nil:
			failures = append(failures, fmt.Sprintf("endpoint %q is not accepting: %v", name, err))
		case !endpoint.Accepting():
			failures = append(failures, fmt.Sprintf("endpoint %q is not accepting", name))
		}
	}
	if p.database != nil {
		if pool, err := p.database.DB().DB(); err != nil {
			failures = append(failures, "the database handle has no connection pool: "+err.Error())
		} else if err := pool.PingContext(c.Request.Context()); err != nil {
			failures = append(failures, "the database does not answer: "+err.Error())
		}
	}
	if len(failures) > 0 {
		c.JSON(http.StatusServiceUnavailable, readiness{Status: "unavailable", Failures: failures})
		return
	}
	c.JSON(http.StatusOK, readiness{Status: "ready"})
}

// serveDocument answers the merged OpenAPI document. It is merged once in Init
// and never recomputed, so every request reads the same bytes.
func (p *product) serveDocument(c *gin.Context) {
	c.Data(http.StatusOK, "application/json; charset=utf-8", p.document)
}
