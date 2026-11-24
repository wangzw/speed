package stream

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

var pathStream = streamSpec.ResolvePath(pathStreamFragment)

func (p *product) mount(router speedhttp.Router) error {
	type target struct {
		name     string
		endpoint speedhttp.Endpoint
	}
	targets := make([]target, 0, len(p.cfg.endpoints))
	for _, name := range p.cfg.endpoints {
		endpoint, err := router.Endpoint(name)
		if err != nil {
			return fmt.Errorf("%s: %w: %q: %w", ModuleName, speedhttp.ErrUnknownEndpoint, name, err)
		}
		targets = append(targets, target{name: name, endpoint: endpoint})
	}
	handler := p.guardStream(subscribeOperation.Handle(p.handleStream))
	for _, tgt := range targets {
		tgt.endpoint.Route(subscribeOperation.Method, pathStream, handler)
	}
	return nil
}

func (p *product) handleStream(ctx context.Context, _ streamInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return nil, speedhttp.Fault{Status: http.StatusUnauthorized}
	}

	return speedhttp.SSEReply(http.StatusOK, func(w *speedhttp.SSEWriter) error {
		conn := p.hub.Subscribe(tenantOf(ctx), string(id.Account()))
		// Deregister the connection before the answer ends, whatever ends it.
		defer p.hub.Close(conn)

		return p.stream(ctx, w, conn)
	}), nil
}

func (p *product) guardStream(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := authn.FromContext(c.Request.Context()); !ok {
			next(c)
			return
		}
		if _, ok := flusherOf(c.Writer); !ok {
			speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusInternalServerError})
			return
		}
		next(c)
	}
}

func (p *product) stream(ctx context.Context, w *speedhttp.SSEWriter, conn *connection) error {
	draining := speedhttp.Draining(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-draining:
			return nil
		case <-conn.done:
			return nil
		case f := <-conn.ch:
			if err := w.Send(f.event, f.data); err != nil {
				return err
			}
		}
	}
}

func flusherOf(w http.ResponseWriter) (http.Flusher, bool) {
	for w != nil {
		if f, ok := w.(http.Flusher); ok {
			return f, true
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil, false
		}
		w = u.Unwrap()
	}
	return nil, false
}
