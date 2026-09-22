package gate

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"sort"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// phase is the three states the exemption registration surface moves through.
// The window in the middle opens in Migrate and shuts in Start: a stage
// advances one module at a time, so it contains the Init stage whole and
// reaches into the rest of the Migrate stage and the Start callbacks ordered
// ahead of this module.
type phase int

const (
	// phaseShut is before Migrate. A write here comes from a module
	// constructing, which is too early for the registry to have every
	// instance.
	phaseShut phase = iota
	// phaseOpen is the window Migrate opens and Start shuts: the Init stage
	// whole, plus the rest of the Migrate stage and the Start callbacks
	// ordered ahead of this module.
	phaseOpen
	// phaseSealed is Start onwards. The table is frozen and the matchers
	// are built from it, so a later write would have to rebuild them under
	// live traffic.
	phaseSealed
)

// exemption is one registration, kept as it was written. Nothing is judged at
// registration time: the registrants run in dependency order through the open
// window, and whether the set is well formed can only be asked once the window
// has shut.
type exemption struct {
	endpoint string
	pattern  string
}

// Exempt registers a route pattern that this gateway lets through without a
// credential.
//
// It returns nothing, for the reason the entry point's own registration surface
// does: the only way a registration can fail is a programming error at the call
// site, and handing back an error a registrant is free to drop would leave a
// route believed exempt and in fact guarded — or, worse, the other way round.
// What can only be judged once the window has shut is judged in Start and ends
// the startup there.
//
// A write outside the registration window panics. The window opens in this
// module's Migrate callback and shuts in its Start callback, so it contains the
// whole Init stage. It is not a configuration mistake but a call from the wrong
// stage, and the two ends of the window are stages of the drive, not anything a
// caller can retry.
func (g *gateway) Exempt(endpoint, pattern string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	switch g.phase {
	case phaseShut:
		panic(fmt.Sprintf("%s: Exempt(%q, %q) was called before the registration gate "+
			"opened. The surface accepts writes from this module's Migrate callback until "+
			"its Start callback, a window that contains the whole Init stage. Move the call "+
			"into the registering module's Init callback: every instance exists by then, "+
			"which is what makes it the stage modules wire each other up in",
			ModuleName, endpoint, pattern))
	case phaseSealed:
		panic(fmt.Sprintf("%s: Exempt(%q, %q) was called after the registration gate "+
			"closed. The surface accepts writes from this module's Migrate callback until "+
			"its Start callback, a window that contains the whole Init stage. The exemption "+
			"table is frozen in Start and the matchers are built from it, so this "+
			"registration would change what is guarded while traffic is being served. "+
			"Move the call into the registering module's Init callback",
			ModuleName, endpoint, pattern))
	}
	g.declared = append(g.declared, exemption{endpoint: endpoint, pattern: pattern})
}

// open lifts the gate. It runs in Migrate.
func (g *gateway) open() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.phase = phaseOpen
}

// seal shuts the gate, validates what was registered and builds one matcher per
// guarded endpoint. It runs in Start.
//
// The gate is shut before anything is judged, so that a registration racing the
// validation lands on the closed side rather than in a table already being
// read.
func (g *gateway) seal(router speedhttp.Router) error {
	g.mu.Lock()
	g.phase = phaseSealed
	declared := g.declared
	g.mu.Unlock()

	guarded := make(map[string]struct{}, len(g.settings.endpoints))
	for _, name := range g.settings.endpoints {
		guarded[name] = struct{}{}
	}

	byEndpoint := make(map[string][]string)
	for _, e := range declared {
		if _, err := router.Endpoint(e.endpoint); err != nil {
			if errors.Is(err, speedhttp.ErrUnknownEndpoint) {
				return fmt.Errorf("%w: the exemption %q names the endpoint %q, which the entry "+
					"point does not have. Correct the name, or declare that endpoint in "+
					"the entry point's configuration",
					authn.ErrUnknownEndpoint, e.pattern, e.endpoint)
			}
			return fmt.Errorf("%s: checking the endpoint %q named by the exemption %q: %w",
				ModuleName, e.endpoint, e.pattern, err)
		}
		if _, ok := guarded[e.endpoint]; !ok {
			return fmt.Errorf("%w: the exemption %q lands on the endpoint %q, which this gateway "+
				"does not guard (it guards %s). An exemption on an unguarded endpoint "+
				"changes nothing and would be swallowed without a signal. Write that "+
				"endpoint into %s.%s, or move the exemption",
				authn.ErrExemptionUnguarded, e.pattern, e.endpoint,
				describeGuarded(g.settings.endpoints), ConfigNamespace, endpointsItem)
		}
		byEndpoint[e.endpoint] = append(byEndpoint[e.endpoint], e.pattern)
	}

	matchers := make(map[string]*matcher, len(byEndpoint))
	for endpoint, patterns := range byEndpoint {
		m, err := newMatcher(endpoint, patterns)
		if err != nil {
			return err
		}
		matchers[endpoint] = m
	}
	g.matchers = matchers
	return nil
}

// describeGuarded renders the guarded set the way a diagnostic names it.
func describeGuarded(endpoints []string) string {
	if len(endpoints) == 0 {
		return "no endpoint at all"
	}
	return strings.Join(endpoints, ", ")
}

// exemptMark is the handler an exempt pattern is bound to. It is a named type
// rather than a function value so that a match can be recognised by a type
// assertion: two function values cannot be compared in Go, and anything the
// matcher hands back that is not this type — the not-found handler, or the
// redirect the matcher issues for a path it would clean — leaves the request
// guarded.
type exemptMark struct{}

func (exemptMark) ServeHTTP(nethttp.ResponseWriter, *nethttp.Request) {}

// matcher answers one question for one endpoint: did this request hit one of
// that endpoint's exempt patterns.
//
// It does not know which routes the endpoint actually carries — this module
// cannot read the entry point's routing table — so what it answers is which
// exemption was hit, not which route the request will be dispatched to. Where
// the two part company is written up as a non-guarantee.
type matcher struct {
	mux      *nethttp.ServeMux
	patterns []string
}

// newMatcher builds one endpoint's matcher.
//
// The engine and this matcher are two parsers. A pattern the engine accepted at
// registration is only known to be one this matcher accepts when the matcher is
// built, which is why a refusal here is a startup failure rather than a panic
// at the registration call: the two grammars need not agree, and an engine with
// a wider syntax than ServeMux makes this the ordinary case rather than the odd
// one.
//
// The same pattern registered twice on one endpoint is one exemption, not a
// conflict: two modules asking for the same path to be public are asking for
// the same thing. Passing the duplicate through to the mux would panic, and a
// panic in Start skips the registry's rollback.
func newMatcher(endpoint string, patterns []string) (m *matcher, err error) {
	mux := nethttp.NewServeMux()
	seen := make(map[string]struct{}, len(patterns))
	kept := make([]string, 0, len(patterns))

	for _, pattern := range patterns {
		if _, duplicate := seen[pattern]; duplicate {
			continue
		}
		seen[pattern] = struct{}{}
		if err := mount(mux, endpoint, pattern); err != nil {
			return nil, err
		}
		kept = append(kept, pattern)
	}
	sort.Strings(kept)
	return &matcher{mux: mux, patterns: kept}, nil
}

// mount binds one pattern, turning the standard library's panic into the
// sentinel. ServeMux reports a malformed pattern, and a pattern that cannot
// stand beside one already mounted, by panicking; neither is a defect of this
// module, and a panic escaping Start would skip the rollback.
func mount(mux *nethttp.ServeMux, endpoint, pattern string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: the exemption %q on endpoint %q is not one the matcher "+
				"accepts: %v. Write it the way the matcher reads patterns, or hang one "+
				"pattern that covers the same requests", authn.ErrUnmatchablePattern,
				pattern, endpoint, r)
		}
	}()
	mux.Handle(pattern, exemptMark{})
	return nil
}

// exempts reports whether this request hit an exemption. The question is asked
// before the Authorization header is read, so a request on an exempt route
// reaches no backend at all.
func (m *matcher) exempts(r *nethttp.Request) bool {
	if m == nil {
		return false
	}
	h, _ := m.mux.Handler(r)
	_, hit := h.(exemptMark)
	return hit
}
