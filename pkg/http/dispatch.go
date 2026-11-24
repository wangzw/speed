package http

import (
	"fmt"
	nethttp "net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/i18n"
)

func buildDispatch(name string, endpoints []*endpoint, localizer i18n.Localizer) (nethttp.Handler, error) {
	var entries []prefixEntry
	var fallback *endpoint
	for _, e := range endpoints {
		for _, prefix := range e.settings.prefixes {
			if prefix == "/" {
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

func (d *dispatch) verifyRoutes(listener string, endpoints []*endpoint) error {
	var failures []string
	for _, e := range endpoints {
		var missed []string
		var leadings []string
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
			if leading != "" {
				leadings = append(leadings, leading)
			}
		}
		if len(missed) == 0 {
			continue
		}
		suggestion := "A route whose literal leading path is empty can only sit on the endpoint " +
			"that claims \"/\""
		// The example has to be a claim the endpoint can actually adopt, so a
		// prefix another endpoint on the listener already claims is not offered.
		if example := coveringClaim(leadings); example != "" && !claimedByOther(example, e, endpoints) {
			suggestion = fmt.Sprintf("Give endpoint %q a claim covering them (for example %q), "+
				"or register them on the endpoint that selects them",
				e.settings.name, example)
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

type prefixEntry struct {
	prefix   string
	endpoint *endpoint
}

type dispatch struct {
	entries  []prefixEntry
	fallback *endpoint
}

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

func coveringClaim(leadings []string) string {
	if len(leadings) == 0 {
		return ""
	}
	common := pathSegments(leadings[0])
	for _, leading := range leadings[1:] {
		other := pathSegments(leading)
		n := len(common)
		if len(other) < n {
			n = len(other)
		}
		i := 0
		for i < n && common[i] == other[i] {
			i++
		}
		common = common[:i]
		if len(common) == 0 {
			return ""
		}
	}
	return "/" + strings.Join(common, "/")
}

func claimedByOther(prefix string, self *endpoint, endpoints []*endpoint) bool {
	for _, other := range endpoints {
		if other == self {
			continue
		}
		for _, claimed := range other.settings.prefixes {
			if claimed == prefix {
				return true
			}
		}
	}
	return false
}

func claimList(prefixes []string) string {
	return "[" + quotedList(prefixes) + "]"
}

func pathSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}
