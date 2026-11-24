package gate

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// registrations are the three calls a registrant makes, each with the name the
// panic of a refused one is written with.
var registrations = []struct {
	name string
	call func(g *gateway, endpoint, method, path string)
}{
	{"Exempt", func(g *gateway, e, m, p string) { g.Exempt(e, m, p) }},
	{"Optional", func(g *gateway, e, m, p string) { g.Optional(e, m, p) }},
	{"Impersonable", func(g *gateway, e, m, p string) { g.Impersonable(e, m, p) }},
}

// TestRegisteringOutsideTheWindowPanics holds both ends of the three-state
// gate for every one of the three registrations.
func TestRegisteringOutsideTheWindowPanics(t *testing.T) {
	for _, reg := range registrations {
		t.Run(reg.name+" before the window opens", func(t *testing.T) {
			g, err := newGateway(settings{endpoints: []string{"public"}, inFlight: 1})
			if err != nil {
				t.Fatal(err)
			}
			text := panicked(t, "registering before Migrate", func() { reg.call(g, "public", "GET", "/health") })
			mustContain(t, text, reg.name+`("public", "GET", "/health")`, "the method that was called and what it was given")
			mustContain(t, text, "before the registration gate opened", "which side of the window the call fell on")
			mustContain(t, text, "this module's Migrate callback", "where the window that accepts writes opens")
			mustContain(t, text, "whole Init stage", "what the window frames")
			mustContain(t, text, "Move the call into the registering module's Init callback", "the fixing action")
		})

		t.Run(reg.name+" after the window shuts", func(t *testing.T) {
			g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
			sealed(t, g, routerWith("public"))
			text := panicked(t, "registering after Start", func() { reg.call(g, "public", "GET", "/health") })
			mustContain(t, text, reg.name+`("public", "GET", "/health")`, "the method that was called and what it was given")
			mustContain(t, text, "after the registration gate closed", "which side of the window the call fell on")
			mustContain(t, text, "so this registration would change what is guarded", "why a late registration reaches nothing")
			mustContain(t, text, "Move the call into the registering module's Init callback", "the fixing action")
		})

		t.Run(reg.name+" inside the window", func(t *testing.T) {
			g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
			reg.call(g, "public", "GET", "/health") // does not panic
		})
	}
}

// TestARegistrationThatCannotLandFailsTheStartup holds the two validations
// Start makes of the endpoint every registration names, each with its own
// sentinel and for each of the three kinds.
func TestARegistrationThatCannotLandFailsTheStartup(t *testing.T) {
	for _, reg := range registrations {
		for _, c := range []struct {
			name      string
			guarded   []string
			endpoints []string
			endpoint  string
			want      error
		}{
			{"the endpoint does not exist", []string{"public"}, []string{"public"}, "typo", authn.ErrUnknownEndpoint},
			{"the endpoint exists but this gateway does not guard it", []string{"public"}, []string{"public", "admin"}, "admin", authn.ErrExemptionUnguarded},
		} {
			t.Run(reg.name+" "+c.name, func(t *testing.T) {
				g := testGateway(t, c.guarded, basicBackend("static", "static."))
				reg.call(g, c.endpoint, "GET", "/health")

				err := g.seal(routerWith(c.endpoints...))
				if !errors.Is(err, c.want) {
					t.Fatalf("sealing gave %v, want one wrapping %v", err, c.want)
				}
				for _, named := range []string{c.endpoint, "GET", "/health", reg.name} {
					if !strings.Contains(err.Error(), named) {
						t.Errorf("the failure reads %q and does not name %q", err, named)
					}
				}
			})
		}
	}
}

// TestOneRouteWithTwoLevelsThatExcludeEachOtherFailsTheStartup holds which
// pairs of registrations conflict.
func TestOneRouteWithTwoLevelsThatExcludeEachOtherFailsTheStartup(t *testing.T) {
	type call struct {
		kind     string
		endpoint string
		method   string
		path     string
	}
	for _, c := range []struct {
		name     string
		calls    []call
		conflict bool
	}{
		{"exempt and optional", []call{{"Exempt", "public", "GET", "/x"}, {"Optional", "public", "GET", "/x"}}, true},
		{"optional and exempt", []call{{"Optional", "public", "GET", "/x"}, {"Exempt", "public", "GET", "/x"}}, true},
		{"exempt and impersonable", []call{{"Exempt", "public", "GET", "/x"}, {"Impersonable", "public", "GET", "/x"}}, true},
		{"impersonable and exempt", []call{{"Impersonable", "public", "GET", "/x"}, {"Exempt", "public", "GET", "/x"}}, true},
		{"all three", []call{{"Exempt", "public", "GET", "/x"}, {"Optional", "public", "GET", "/x"}, {"Impersonable", "public", "GET", "/x"}}, true},
		{"optional and impersonable", []call{{"Optional", "public", "GET", "/x"}, {"Impersonable", "public", "GET", "/x"}}, false},
		{"the same registration twice", []call{{"Exempt", "public", "GET", "/x"}, {"Exempt", "public", "GET", "/x"}}, false},
		{"exempt and optional under two methods", []call{{"Exempt", "public", "GET", "/x"}, {"Optional", "public", "POST", "/x"}}, false},
		{"exempt and optional under two paths", []call{{"Exempt", "public", "GET", "/x"}, {"Optional", "public", "GET", "/y"}}, false},
		{"exempt and optional on two endpoints", []call{{"Exempt", "public", "GET", "/x"}, {"Optional", "admin", "GET", "/x"}}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := testGateway(t, []string{"admin", "public"}, basicBackend("static", "static."))
			for _, k := range c.calls {
				for _, reg := range registrations {
					if reg.name == k.kind {
						reg.call(g, k.endpoint, k.method, k.path)
					}
				}
			}

			err := g.seal(routerWith("admin", "public"))
			if !c.conflict {
				if err != nil {
					t.Fatalf("sealing refused a set of registrations that do not conflict: %v", err)
				}
				return
			}
			if !errors.Is(err, authn.ErrAccessConflict) {
				t.Fatalf("sealing gave %v, want one wrapping %v", err, authn.ErrAccessConflict)
			}
			for _, named := range []string{"GET /x", `"public"`, "Exempt"} {
				if !strings.Contains(err.Error(), named) {
					t.Errorf("the failure reads %q and does not name %q", err, named)
				}
			}
		})
	}
}

// TestTheFrozenTableCarriesEachKindOnTheRoutesItWasRegisteredOn holds the
// freeze: the tables say, route by route, which of the three levels each route
// has, and a route registered with none has the zero value.
func TestTheFrozenTableCarriesEachKindOnTheRoutesItWasRegisteredOn(t *testing.T) {
	g := testGateway(t, []string{"admin", "public"}, basicBackend("static", "static."))
	g.Exempt("public", "GET", "/status")
	g.Optional("public", "GET", "/catalog")
	g.Impersonable("public", "GET", "/catalog")
	g.Impersonable("admin", "POST", "/end")
	sealed(t, g, routerWith("admin", "public"))

	for _, c := range []struct {
		endpoint string
		route    route
		want     access
	}{
		{"public", route{"GET", "/status"}, access{exempt: true}},
		{"public", route{"GET", "/catalog"}, access{optional: true, impersonable: true}},
		{"admin", route{"POST", "/end"}, access{impersonable: true}},
		{"public", route{"POST", "/status"}, access{}},
		{"admin", route{"GET", "/status"}, access{}},
		{"public", route{"GET", "/orders"}, access{}},
	} {
		if got := g.routes[c.endpoint][c.route]; got != c.want {
			t.Errorf("%s %s: the table holds %+v, want %+v", c.endpoint, c.route, got, c.want)
		}
	}
}

// TestTheFrozenTablesAreWrittenByEndpointMethodAndPath holds the startup
// diagnostics that state what is public, what is optional and what admits an
// impersonation.
func TestTheFrozenTablesAreWrittenByEndpointMethodAndPath(t *testing.T) {
	logs := liveAssembly{
		endpoints: []string{"public", "admin"},
		backends:  []authn.Verifier{passes(basicBackend("static", "static."))},
		routes: []liveRoute{
			{endpoint: "public", method: "GET", path: "/health"},
			{endpoint: "public", method: "POST", path: "/login"},
			{endpoint: "public", method: "GET", path: "/catalog"},
			{endpoint: "admin", method: "GET", path: "/status"},
			{endpoint: "admin", method: "POST", path: "/end"},
		},
		exempt: []liveRoute{
			{endpoint: "public", method: "POST", path: "/login"},
			{endpoint: "public", method: "GET", path: "/health"},
			{endpoint: "public", method: "GET", path: "/health"},
			{endpoint: "admin", method: "GET", path: "/status"},
		},
		optional:     []liveRoute{{endpoint: "public", method: "GET", path: "/catalog"}},
		impersonable: []liveRoute{{endpoint: "admin", method: "POST", path: "/end"}},
	}.serve(t, func(*live) {})

	frozen, ok := logs.recordNamed("authn: the exempt routes, frozen")
	if !ok {
		t.Fatal("the startup diagnostics say nothing about the exempt routes")
	}
	if got, want := attrText(frozen, "public"), "GET /health, POST /login"; got != want {
		t.Errorf("the exempt table lists %q for the endpoint public, want %q: each route once, as "+
			"method and path, in a stable order", got, want)
	}
	if got, want := attrText(frozen, "admin"), "GET /status"; got != want {
		t.Errorf("the exempt table lists %q for the endpoint admin, want %q", got, want)
	}

	optional, ok := logs.recordNamed("authn: the routes that accept a request with no credential, frozen")
	if !ok {
		t.Fatal("the startup diagnostics say nothing about the optional routes")
	}
	if got, want := attrText(optional, "public"), "GET /catalog"; got != want {
		t.Errorf("the optional table lists %q for public, want %q", got, want)
	}
	if got := attrText(optional, "admin"); got != "" {
		t.Errorf("the optional table lists %q for admin, want nothing", got)
	}

	imp, ok := logs.recordNamed("authn: the routes that admit an impersonating identity, frozen")
	if !ok {
		t.Fatal("the startup diagnostics say nothing about the impersonable routes")
	}
	if got, want := attrText(imp, "admin"), "POST /end"; got != want {
		t.Errorf("the impersonable table lists %q for admin, want %q", got, want)
	}
}

// TestAnEmptyFrozenTableSaysSo holds the other half of that diagnostic: with
// nothing registered it still speaks, and the exempt table says that every route
// needs a credential, which is the sentence that makes an absent registration
// readable.
func TestAnEmptyFrozenTableSaysSo(t *testing.T) {
	attrs := registrationAttrs(nil, func(a access) bool { return a.exempt }, "exemptions",
		"none: every route on every guarded endpoint requires a credential")
	if len(attrs) != 1 {
		t.Fatalf("an empty table is written as %d attribute(s), want the one that says so", len(attrs))
	}
	if rendered := renderAttrs(attrs); !strings.Contains(rendered, "none") ||
		!strings.Contains(rendered, "requires a credential") {
		t.Errorf("an empty table is written as %q, which does not say that nothing is exempt", rendered)
	}
}

func renderAttrs(attrs []any) string {
	var b strings.Builder
	for _, a := range attrs {
		fmt.Fprint(&b, a)
		b.WriteByte(' ')
	}
	return b.String()
}

// TestAnExemptRouteReachesNoBackendAtAll holds the ordering the whole
// exemption mechanism rests on.
func TestAnExemptRouteReachesNoBackendAtAll(t *testing.T) {
	var asked atomic.Int64
	backend := passes(basicBackend("static", "static."))
	backend.asked = &asked

	var exempt, guarded reply
	var askedByExempt int64
	liveAssembly{
		backends: []authn.Verifier{backend},
		routes: []liveRoute{
			{endpoint: "public", method: "GET", path: "/health"},
			{endpoint: "public", method: "GET", path: "/orders"},
		},
		exempt: []liveRoute{{endpoint: "public", method: "GET", path: "/health"}},
	}.serve(t, func(l *live) {
		// The request carries a credential, so that what is held is the ordering
		// and not merely the absence of a header.
		exempt = l.send("public", "GET", "/health", asAlice)
		askedByExempt = asked.Load()
		guarded = l.send("public", "GET", "/orders", asAlice)
	})

	requireStatus(t, "an exempt route", exempt, nethttp.StatusOK)
	if askedByExempt != 0 {
		t.Errorf("a backend was asked %d time(s) on an exempt route, want none: the exemption is "+
			"judged before the credential is read", askedByExempt)
	}
	if want := "reached as nobody [open]"; exempt.body != want {
		t.Errorf("the exempt route answered %q, want %q: nothing authenticated the request, so it "+
			"carries no identity, and the access level says why", exempt.body, want)
	}

	requireStatus(t, "a guarded route with a good credential", guarded, nethttp.StatusOK)
	if want := "reached as static.alice [required]"; guarded.body != want {
		t.Errorf("the guarded route answered %q, want %q", guarded.body, want)
	}
	if got := asked.Load(); got != 1 {
		t.Errorf("a backend was asked %d time(s) in all, want the one from the guarded route", got)
	}
}

// TestAnOptionalRouteTakesARequestWithNoCredentialAndJudgesOneWithACredential
// holds the optional registration against the real entry point: no credential
// passes as nobody and the access level says optional, a good credential is the
// account, a refused one is a 401 and not a pass as nobody, and a route beside
// it that is not optional is guarded.
func TestAnOptionalRouteTakesARequestWithNoCredentialAndJudgesOneWithACredential(t *testing.T) {
	tokens := schemeBackend("token", "bearer")
	tokens.verify = func(_ context.Context, c authn.Credential) authn.Verdict {
		if c.Value == "good" {
			return held(tokens, "carol")
		}
		return authn.Denied(errors.New("expired"))
	}
	var bare, good, refused, other reply
	liveAssembly{
		backends: []authn.Verifier{tokens},
		routes: []liveRoute{
			{endpoint: "public", method: "GET", path: "/catalog"},
			{endpoint: "public", method: "GET", path: "/orders"},
		},
		optional: []liveRoute{{endpoint: "public", method: "GET", path: "/catalog"}},
	}.serve(t, func(l *live) {
		bare = l.send("public", "GET", "/catalog")
		good = l.send("public", "GET", "/catalog", asBearer("good"))
		refused = l.send("public", "GET", "/catalog", asBearer("expired"))
		other = l.send("public", "GET", "/orders")
	})

	requireStatus(t, "an optional route with no credential", bare, nethttp.StatusOK)
	if want := "reached as nobody [optional]"; bare.body != want {
		t.Errorf("the optional route with no credential answered %q, want %q", bare.body, want)
	}
	requireStatus(t, "an optional route with a good credential", good, nethttp.StatusOK)
	if want := "reached as carol [optional]"; good.body != want {
		t.Errorf("the optional route with a good credential answered %q, want %q: a credential that is "+
			"there is judged", good.body, want)
	}
	requireStatus(t, "an optional route with a credential that does not hold", refused, nethttp.StatusUnauthorized)
	requireChallenge(t, "an optional route with a credential that does not hold", refused)
	requireStatus(t, "a route beside the optional one", other, nethttp.StatusUnauthorized)
}

// TestAnExemptionMatchesTheRegisteredMethodAndPathOnly holds that the method
// is part of what is exempted.
func TestAnExemptionMatchesTheRegisteredMethodAndPathOnly(t *testing.T) {
	var get, post, other, optionalGet, optionalPost reply
	liveAssembly{
		backends: []authn.Verifier{passes(basicBackend("static", "static."))},
		routes: []liveRoute{
			{endpoint: "public", method: "GET", path: "/both"},
			{endpoint: "public", method: "POST", path: "/both"},
			{endpoint: "public", method: "GET", path: "/other"},
			{endpoint: "public", method: "GET", path: "/either"},
			{endpoint: "public", method: "POST", path: "/either"},
		},
		exempt:   []liveRoute{{endpoint: "public", method: "GET", path: "/both"}},
		optional: []liveRoute{{endpoint: "public", method: "GET", path: "/either"}},
	}.serve(t, func(l *live) {
		get = l.send("public", "GET", "/both")
		post = l.send("public", "POST", "/both")
		other = l.send("public", "GET", "/other")
		optionalGet = l.send("public", "GET", "/either")
		optionalPost = l.send("public", "POST", "/either")
	})

	requireStatus(t, "GET /both, the exempted route", get, nethttp.StatusOK)
	requireStatus(t, "POST /both, on the same path with another method", post, nethttp.StatusUnauthorized)
	requireChallenge(t, "POST /both", post)
	requireStatus(t, "GET /other, a route nobody exempted", other, nethttp.StatusUnauthorized)
	requireStatus(t, "GET /either, the optional route", optionalGet, nethttp.StatusOK)
	requireStatus(t, "POST /either, on the same path as the optional one", optionalPost, nethttp.StatusUnauthorized)
	// The three refused above are refused by the gateway before the router
	// answers, so they are one answer: same status, same headers, same body.
	for _, c := range []struct {
		name string
		got  reply
	}{
		{"GET /other", other},
		{"POST /either", optionalPost},
	} {
		requireSameReply(t, c.name, "POST /both", post, c.name, c.got, true)
	}
}

// TestAnExemptionWrittenAsTheRouteWasRegisteredCoversThatRouteOnly holds the
// comparison for a route that carries a parameter or a catch-all.
func TestAnExemptionWrittenAsTheRouteWasRegisteredCoversThatRouteOnly(t *testing.T) {
	var parameter, sibling, catchAll, nested, elsewhere reply
	liveAssembly{
		backends: []authn.Verifier{passes(basicBackend("static", "static."))},
		routes: []liveRoute{
			{endpoint: "public", method: "GET", path: "/accounts/:id"},
			{endpoint: "public", method: "GET", path: "/accounts/me"},
			{endpoint: "public", method: "GET", path: "/public/*path"},
			{endpoint: "public", method: "GET", path: "/private/*path"},
		},
		exempt: []liveRoute{
			{endpoint: "public", method: "GET", path: "/accounts/:id"},
			{endpoint: "public", method: "GET", path: "/public/*path"},
		},
	}.serve(t, func(l *live) {
		parameter = l.send("public", "GET", "/accounts/17")
		sibling = l.send("public", "GET", "/accounts/me")
		catchAll = l.send("public", "GET", "/public/logo.png")
		nested = l.send("public", "GET", "/public/css/site.css")
		elsewhere = l.send("public", "GET", "/private/logo.png")
	})

	requireStatus(t, "GET /accounts/17, matched to the exempted /accounts/:id", parameter, nethttp.StatusOK)
	requireStatus(t, "GET /accounts/me, matched to the route beside it that nobody exempted",
		sibling, nethttp.StatusUnauthorized)
	requireStatus(t, "GET /public/logo.png, matched to the exempted /public/*path", catchAll, nethttp.StatusOK)
	requireStatus(t, "GET /public/css/site.css, matched to the same catch-all", nested, nethttp.StatusOK)
	requireStatus(t, "GET /private/logo.png, matched to a catch-all nobody exempted",
		elsewhere, nethttp.StatusUnauthorized)
	// Both refusals come from the gateway before the matched routes' handlers
	// run, so they are one answer despite matching different routes.
	requireSameReply(t, "a refusal on a route beside the exempted one and on a catch-all nobody exempted",
		"GET /accounts/me", sibling, "GET /private/logo.png", elsewhere, true)
}

// TestAnExemptionSpelledDifferentlyFromEveryRouteNeverMatches holds the cost
// the comparison accepts: a registration that no route carries, in those
// exact characters, is not an error and is not a match.
func TestAnExemptionSpelledDifferentlyFromEveryRouteNeverMatches(t *testing.T) {
	routes := []liveRoute{
		{endpoint: "public", method: "GET", path: "/accounts/:id"},
		{endpoint: "public", method: "GET", path: "/public/*path"},
		{endpoint: "public", method: "GET", path: "/health"},
		{endpoint: "public", method: "GET", path: "/orders/:id"},
	}
	spelled := []liveRoute{
		{endpoint: "public", method: "GET", path: "/accounts/:ident"},
		{endpoint: "public", method: "GET", path: "/accounts/17"},
		{endpoint: "public", method: "GET", path: "/public/*rest"},
		{endpoint: "public", method: "GET", path: "/public/"},
		{endpoint: "public", method: "GET", path: "/health/"},
		{endpoint: "public", method: "get", path: "/health"},
		{endpoint: "public", method: "GET", path: "/Health"},
		{endpoint: "public", method: "GET", path: "/ACCOUNTS/:id"},
		{endpoint: "public", method: "GET", path: "health"},
		{endpoint: "public", method: "GET", path: "/orders/{id}"},
		{endpoint: "public", method: "GET", path: "/{"},
		{endpoint: "public", method: "GET", path: ""},
		{endpoint: "public", method: "", path: ""},
	}

	requests := []struct{ method, path string }{
		{"GET", "/accounts/17"},
		{"GET", "/public/logo.png"},
		{"GET", "/health"},
		{"GET", "/orders/9"},
		{"GET", "/nowhere"},
	}
	// Each registration is tried on its own, in a drive of its own: an exempt
	// route may not also be optional, so the two kinds cannot share a spelling.
	for kind, assemble := range map[string]func(liveAssembly) liveAssembly{
		"an exemption":      func(a liveAssembly) liveAssembly { a.exempt = spelled; return a },
		"an optional route": func(a liveAssembly) liveAssembly { a.optional = spelled; return a },
	} {
		got := make([]reply, len(requests))
		assemble(liveAssembly{
			backends: []authn.Verifier{passes(basicBackend("static", "static."))},
			routes:   routes,
		}).serve(t, func(l *live) {
			for i, r := range requests {
				got[i] = l.send("public", r.method, r.path)
			}
		})

		for i, r := range requests {
			requireStatus(t, fmt.Sprintf("%s %s, which no %s is spelled as", r.method, r.path, kind),
				got[i], nethttp.StatusUnauthorized)
			requireSameReply(t, fmt.Sprintf("%s %s, which no %s is spelled as", r.method, r.path, kind),
				requests[0].method+" "+requests[0].path, got[0], r.method+" "+r.path, got[i], true)
		}
	}
}

// TestARequestTheRouterAnswersWithoutARouteIsStillChallenged holds the
// position of the gateway relative to the router's own answers: a request
// that matches no route, one the matched path does not allow the method for,
// and a HEAD to a route registered for GET alone, are all answered by the
// gateway first when they carry no credential, and by the router once they
// do.
func TestARequestTheRouterAnswersWithoutARouteIsStillChallenged(t *testing.T) {
	requests := []struct {
		name   string
		method string
		path   string
		// answered is what the router answers once the caller has authenticated.
		answered int
	}{
		{"a path no route carries", "GET", "/nowhere", nethttp.StatusNotFound},
		{"a method the matched path does not allow", "POST", "/health", nethttp.StatusMethodNotAllowed},
		{"HEAD to a route registered for GET", "HEAD", "/health", nethttp.StatusMethodNotAllowed},
	}
	unrouted := []liveRoute{
		{endpoint: "public", method: "GET", path: "/health"},
		{endpoint: "public", method: "GET", path: "/nowhere"},
		{endpoint: "public", method: "POST", path: "/health"},
		{endpoint: "public", method: "HEAD", path: "/health"},
	}

	bare := make([]reply, len(requests))
	credentialed := make([]reply, len(requests))
	for _, assemble := range []func(liveAssembly) liveAssembly{
		func(a liveAssembly) liveAssembly { a.exempt = unrouted; return a },
		func(a liveAssembly) liveAssembly { a.optional = unrouted; return a },
	} {
		assemble(liveAssembly{
			backends: []authn.Verifier{passes(basicBackend("static", "static."))},
			routes:   []liveRoute{{endpoint: "public", method: "GET", path: "/health"}},
		}).serve(t, func(l *live) {
			for i, r := range requests {
				bare[i] = l.send("public", r.method, r.path)
				credentialed[i] = l.send("public", r.method, r.path, asAlice)
			}
		})
		for i, r := range requests {
			requireStatus(t, r.name+" without a credential, whichever registration is written for it",
				bare[i], nethttp.StatusUnauthorized)
		}
	}

	for i, r := range requests {
		requireStatus(t, r.name+" without a credential", bare[i], nethttp.StatusUnauthorized)
		requireChallenge(t, r.name+" without a credential", bare[i])
		// The gateway refuses all three before the router answers, so the answers
		// must not differ in anything but Date: same status, same headers (Allow
		// included) and, where the method has a body, the same body.
		requireSameReply(t, r.name+" without a credential", requests[0].name, bare[0],
			r.name, bare[i], r.method != nethttp.MethodHead)
		if got := bare[i].header.Get("Allow"); got != "" {
			t.Errorf("%s without a credential carries Allow %q, which tells a caller with no "+
				"credential which methods the path accepts", r.name, got)
		}
		requireStatus(t, r.name+" with a credential", credentialed[i], r.answered)
	}
	for _, i := range []int{1, 2} {
		if got := credentialed[i].header.Get("Allow"); got != "GET" {
			t.Errorf("%s with a credential carries Allow %q, want GET: the router's own answer "+
				"reaches a caller that authenticated", requests[i].name, got)
		}
	}
}

// TestAnEndpointWithNoRegistrationExemptsNothing holds what an endpoint
// carrying no registration is left with.
func TestAnEndpointWithNoRegistrationExemptsNothing(t *testing.T) {
	var health, nowhere reply
	liveAssembly{
		backends: []authn.Verifier{passes(basicBackend("static", "static."))},
		routes:   []liveRoute{{endpoint: "public", method: "GET", path: "/health"}},
	}.serve(t, func(l *live) {
		health = l.send("public", "GET", "/health")
		nowhere = l.send("public", "GET", "/nowhere")
	})

	requireStatus(t, "a route on an endpoint with no registration", health, nethttp.StatusUnauthorized)
	requireStatus(t, "a path no route carries, on an endpoint with no registration", nowhere,
		nethttp.StatusUnauthorized)
	requireSameReply(t, "a path no route carries, on an endpoint with no registration",
		"the registered route", health, "the path no route carries", nowhere, true)
}

// TestARegistrationOnlyAppliesToTheEndpointItWasRegisteredOn holds the keying
// of the tables.
func TestARegistrationOnlyAppliesToTheEndpointItWasRegisteredOn(t *testing.T) {
	var opened, guarded, optionalHere, optionalThere reply
	liveAssembly{
		endpoints: []string{"public", "admin"},
		backends:  []authn.Verifier{passes(basicBackend("static", "static."))},
		routes: []liveRoute{
			{endpoint: "public", method: "GET", path: "/health"},
			{endpoint: "admin", method: "GET", path: "/health"},
			{endpoint: "public", method: "GET", path: "/catalog"},
			{endpoint: "admin", method: "GET", path: "/catalog"},
		},
		exempt:   []liveRoute{{endpoint: "public", method: "GET", path: "/health"}},
		optional: []liveRoute{{endpoint: "public", method: "GET", path: "/catalog"}},
	}.serve(t, func(l *live) {
		opened = l.send("public", "GET", "/health")
		guarded = l.send("admin", "GET", "/health")
		optionalHere = l.send("public", "GET", "/catalog")
		optionalThere = l.send("admin", "GET", "/catalog")
	})

	requireStatus(t, "the endpoint the exemption was registered on", opened, nethttp.StatusOK)
	requireStatus(t, "an endpoint nobody registered the exemption on, for the same route", guarded,
		nethttp.StatusUnauthorized)
	requireStatus(t, "the endpoint the optional route was registered on", optionalHere, nethttp.StatusOK)
	requireStatus(t, "an endpoint nobody registered the optional route on, for the same route", optionalThere,
		nethttp.StatusUnauthorized)
}

// assertPanics runs fn and fails unless it panicked.
func assertPanics(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s returned without panicking", what)
		}
	}()
	fn()
}

// panicked is assertPanics with the message kept. The panic a refused
// registration raises is a rendered message, and it is the whole of what the
// caller sees: only the text can hold which side of the window the call fell on
// and where the call belongs.
func panicked(t *testing.T, what string, fn func()) (text string) {
	t.Helper()
	defer func() {
		raised := recover()
		if raised == nil {
			t.Errorf("%s returned without panicking", what)
			return
		}
		text = fmt.Sprint(raised)
	}()
	fn()
	return ""
}

func mustContain(t *testing.T, text, fragment, why string) {
	t.Helper()
	if !strings.Contains(text, fragment) {
		t.Errorf("the message does not carry %q, which is %s.\nThe message was: %s",
			fragment, why, text)
	}
}
