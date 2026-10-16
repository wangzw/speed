package http

import (
	"fmt"
	nethttp "net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/i18n"
)

// catchAll reports whether any route this endpoint registered carries a
// catch-all segment. Such an endpoint is a listener's fallback by construction:
// its own route table covers whatever path arrives, so the dispatch hands it the
// requests no endpoint owns. In gin a catch-all can sit beside no other route on
// the same method tree. A catch-all is not a static prefix: it covers a whole
// path space, so the fallback is left out of the prefix a listener routes by.
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
// The dispatch selects one endpoint per request and hands the request to that
// endpoint's assembled engine, which matches it, runs the endpoint's chain and
// answers 200, its own 404 or its own 405. Selection is by the request path and
// the endpoints' static prefixes: the endpoint whose static prefix the path
// falls under answers it, the longest such prefix winning when the prefixes
// nest. A path under no prefix goes to the listener's fallback endpoint, or,
// with none, to a 404.
//
// The static prefixes of an endpoint are read off its registered routes: a route
// contributes the literal path segments it leads with, up to the first dynamic
// segment. A route that is entirely literal contributes its parent path, so an
// endpoint registering /api/v1/objects owns /api/v1 and answers a request for a
// path under it that no route registered — a mistyped or removed route — with
// its own 404 rather than letting the request fall through to a fallback whose
// catch-all would answer 200. A route whose first segment is already dynamic
// contributes no prefix.
//
// The fallback is the endpoint that receives a request no endpoint owns. A
// catch-all endpoint is one by construction; so is the only endpoint of a
// listener, because a listener with one endpoint has no routing to do — every
// request belongs to that endpoint, and its prefixes are not consulted. The
// fallback itself owns no prefix.
//
// Three things the listener cannot hold are refused here as startup failures,
// all ErrRouteConflict and all naming the listener: a second catch-all endpoint;
// two endpoints claiming the same (method, path); and two endpoints whose static
// prefixes collide — the same prefix owned by two endpoints, or an endpoint
// whose routes all begin with a dynamic segment, leaving it with no prefix to be
// selected by at all. Prefixes that nest are not a collision: the longest one
// wins, so an endpoint owning /api/v1 may stand beside one owning
// /api/v1/internal.
func buildDispatch(name string, endpoints []*endpoint, localizer i18n.Localizer) (nethttp.Handler, error) {
	var catchAlls []*endpoint
	for _, e := range endpoints {
		if e.catchAll() {
			catchAlls = append(catchAlls, e)
		}
	}
	if len(catchAlls) > 1 {
		return nil, fmt.Errorf("%w: listener %q carries two catch-all endpoints, %q and %q. "+
			"A listener hands every request no endpoint owns to its one fallback endpoint, "+
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

	// One (method, path) belongs to one endpoint. The fallback is included: it is
	// left out of prefix ownership, not out of the rule, and a route only the
	// fallback registers beside a prefix-owning endpoint's would be unreachable
	// without this check — the prefix owning the path would answer it instead.
	registered := make(map[string]*endpoint)
	for _, e := range endpoints {
		for _, r := range e.routes {
			key := r.method + " " + r.path
			if prev, ok := registered[key]; ok {
				return nil, fmt.Errorf("%w: listener %q: the endpoints %q and %q both register "+
					"the route %q. A listener routes each path to one endpoint, and one route "+
					"belongs to one endpoint only. Change one of the routes or move it to another "+
					"endpoint",
					ErrRouteConflict, name, prev.settings.name, e.settings.name, r.String())
			}
			registered[key] = e
		}
	}

	// A static prefix belongs to one endpoint. Collect them longest-first for the
	// request-time lookup, refusing a prefix two endpoints own.
	d := &dispatch{fallback: fallback}
	seen := make(map[string]*endpoint)
	for _, e := range endpoints {
		if e == fallback {
			continue
		}
		prefixes := e.staticPrefixes()
		if len(prefixes) == 0 {
			return nil, fmt.Errorf("%w: listener %q carries the endpoint %q beside other "+
				"endpoints, but every route it registers begins with a dynamic segment, so it "+
				"owns no static prefix and the listener has no path to select it by. Give the "+
				"endpoint a route under a literal prefix, or move it to a listener of its own",
				ErrRouteConflict, name, e.settings.name)
		}
		for _, p := range prefixes {
			if prev, ok := seen[p]; ok && prev != e {
				return nil, fmt.Errorf("%w: listener %q: the endpoints %q and %q both own the "+
					"path prefix %q. The listener selects an endpoint by the longest prefix a "+
					"request falls under, so one prefix belongs to one endpoint. Change one "+
					"endpoint's routes or move it to another listener",
					ErrRouteConflict, name, prev.settings.name, e.settings.name, p)
			}
			seen[p] = e
			d.entries = append(d.entries, prefixEntry{prefix: p, endpoint: e})
		}
	}
	slices.SortFunc(d.entries, func(a, b prefixEntry) int { return len(b.prefix) - len(a.prefix) })
	return d.handler(localizer), nil
}

// prefixEntry binds one static prefix to the endpoint that owns it.
type prefixEntry struct {
	prefix   string
	endpoint *endpoint
}

// dispatch is a listener's request selector: the endpoints' static prefixes,
// longest first, and the fallback for a path none of them owns.
type dispatch struct {
	entries  []prefixEntry
	fallback *endpoint
}

// handler renders the dispatch as the net/http handler a listener serves. The
// dispatch engine carries no route of its own: every request reaches its NoRoute
// branch, which selects the endpoint and hands the request to that endpoint's
// engine. So the 404 of a listener with no fallback is the dispatch's own, and
// every other answer — a matched route, an endpoint's 404, its 405 — is written
// by the selected endpoint's engine after its chain has run.
func (d *dispatch) handler(localizer i18n.Localizer) nethttp.Handler {
	engine := gin.New()
	engine.RedirectTrailingSlash = false
	engine.RedirectFixedPath = false
	engine.NoRoute(func(c *gin.Context) {
		if e := d.owner(c.Request.URL.Path); e != nil {
			e.handler.ServeHTTP(c.Writer, c.Request)
			return
		}
		if d.fallback != nil {
			d.fallback.handler.ServeHTTP(c.Writer, c.Request)
			return
		}
		Fail(c, Problem{
			Status: nethttp.StatusNotFound,
			Title:  refusalTitle(c.Request.Context(), localizer, nethttp.StatusNotFound),
		})
	})
	return engine
}

// owner returns the endpoint whose static prefix the path falls under, the
// longest such prefix winning, or nil when no prefix covers the path. entries is
// ordered longest-first, so the first prefix that covers the path is the longest.
func (d *dispatch) owner(path string) *endpoint {
	for _, e := range d.entries {
		if fallsUnder(path, e.prefix) {
			return e.endpoint
		}
	}
	return nil
}

// fallsUnder reports whether a request path lies at or below a static prefix:
// equal to it, or continuing past it at a segment boundary. A bare string-prefix
// match is not enough — "/apifoo" does not fall under "/api" — which is what
// keeps an endpoint's ownership to its own segment subtree.
func fallsUnder(path, prefix string) bool {
	if prefix == "/" {
		return true
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// staticPrefixes returns the path prefixes this endpoint owns on its listener,
// read off its registered routes and in ascending order. Each route contributes
// the literal segments it leads with up to the first dynamic segment; a fully
// literal route contributes its parent path; a route whose first segment is
// dynamic contributes nothing. A prefix nested under another prefix of the same
// endpoint is dropped, because the shallower one already covers it.
func (e *endpoint) staticPrefixes() []string {
	set := make(map[string]bool)
	for _, r := range e.routes {
		if p, ok := routePrefix(r.path); ok {
			set[p] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		if !hasAncestor(set, p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// hasAncestor reports whether any prefix in set is a proper ancestor of p.
func hasAncestor(set map[string]bool, p string) bool {
	for q := parentPath(p); q != ""; q = parentPath(q) {
		if set[q] {
			return true
		}
	}
	return false
}

// parentPath returns the parent of a path at the last segment boundary, or ""
// when the path is single-segment or at the root. The root is never a parent:
// "/" is a prefix of every path, and it is not one an endpoint derives from a
// route.
func parentPath(p string) string {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return ""
	}
	return p[:i]
}

// routePrefix returns the static prefix one route contributes to its endpoint's
// ownership, and false when it contributes none because its first segment is
// already dynamic. A path under the prefix — even one no route registered — is
// answered by the endpoint. A fully literal route of two or more segments
// contributes its parent path, so the endpoint answers a path beside or under
// its own routes instead of letting it reach a fallback; the endpoint's own
// routes are then covered by that parent. A single-segment route contributes
// itself.
func routePrefix(path string) (string, bool) {
	segments := pathSegments(path)
	if len(segments) == 0 {
		return "/", true
	}
	cut := len(segments)
	for i, s := range segments {
		if strings.HasPrefix(s, ":") || strings.HasPrefix(s, "*") {
			cut = i
			break
		}
	}
	if cut == 0 {
		return "", false
	}
	end := cut
	if cut == len(segments) && end > 1 {
		end--
	}
	return "/" + strings.Join(segments[:end], "/"), true
}

// pathSegments splits a route or request path into its non-empty segments.
func pathSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}
