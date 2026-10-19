package ops

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

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

// reportHealth answers that the process is alive. It reads nothing.
func reportHealth(context.Context, struct{}) (OpsHealth, error) {
	return OpsHealth{Status: "ok"}, nil
}

// reportReady answers whether this process can serve: every declared endpoint
// accepting requests and, when one is assembled, the database answering. Each
// failure is named, so a probe's log says which half was down. The verdict is
// read fresh on every request — no endpoint state is cached and the database is
// pinged each time — so a reader sees the state as it is now.
//
// The answer is built rather than returned as a typed success body because an
// unavailable process is a 503 carrying this same readiness document, not a
// problem document; both statuses are declared against OpsReadiness.
func (p *product) reportReady(ctx context.Context, _ struct{}, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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
		} else if err := pool.PingContext(ctx); err != nil {
			failures = append(failures, "the database does not answer: "+err.Error())
		}
	}
	if len(failures) > 0 {
		return speedhttp.JSONReply(http.StatusServiceUnavailable,
			OpsReadiness{Status: "unavailable", Failures: failures}), nil
	}
	return speedhttp.JSONReply(http.StatusOK, OpsReadiness{Status: "ready"}), nil
}

// serveDocument answers the merged OpenAPI document. It is merged once in Init
// and never recomputed, so every request reads the same bytes. The bytes are
// already an encoded document, so they are written as they are rather than
// re-encoded from a value.
func (p *product) serveDocument(context.Context, struct{}, *speedhttp.Meta) (speedhttp.Reply, error) {
	return speedhttp.BytesReply(http.StatusOK, "application/json; charset=utf-8", p.document), nil
}
