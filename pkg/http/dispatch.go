package http

import (
	"fmt"
	nethttp "net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/i18n"
)

// catchAll reports whether any route this endpoint registered carries a
// catch-all segment. Such an endpoint is a listener's fallback by construction:
// its own route table covers whatever path arrives, so the dispatch hands it the
// requests no endpoint registered. In gin a catch-all can sit beside no other
// route on the same method tree, which is why the dispatch mounts the fallback
// as the listener's NoRoute rather than as a route of its own.
func (e *endpoint) catchAll() bool {
	for _, r := range e.routes {
		if strings.Contains(r.path, "*") {
			return true
		}
	}
	return false
}

// buildDispatch builds the handler one listener serves every accepted
// connection from.
//
// The dispatch is a gin engine that routes by (method, path): each route an
// endpoint registered is mounted here, and its handler hands the request to
// that endpoint's own assembled engine, which matches the request again and
// runs the endpoint's chain. What no endpoint registered goes to the listener's
// fallback endpoint, or, with none, to a 404. A path the method tree knows under
// another method is answered 405 with the Allow header.
//
// The fallback is the endpoint that receives a request no endpoint registered.
// A catch-all endpoint is one by construction; so is the only endpoint of a
// listener, because a listener with one endpoint has no routing to do — every
// request belongs to that endpoint, and mounting its routes here would only
// reproduce what its own engine already does. Every other endpoint is mounted by
// (method, path).
//
// Two things the listener cannot hold are refused here as startup failures: a
// second catch-all endpoint, and two endpoints claiming the same (method, path).
// Both are ErrRouteConflict, and both name the listener and the two endpoints.
func buildDispatch(name string, endpoints []*endpoint, localizer i18n.Localizer) (nethttp.Handler, error) {
	dispatcher := gin.New()
	dispatcher.RedirectTrailingSlash = false
	dispatcher.RedirectFixedPath = false
	dispatcher.HandleMethodNotAllowed = true

	var catchAlls []*endpoint
	for _, e := range endpoints {
		if e.catchAll() {
			catchAlls = append(catchAlls, e)
		}
	}
	if len(catchAlls) > 1 {
		return nil, fmt.Errorf("%w: listener %q carries two catch-all endpoints, %q and %q. "+
			"A listener hands every request no endpoint registered to its one fallback endpoint, "+
			"because a catch-all covers a whole path space and excludes every other route on the "+
			"same method tree; a second catch-all has no request it could ever be reached by. "+
			"Give each listener at most one endpoint whose routes carry a catch-all, or move one "+
			"of them to another listener",
			ErrRouteConflict, name, catchAlls[0].settings.name, catchAlls[1].settings.name)
	}

	var fallback *endpoint
	switch {
	case len(catchAlls) == 1:
		fallback = catchAlls[0]
	case len(endpoints) == 1:
		fallback = endpoints[0]
	}

	registered := make(map[string]*endpoint)
	for _, e := range endpoints {
		if e == fallback {
			continue
		}
		for _, r := range e.routes {
			key := r.method + " " + r.path
			if prev, ok := registered[key]; ok {
				return nil, fmt.Errorf("%w: listener %q: the endpoints %q and %q both register "+
					"the route %q. A listener routes each (method, path) to the endpoint that "+
					"registered it, and one route belongs to one endpoint only. Change one of the "+
					"routes or move it to another endpoint",
					ErrRouteConflict, name, prev.settings.name, e.settings.name, r.String())
			}
			registered[key] = e
			if raised := mountDispatch(dispatcher, r, e); raised != nil {
				return nil, fmt.Errorf("%w: listener %q: endpoint %q could not mount the route "+
					"%q on the listener: %v. Two endpoints on one listener mount into one router, "+
					"so routes gin refuses beside each other on one method tree — a parameter "+
					"named differently at one position, a catch-all beside a static segment — are "+
					"refused here too. Change the route or move one of the endpoints to another "+
					"listener",
					ErrRouteConflict, name, e.settings.name, r.String(), raised)
			}
		}
	}

	if fallback != nil {
		dispatcher.NoRoute(delegate(fallback))
	} else {
		dispatcher.NoRoute(func(c *gin.Context) {
			Fail(c, Problem{
				Status: nethttp.StatusNotFound,
				Title:  refusalTitle(c.Request.Context(), localizer, nethttp.StatusNotFound),
			})
		})
	}
	dispatcher.NoMethod(func(c *gin.Context) {
		// gin has already set the Allow header for the matched path; Fail keeps
		// the headers set before it, so the caller still learns the methods the
		// path allows.
		Fail(c, Problem{
			Status: nethttp.StatusMethodNotAllowed,
			Title:  refusalTitle(c.Request.Context(), localizer, nethttp.StatusMethodNotAllowed),
		})
	})
	return dispatcher, nil
}

// mountDispatch mounts one endpoint's route on the listener's dispatch engine,
// handing back whatever gin raised, or nil when it took the route.
func mountDispatch(dispatcher *gin.Engine, r route, e *endpoint) (raised any) {
	defer func() { raised = recover() }()
	dispatcher.Handle(r.method, r.path, delegate(e))
	return nil
}

// delegate is the handler the dispatch mounts for one endpoint's route: it hands
// the request to that endpoint's assembled engine, which matches it again and
// runs the endpoint's chain.
func delegate(e *endpoint) gin.HandlerFunc {
	return func(c *gin.Context) { e.handler.ServeHTTP(c.Writer, c.Request) }
}
