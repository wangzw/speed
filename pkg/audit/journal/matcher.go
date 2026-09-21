package journal

import (
	"fmt"
	nethttp "net/http"
)

// mark is the handler a configured pattern is bound to.
//
// It is a named type rather than a function value so that a match can be
// recognised by a type assertion. Anything else the matcher hands back for a
// request — the not-found handler, or the redirect it issues for a path it
// would clean — leaves the request unrecorded.
type mark struct{ pattern string }

func (mark) ServeHTTP(nethttp.ResponseWriter, *nethttp.Request) {}

// matcher answers one question about a request: did it hit one of the
// configured patterns, and which one.
//
// It is built on the standard library's own router rather than on a grammar of
// this module's own, because the configured patterns have to mean the same
// thing here as they mean to the entry point's engine. Where the two can still
// part company is written up as a non-guarantee: this answers which configured
// pattern a request hit, and the engine answers which route it is dispatched
// to, and nothing here can read the routing table to reconcile them.
//
// The capture layer sits outside the engine, so the request it sees carries no
// record of the pattern that will be dispatched. That is why the matching
// happens here at all.
type matcher struct {
	mux *nethttp.ServeMux
}

// newMatcher builds a matcher over the configured patterns.
//
// A pattern repeated in the configuration is one pattern, not a conflict: the
// same request named twice is the same request. Passing the duplicate through
// would panic, and a panic in the Init stage skips the registry's rollback.
func newMatcher(patterns []string) (*matcher, error) {
	mux := nethttp.NewServeMux()
	seen := make(map[string]struct{}, len(patterns))
	for _, pattern := range patterns {
		if _, duplicate := seen[pattern]; duplicate {
			continue
		}
		seen[pattern] = struct{}{}
		if err := mount(mux, pattern); err != nil {
			return nil, err
		}
	}
	return &matcher{mux: mux}, nil
}

// mount binds one pattern, turning the standard library's panic into the
// sentinel. ServeMux reports a malformed pattern, and a pattern that cannot
// stand beside one already mounted, by panicking; neither is a defect of this
// module, and a panic escaping the Init stage would skip the rollback.
func mount(mux *nethttp.ServeMux, pattern string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %q: %v", ErrUnmatchablePattern, pattern, r)
		}
	}()
	mux.Handle(pattern, mark{pattern: pattern})
	return nil
}

// match reports which configured pattern the request hit.
//
// The question is asked of a copy the matcher routes itself; no handler on the
// chain runs, and the request is untouched.
func (m *matcher) match(r *nethttp.Request) (string, bool) {
	if m == nil {
		return "", false
	}
	h, _ := m.mux.Handler(r)
	hit, ok := h.(mark)
	if !ok {
		return "", false
	}
	return hit.pattern, true
}
