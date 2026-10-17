package http

import (
	"fmt"
	nethttp "net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/i18n"
)

// buildDispatch builds the handler one listener serves every accepted
// connection from.
//
// The dispatch selects one endpoint per request and hands the request to that
// endpoint's assembled engine, which matches it, runs the endpoint's chain and
// answers 200, its own 404 or its own 405. Selection reads configuration only:
// the prefixes each endpoint claimed under http.endpoints.<name>.prefixes. The
// endpoint whose claim the path falls under answers it, the longest such prefix
// winning when the claims nest. A path under no non-root claim goes to the
// listener's fallback endpoint, the one that claimed "/", or, with none, to a
// 404.
//
// The registered routes take no part in selection. They are checked against it
// once, here: a route whose literal leading path would select another endpoint,
// or no endpoint at all, could be mounted but never reached, and that is
// reported as ErrUnclaimedRoute rather than left silent. The check allows
// partial shadowing — an endpoint claiming /api may register /api/:id beside
// another endpoint claiming /api/internal — because the literal leading path
// /api still selects the first endpoint, and /api/internal is the meaning of the
// nested claim.
func buildDispatch(name string, endpoints []*endpoint, localizer i18n.Localizer) (nethttp.Handler, error) {
	var entries []prefixEntry
	var fallback *endpoint
	for _, e := range endpoints {
		for _, prefix := range e.settings.prefixes {
			if prefix == "/" {
				// "/" means "the paths no other endpoint claims", so it is the
				// listener's fallback rather than an entry in longest matching.
				fallback = e
				continue
			}
			entries = append(entries, prefixEntry{prefix: prefix, endpoint: e})
		}
	}
	slices.SortFunc(entries, func(a, b prefixEntry) int { return len(b.prefix) - len(a.prefix) })
	d := &dispatch{entries: entries, fallback: fallback}
	if err := d.verifyRoutes(name, endpoints); err != nil {
		return nil, err
	}
	return d.handler(localizer), nil
}

// verifyRoutes refuses every route whose literal leading path selects an
// endpoint other than the one that registered it.
//
// The literal leading path is the part before the first segment that begins
// with ":" or "*": a fully literal route contributes its whole path, while a
// root route and one whose first segment is dynamic contribute "". An empty
// leading path falls under no non-root claim, so it selects the fallback. The
// route is registered where it is answered only when that selection is the
// registering endpoint.
//
// Every failing route of one endpoint is reported together, with the endpoint
// and its claims, the endpoint and prefix that would answer it, and a suggested
// claim. Naming the routes one at a time would send the reader back to the
// startup once per route; naming none of the answerers would leave out the
// endpoint whose claim has to change. The text names no registrant: the
// registration surface does not know who called it.
func (d *dispatch) verifyRoutes(listener string, endpoints []*endpoint) error {
	var failures []string
	for _, e := range endpoints {
		var missed []string
		var suggestions []string
		for _, r := range e.routes {
			leading := literalLeadingPath(r.path)
			selected, prefix := d.owner(leading)
			if selected == nil {
				selected = d.fallback
				if selected != nil {
					prefix = "/"
				}
			}
			if selected == e {
				continue
			}
			answer := "no endpoint selects it: no endpoint on this listener claims \"/\""
			if selected != nil {
				answer = fmt.Sprintf("it is selected by endpoint %q, which claims %q",
					selected.settings.name, prefix)
			}
			missed = append(missed, fmt.Sprintf("%q (literal leading path %q, and %s)",
				r.String(), leading, answer))
			if top := topLevelSegment(leading); top != "" {
				suggestions = append(suggestions, top)
			}
		}
		if len(missed) == 0 {
			continue
		}
		suggestion := "A route whose literal leading path is empty can only sit on the endpoint " +
			"that claims \"/\""
		if len(suggestions) > 0 {
			slices.Sort(suggestions)
			suggestions = slices.Compact(suggestions)
			suggestion = fmt.Sprintf("Give endpoint %q a claim covering them (for example %s), "+
				"or register them on the endpoint that selects them",
				e.settings.name, quotedList(suggestions))
		}
		failures = append(failures, fmt.Sprintf(
			"endpoint %q claims %s but registers %d route(s) those claims never select: %s. %s",
			e.settings.name, claimList(e.settings.prefixes), len(missed),
			strings.Join(missed, "; "), suggestion))
	}
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("%w: listener %q: %s", ErrUnclaimedRoute, listener,
		strings.Join(failures, ". "))
}

// prefixEntry binds one claimed prefix to the endpoint that claimed it.
type prefixEntry struct {
	prefix   string
	endpoint *endpoint
}

// dispatch is a listener's request selector: the endpoints' non-root claims,
// longest first, and the fallback for a path none of them covers.
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
		e, _ := d.owner(c.Request.URL.Path)
		if e == nil {
			e = d.fallback
		}
		if e != nil {
			e.handler.ServeHTTP(c.Writer, c.Request)
			return
		}
		Fail(c, Problem{
			Status: nethttp.StatusNotFound,
			Title:  refusalTitle(c.Request.Context(), localizer, nethttp.StatusNotFound),
		})
	})
	return engine
}

// owner returns the endpoint whose claimed prefix the path falls under, together
// with that prefix, the longest one winning, or nil and "" when no claim covers
// the path. entries is ordered longest-first, so the first prefix that covers
// the path is the longest. The fallback — the endpoint claiming "/" — is not one
// of the entries and is therefore never returned here; selection falls back to
// it only when owner finds nothing.
func (d *dispatch) owner(path string) (*endpoint, string) {
	for _, e := range d.entries {
		if fallsUnder(path, e.prefix) {
			return e.endpoint, e.prefix
		}
	}
	return nil, ""
}

// fallsUnder reports whether a request path lies at or below a claimed prefix:
// equal to it, or continuing past it at a segment boundary. A bare string-prefix
// match is not enough — "/apifoo" does not fall under "/api" — which is what
// keeps an endpoint's claim to its own segment subtree. No entry is "/": the
// endpoint that claims the root is its listener's fallback and contributes no
// entry (see buildDispatch), so the root needs no case here.
func fallsUnder(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// literalLeadingPath returns the literal part a route leads with: everything
// before its first segment that begins with ":" or "*". A fully literal route
// contributes its whole path; a route at the root and one whose first segment is
// already dynamic contribute "". It is this prefix, not the registered path, that
// the dispatch's selection has to answer the same way as the registering
// endpoint.
func literalLeadingPath(path string) string {
	segments := pathSegments(path)
	if len(segments) == 0 {
		return ""
	}
	cut := len(segments)
	for i, segment := range segments {
		if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
			cut = i
			break
		}
	}
	if cut == 0 {
		return ""
	}
	return "/" + strings.Join(segments[:cut], "/")
}

// topLevelSegment returns the top-level segment of a literal path, or "" when
// the path has none. It is the coarsest claim that would cover the path, which
// is what a startup failure over an unclaimed route suggests: claims are written
// per top-level namespace unless a subtree is deliberately given to another
// endpoint.
func topLevelSegment(path string) string {
	segments := pathSegments(path)
	if len(segments) == 0 {
		return ""
	}
	return "/" + segments[0]
}

// claimList renders an endpoint's claims for a message, for example ["/api",
// "/other"].
func claimList(prefixes []string) string {
	return "[" + quotedList(prefixes) + "]"
}

// pathSegments splits a route or request path into its non-empty segments.
func pathSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}
