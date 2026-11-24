package ops

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
)

type product struct {
	config   moduleConfig
	logger   *slog.Logger
	router   speedhttp.Router
	database db.Database
	document []byte
}

func reportHealth(context.Context, struct{}) (OpsHealth, error) {
	return OpsHealth{Status: "ok"}, nil
}

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

func (p *product) serveDocument(context.Context, struct{}, *speedhttp.Meta) (speedhttp.Reply, error) {
	return speedhttp.BytesReply(http.StatusOK, "application/json; charset=utf-8", p.document), nil
}
