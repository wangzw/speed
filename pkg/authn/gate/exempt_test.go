package gate

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestRegisteringOutsideTheWindowPanics holds both ends of the three-state
// gate.
//
// Before the window a registrant is still constructing, and the instance it
// would register with may not exist. After it the table is frozen and requests
// read it, so a late registration would change what is guarded while requests
// are already being served. Neither is something a caller can retry, which is
// why it is a panic rather than a value handed back.
//
// The panic text is part of what is held here: it is the only thing the caller
// sees, and it has to say which side of the window the call fell on and where
// the call belongs.
func TestRegisteringOutsideTheWindowPanics(t *testing.T) {
	t.Run("before the window opens", func(t *testing.T) {
		g := newGateway(settings{endpoints: []string{"public"}, inFlight: 1})
		text := panicked(t, "registering before Migrate", func() {
			g.Exempt("public", "GET", "/health")
		})
		mustContain(t, text, "before the registration gate opened", "which side of the window the call fell on")
		mustContain(t, text, "this module's Migrate callback", "where the window that accepts writes opens")
		mustContain(t, text, "whole Init stage", "what the window frames")
		mustContain(t, text, "Move the call into the registering module's Init callback", "the fixing action")
		mustContain(t, text, `"GET", "/health"`, "the method and path that were refused")
	})

	t.Run("after the window shuts", func(t *testing.T) {
		g := testGateway(t, []string{"public"}, enumerating("local", "alice"))
		sealed(t, g, routerWith("public"))
		text := panicked(t, "registering after Start", func() {
			g.Exempt("public", "GET", "/health")
		})
		mustContain(t, text, "after the registration gate closed", "which side of the window the call fell on")
		mustContain(t, text, "so this registration would change what is guarded", "why a late registration reaches nothing")
		mustContain(t, text, "Move the call into the registering module's Init callback", "the fixing action")
		mustContain(t, text, `"GET", "/health"`, "the method and path that were refused")
	})
}

// TestAnExemptionThatCannotLandFailsTheStartup holds the two validations Start
// makes, each with its own sentinel.
//
// The unguarded case is the one worth spelling out: the direction is already
// fail-closed, since an exemption on an endpoint nobody guards changes nothing.
// Swallowing it silently is what the sentinel prevents — the registrant
// believes a route is public, and nothing anywhere says it is not.
func TestAnExemptionThatCannotLandFailsTheStartup(t *testing.T) {
	for _, c := range []struct {
		name      string
		guarded   []string
		endpoints []string
		endpoint  string
		method    string
		path      string
		want      error
	}{
		{
			name:      "the endpoint does not exist",
			guarded:   []string{"public"},
			endpoints: []string{"public"},
			endpoint:  "typo",
			method:    "GET",
			path:      "/health",
			want:      authn.ErrUnknownEndpoint,
		},
		{
			name:      "the endpoint exists but this gateway does not guard it",
			guarded:   []string{"public"},
			endpoints: []string{"public", "admin"},
			endpoint:  "admin",
			method:    "GET",
			path:      "/health",
			want:      authn.ErrExemptionUnguarded,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := testGateway(t, c.guarded, enumerating("local", "alice"))
			g.Exempt(c.endpoint, c.method, c.path)

			err := g.seal(routerWith(c.endpoints...))
			if !errors.Is(err, c.want) {
				t.Fatalf("sealing gave %v, want one wrapping %v", err, c.want)
			}
			for _, named := range []string{c.endpoint, c.method, c.path} {
				if !strings.Contains(err.Error(), named) {
					t.Errorf("the failure reads %q and does not name %q", err, named)
				}
			}
		})
	}
}

// TestTheFrozenTableIsWrittenByEndpointMethodAndPath holds the startup diagnostic
// that states what is public. It names the routes the way a registrant wrote
// them — method and path, under the endpoint they are on — and a route
// registered twice is one line of it, because it is one exemption.
func TestTheFrozenTableIsWrittenByEndpointMethodAndPath(t *testing.T) {
	logs := liveAssembly{
		endpoints: []string{"public", "admin"},
		backends:  []authn.Verifier{passes(enumerating("local", "alice"))},
		routes: []liveRoute{
			{endpoint: "public", method: "GET", path: "/health"},
			{endpoint: "public", method: "POST", path: "/login"},
			{endpoint: "admin", method: "GET", path: "/status"},
		},
		exempt: []liveRoute{
			{endpoint: "public", method: "POST", path: "/login"},
			{endpoint: "public", method: "GET", path: "/health"},
			{endpoint: "public", method: "GET", path: "/health"},
			{endpoint: "admin", method: "GET", path: "/status"},
		},
	}.serve(t, func(*live) {})

	frozen, ok := logs.recordNamed("authn: the exemption table, frozen")
	if !ok {
		t.Fatal("the startup diagnostics say nothing about the exemption table")
	}
	if got, want := attrText(frozen, "public"), "GET /health, POST /login"; got != want {
		t.Errorf("the frozen table lists %q for the endpoint public, want %q: each route once, "+
			"as method and path, in a stable order", got, want)
	}
	if got, want := attrText(frozen, "admin"), "GET /status"; got != want {
		t.Errorf("the frozen table lists %q for the endpoint admin, want %q", got, want)
	}
}

// TestAnEmptyFrozenTableSaysSo holds the other half of that diagnostic: with
// nothing registered it still speaks, and says that every route needs a
// credential, which is the sentence that makes an absent registration readable.
func TestAnEmptyFrozenTableSaysSo(t *testing.T) {
	attrs := exemptionAttrs(nil)
	if len(attrs) != 1 {
		t.Fatalf("an empty table is written as %d attribute(s), want the one that says so", len(attrs))
	}
	if rendered := renderAttrs(attrs); !strings.Contains(rendered, "none") ||
		!strings.Contains(rendered, "requires a credential") {
		t.Errorf("an empty table is written as %q, which does not say that nothing is exempt", rendered)
	}
}

// TestAnExemptRouteReachesNoBackendAtAll holds the ordering the whole exemption
// mechanism rests on.
//
// The exemption is judged before the Authorization header is read. Were it
// judged after, a public health check would consume a backend call on every
// probe, and a backend that is down would take the health check down with it —
// the one route that must answer when everything else cannot.
func TestAnExemptRouteReachesNoBackendAtAll(t *testing.T) {
	var asked atomic.Int64
	backend := passes(enumerating("local", "alice"))
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
		// The request carries a credential, so that what is held is the
		// ordering and not merely the absence of a header.
		exempt = l.send("public", "GET", "/health", asAlice)
		askedByExempt = asked.Load()
		guarded = l.send("public", "GET", "/orders", asAlice)
	})

	requireStatus(t, "an exempt route", exempt, nethttp.StatusOK)
	if askedByExempt != 0 {
		t.Errorf("a backend was asked %d time(s) on an exempt route, want none: the exemption is "+
			"judged before the credential is read", askedByExempt)
	}
	if want := "reached as nobody"; exempt.body != want {
		t.Errorf("the exempt route answered %q, want %q: nothing authenticated the request, so it "+
			"carries no identity", exempt.body, want)
	}

	// The guarded route with the same credential is the control: the backend
	// is reachable and the credential is good, so the silence above is the
	// exemption's doing.
	requireStatus(t, "a guarded route with a good credential", guarded, nethttp.StatusOK)
	if want := "reached as local/alice"; guarded.body != want {
		t.Errorf("the guarded route answered %q, want %q", guarded.body, want)
	}
	if got := asked.Load(); got != 1 {
		t.Errorf("a backend was asked %d time(s) in all, want the one from the guarded route", got)
	}
}

// TestAnExemptionMatchesTheRegisteredMethodAndPathOnly holds that the method is
// part of what is exempted.
//
// Only GET /both is exempted, and POST /both is a route on the same path that
// carries its own handler and its own business. Exempting by path alone would
// open the one by exempting the other.
func TestAnExemptionMatchesTheRegisteredMethodAndPathOnly(t *testing.T) {
	var get, post, other reply
	liveAssembly{
		backends: []authn.Verifier{passes(enumerating("local", "alice"))},
		routes: []liveRoute{
			{endpoint: "public", method: "GET", path: "/both"},
			{endpoint: "public", method: "POST", path: "/both"},
			{endpoint: "public", method: "GET", path: "/other"},
		},
		exempt: []liveRoute{{endpoint: "public", method: "GET", path: "/both"}},
	}.serve(t, func(l *live) {
		get = l.send("public", "GET", "/both")
		post = l.send("public", "POST", "/both")
		other = l.send("public", "GET", "/other")
	})

	requireStatus(t, "GET /both, the exempted route", get, nethttp.StatusOK)
	requireStatus(t, "POST /both, on the same path with another method", post, nethttp.StatusUnauthorized)
	requireChallenge(t, "POST /both", post)
	requireStatus(t, "GET /other, a route nobody exempted", other, nethttp.StatusUnauthorized)
}

// TestAnExemptionWrittenAsTheRouteWasRegisteredCoversThatRouteOnly holds the
// comparison for a route that carries a parameter or a catch-all.
//
// The exemption is written the way the route was registered, and what it covers
// is that route: every request the router matches to it, and no request matched
// to another. GET /accounts/me is a route of its own beside GET /accounts/:id —
// the router prefers the static segment — and its path is what the router
// reports for it, so the exemption of the parameter route does not reach it. A
// comparison that read the exemption as a pattern over the request's path would
// reach it.
func TestAnExemptionWrittenAsTheRouteWasRegisteredCoversThatRouteOnly(t *testing.T) {
	var parameter, sibling, catchAll, nested, elsewhere reply
	liveAssembly{
		backends: []authn.Verifier{passes(enumerating("local", "alice"))},
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
}

// TestAnExemptionSpelledDifferentlyFromEveryRouteNeverMatches holds the cost the
// comparison accepts: a registration that no route carries, in those exact
// characters, is not an error and is not a match.
//
// Each exemption below names a route that exists in some other spelling — a
// parameter called something else, one concrete instance of it, a trailing
// slash, a lower-case method, a path in another case, a pattern in the standard
// library's wildcard syntax — and none of them gets that route through. The
// startup succeeds anyway, because this module cannot read the routing table and
// so cannot tell a spelling no route carries from one a route will carry later.
// The empty path is the one the lookup has to refuse by name: it is what the
// router reports for a request that matched nothing, so an exemption registered
// with it would be one every unmatched request hit.
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
	got := make([]reply, len(requests))
	liveAssembly{
		backends: []authn.Verifier{passes(enumerating("local", "alice"))},
		routes:   routes,
		exempt:   spelled,
	}.serve(t, func(l *live) {
		for i, r := range requests {
			got[i] = l.send("public", r.method, r.path)
		}
	})

	for i, r := range requests {
		requireStatus(t, fmt.Sprintf("%s %s, which no exemption is spelled as", r.method, r.path),
			got[i], nethttp.StatusUnauthorized)
	}
}

// TestARequestTheRouterAnswersWithoutARouteIsStillChallenged holds the position
// of the gateway relative to the router's own answers: a request that matches no
// route, one the matched path does not allow the method for, and a HEAD to a
// route registered for GET alone, are all answered by the gateway first when
// they carry no credential, and by the router once they do.
//
// The gateway answering first is what keeps an unauthenticated caller from
// telling a path that does not exist from one that does, and that includes the
// Allow header a 405 carries: it names the methods a path accepts, so a refusal
// that carried it would answer the question the refusal exists to withhold. The
// credentialed half is what shows the router's answer, Allow included, was not
// lost.
//
// None of the three is a request an exemption covers, because the router reports
// no route for them. That is held with exemptions written for exactly those
// requests — GET /nowhere, POST /health and HEAD /health, none of them a route
// anyone registered — which would let the requests through if the path the
// router reports for an unmatched request, or for a request whose method the
// path does not allow, were the path that was asked for.
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

	bare := make([]reply, len(requests))
	credentialed := make([]reply, len(requests))
	liveAssembly{
		backends: []authn.Verifier{passes(enumerating("local", "alice"))},
		routes:   []liveRoute{{endpoint: "public", method: "GET", path: "/health"}},
		exempt: []liveRoute{
			{endpoint: "public", method: "GET", path: "/health"},
			{endpoint: "public", method: "GET", path: "/nowhere"},
			{endpoint: "public", method: "POST", path: "/health"},
			{endpoint: "public", method: "HEAD", path: "/health"},
		},
	}.serve(t, func(l *live) {
		for i, r := range requests {
			bare[i] = l.send("public", r.method, r.path)
			credentialed[i] = l.send("public", r.method, r.path, asAlice)
		}
	})

	for i, r := range requests {
		requireStatus(t, r.name+" without a credential", bare[i], nethttp.StatusUnauthorized)
		requireChallenge(t, r.name+" without a credential", bare[i])
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

// TestAnEndpointWithNoExemptionExemptsNothing holds what an endpoint carrying no
// registration is left with. Reading an empty table as "nothing was registered,
// so let everything through" would open every guarded endpoint that nobody
// wrote an exemption for.
func TestAnEndpointWithNoExemptionExemptsNothing(t *testing.T) {
	var health, nowhere reply
	liveAssembly{
		backends: []authn.Verifier{passes(enumerating("local", "alice"))},
		routes:   []liveRoute{{endpoint: "public", method: "GET", path: "/health"}},
	}.serve(t, func(l *live) {
		health = l.send("public", "GET", "/health")
		nowhere = l.send("public", "GET", "/nowhere")
	})

	requireStatus(t, "a route on an endpoint with no exemption", health, nethttp.StatusUnauthorized)
	requireStatus(t, "a path no route carries, on an endpoint with no exemption", nowhere,
		nethttp.StatusUnauthorized)
}

// TestAnExemptionOnlyExemptsTheEndpointItWasRegisteredOn holds the keying of the
// tables.
//
// An exemption is registered against one endpoint by name. A gateway that
// consulted the wrong endpoint's table — or one table for all of them — would
// open a public route's path on every endpoint it guards. That is an
// authentication bypass produced by a one-word mistake and visible nowhere
// else: both endpoints go on answering, one of them without a credential.
func TestAnExemptionOnlyExemptsTheEndpointItWasRegisteredOn(t *testing.T) {
	var opened, guarded reply
	liveAssembly{
		endpoints: []string{"public", "admin"},
		backends:  []authn.Verifier{passes(enumerating("local", "alice"))},
		routes: []liveRoute{
			{endpoint: "public", method: "GET", path: "/health"},
			{endpoint: "admin", method: "GET", path: "/health"},
		},
		exempt: []liveRoute{{endpoint: "public", method: "GET", path: "/health"}},
	}.serve(t, func(l *live) {
		opened = l.send("public", "GET", "/health")
		guarded = l.send("admin", "GET", "/health")
	})

	requireStatus(t, "the endpoint the exemption was registered on", opened, nethttp.StatusOK)
	requireStatus(t, "an endpoint nobody registered the exemption on, for the same route", guarded,
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

// mustContain fails when the rendered message does not carry the fragment,
// naming what the fragment stands for so that a missing one reads as the
// property that was lost rather than as a string mismatch.
func mustContain(t *testing.T, text, fragment, why string) {
	t.Helper()
	if !strings.Contains(text, fragment) {
		t.Errorf("the message does not carry %q, which is %s.\nThe message was: %s",
			fragment, why, text)
	}
}
