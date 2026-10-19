package stream

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// pathStream is the absolute path the stream route is mounted on: the path the
// fragment declares, resolved against the API root the module's Spec declares.
// It is the module's whole route surface, and it is a long-lived connection:
// the answer is one text/event-stream.
var pathStream = streamSpec.ResolvePath(pathStreamFragment)

// mount resolves every configured endpoint on the entry point and registers the
// stream route on each. Every name is looked up before any route is registered,
// so a name the entry point does not declare fails the startup naming that
// endpoint rather than leaving a partial mounting behind.
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

// handleStream serves GET /stream. It reads the caller, subscribes a connection
// under (tenant, recipient) when the answer begins, and writes one frame per
// event until the connection ends.
//
// The recipient is the identity's effective account, Identity.Account, so an
// impersonating caller sees the impersonated account's inbox. A request with no
// identity is a 401: there is no recipient to scope the connection to.
//
// The answer is one event stream the entry point writes: it sets the declared
// content type and status before the callback runs, and each frame the callback
// sends is flushed before the next one is built. The subscription is made and
// released inside the callback, so it lives exactly as long as the stream does.
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

// guardStream holds the one check the declared handler cannot make: the
// response writer must be able to flush. A writer with no flusher anywhere down
// the chain cannot refresh a stream, and there is no fallback to a one-shot
// body, so the route answers the declared 500.
//
// The typed handler receives no response writer, so the check stays at the
// mount, in front of the handler. The identity is read first, which keeps the
// route's original order: a request with neither an identity nor a flushing
// writer still answers the declared 401.
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

// stream writes the connection's frames until the connection ends. It ends on
// the client disconnecting (the request context), the entry point draining (the
// drain signal), this module's Stop or the handler's own Close (the connection's
// done), and returns on the first write failure. The drain signal is separate
// from the request context because the request context is not cancelled by a
// drain; a handler watching only it would run until the drain timeout cut it.
//
// An error from the writer ends the answer: the response has already begun, so
// the entry point abandons the connection rather than trying to write a
// different answer.
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

// flusherOf walks the response-writer chain looking for an http.Flusher. A layer
// that replaced the writer leaves both Flusher and Unwrap behind, so the module
// unwraps until it finds one rather than assuming the outermost writer is it.
// It reports false when the chain ends with no flusher.
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
