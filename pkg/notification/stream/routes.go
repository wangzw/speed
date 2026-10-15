package stream

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The stream route, under the fixed base path. It is the module's whole route
// surface, and it is a long-lived connection: the answer is one text/event-stream.
const (
	basePath   = "/api/v1/notifications"
	pathStream = basePath + "/stream"
)

// route is one route: the method and path it answers and the handler that
// serves it.
type route struct {
	method  string
	path    string
	handler gin.HandlerFunc
}

// routes is the closed set of this module's routes, in the order they are
// mounted.
func (p *product) routes() []route {
	return []route{
		{http.MethodGet, pathStream, p.handleStream},
	}
}

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
	for _, tgt := range targets {
		for _, r := range p.routes() {
			tgt.endpoint.Route(r.method, r.path, r.handler)
		}
	}
	return nil
}

// handleStream serves GET /stream. It reads the caller, resolves the writer it
// can flush through, subscribes a connection under (tenant, recipient), sets the
// event-stream content type, and writes one frame per event until the
// connection ends.
//
// The recipient is the identity's effective account, Identity.Account, so an
// impersonating caller sees the impersonated account's inbox. A request with no
// identity is a 401: there is no recipient to scope the connection to.
func (p *product) handleStream(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := authn.FromContext(ctx)
	if !ok {
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
		return
	}
	flusher, ok := flusherOf(c.Writer)
	if !ok {
		// A writer with no flusher anywhere down the chain cannot refresh a
		// stream. There is no fallback to a one-shot body: a stream that cannot
		// flush is not a stream.
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusInternalServerError})
		return
	}

	conn := p.hub.Subscribe(tenantOf(ctx), string(id.Account()))
	// Deregister before the response ends, whatever ends it.
	defer p.hub.Close(conn)

	// The content type is set before any body byte, so the first frame goes out
	// under the stream media type.
	c.Header("Content-Type", "text/event-stream")
	c.Writer.WriteHeaderNow()
	flusher.Flush()

	p.stream(ctx, c.Writer, flusher, conn)
}

// stream writes the connection's frames until the connection ends. It ends on
// the client disconnecting (the request context), the entry point draining (the
// drain signal), this module's Stop or the handler's own Close (the connection's
// done), and returns on the first write failure. The drain signal is separate
// from the request context because the request context is not cancelled by a
// drain; a handler watching only it would run until the drain timeout cut it.
func (p *product) stream(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, conn *connection) {
	draining := speedhttp.Draining(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-draining:
			return
		case <-conn.done:
			return
		case f := <-conn.ch:
			if err := writeFrame(w, flusher, f); err != nil {
				return
			}
		}
	}
}

// writeFrame writes one frame and flushes it. The frame is
// "event: <name>\ndata: <json>\n\n": the event name, the JSON data line, and a
// blank line that ends the event. Both lines and the trailing blank line are
// part of what a connected client parses, so the shape is fixed.
func writeFrame(w http.ResponseWriter, flusher http.Flusher, f frame) error {
	buf := make([]byte, 0, len(f.event)+len(f.data)+len("event: ")+len("data: ")+3)
	buf = append(buf, "event: "...)
	buf = append(buf, f.event...)
	buf = append(buf, '\n')
	buf = append(buf, "data: "...)
	buf = append(buf, f.data...)
	buf = append(buf, '\n', '\n')
	if _, err := w.Write(buf); err != nil {
		return err
	}
	flusher.Flush()
	return nil
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
