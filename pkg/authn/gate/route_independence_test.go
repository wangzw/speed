package gate

import (
	"fmt"
	nethttp "net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// The cases in this file hold the gateway's part of the promise the entry point
// describes in its dispatch section: on a guarded endpoint, an unauthenticated
// request is answered by the gateway before the endpoint's router writes its own
// answer, so the status, the headers and the body do not depend on which route
// the request matched, whether any route matched, or whether the method is
// allowed.
//
// This is a regression guard, not a fail-before: the behaviour exists today, and
// the case is here to notice a change that would give an unauthenticated caller
// a way to read the endpoint's route table. The non-hollowness of the cookie
// half is the Set-Cookie assertion: a wrong cookie name would silently turn
// every cookie request into an anonymous one, which is indistinguishable from
// the anonymous refusal except for the clear the backend writes.

// routeIndependenceRequest is one shape of request whose refusal must be
// identical to the others'.
type routeIndependenceRequest struct {
	name   string
	method string
	path   string
}

// replyHeaders renders a reply's headers without Date. Date is the one header
// outside the comparison: two requests made at different moments may legitimately
// straddle a second. Same-name values keep the order the server wrote them in,
// because Set-Cookie is the header where that order is the value's meaning.
func replyHeaders(r reply) string {
	names := make([]string, 0, len(r.header))
	for name := range r.header {
		if nethttp.CanonicalHeaderKey(name) == "Date" {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, name+": "+strings.Join(r.header[name], "\x00"))
	}
	return strings.Join(lines, "\n")
}

// requireSameReply fails unless two replies have the same status and the same
// headers but for Date, and, when the two requests carry a body, the same body.
// The body is left out for HEAD, which has none by protocol.
func requireSameReply(t *testing.T, what, wantName string, want reply, gotName string, got reply, compareBody bool) {
	t.Helper()
	if want.status != got.status {
		t.Errorf("%s: %s answered %d and %s answered %d, so the answer tells the caller "+
			"which route matched", what, wantName, want.status, gotName, got.status)
	}
	if wantHeaders, gotHeaders := replyHeaders(want), replyHeaders(got); wantHeaders != gotHeaders {
		t.Errorf("%s: %s and %s carry different headers:\n  %s: {%s}\n  %s: {%s}",
			what, wantName, gotName, wantName, wantHeaders, gotName, gotHeaders)
	}
	if compareBody && want.body != got.body {
		t.Errorf("%s: %s and %s carry different bodies:\n  %s: %q\n  %s: %q",
			what, wantName, gotName, wantName, want.body, gotName, got.body)
	}
}

// requireClearsCookie fails unless the reply carries a Set-Cookie that deletes
// the named cookie. It is what proves the backend was asked about the cookie
// rather than the request being read as anonymous.
func requireClearsCookie(t *testing.T, what string, got reply, name string) {
	t.Helper()
	value := got.header.Get("Set-Cookie")
	if value == "" {
		t.Errorf("%s carries no Set-Cookie: the cookie named %q was never read and judged, "+
			"so this reply is the anonymous one and the comparison it takes part in holds nothing",
			what, name)
		return
	}
	if !strings.HasPrefix(value, name+"=") {
		t.Errorf("%s clears %q, not the cookie this assembly's backend takes, %q", what, value, name)
		return
	}
	if !strings.Contains(value, "Max-Age=0") {
		t.Errorf("%s carries %q, which does not delete the cookie", what, value)
	}
}

// asCookie puts a cookie with the given name and value on a client request. The
// server-side withCookie builds a request for the middleware directly; these
// cases go through the entry point over a socket.
func asCookie(name, value string) func(*nethttp.Request) {
	return func(r *nethttp.Request) {
		r.AddCookie(&nethttp.Cookie{Name: name, Value: value})
	}
}

// asUntrustedOrigin marks a request as made by a page of another origin, which
// the cross-origin check refuses on an unsafe method carrying a browser-attached
// credential.
func asUntrustedOrigin(r *nethttp.Request) {
	r.Header.Set("Origin", "https://evil.example")
}

// TestTheRefusalDoesNotDependOnTheMatchedRoute holds the gateway's promise
// against the real entry point on a real socket: five shapes of unauthenticated
// request, each with three kinds of credential, all answered identically.
//
// The five shapes cover the routes the router can report: a registered guarded
// route, no match at all, a method the registered exempt route does not allow, a
// HEAD to a route registered for GET only, and an OPTIONS preflight with no
// Origin. The router reports no route for the last three, so a gateway that let
// the entry point answer them would show 405 with Allow or 404, and one that
// passed unmatched paths through would show the route's own status. With no
// credential, with a Bearer no backend accepts, and with an invalid cookie whose
// name is read off the backend's declaration, every one of the five must produce
// the same status, the same headers and the same body. The declared name is one
// no production code names, so a gateway that ignored the declaration and wrote
// the session backend's default in its place would read no cookie here at all.
func TestTheRefusalDoesNotDependOnTheMatchedRoute(t *testing.T) {
	cookie := cookieBackend("session", "__Host-gate-probe")
	cookieName := cookie.accepts.Cookies[0]
	cleared := new(atomic.Int64)
	backends := []authn.Verifier{
		schemeBackend("tokenz", "bearer"),
		&clearingVerifier{fakeVerifier: cookie, cleared: cleared},
	}

	classes := []routeIndependenceRequest{
		{"a registered guarded route", nethttp.MethodGet, "/orders"},
		{"a path no route carries", nethttp.MethodGet, "/nowhere"},
		{"a method the exempt route does not allow", nethttp.MethodPost, "/health"},
		{"HEAD to a route registered for GET only", nethttp.MethodHead, "/orders"},
		{"OPTIONS without Origin", nethttp.MethodOptions, "/orders"},
	}
	const invalidCookie = "an invalid cookie"
	credentials := []struct {
		name string
		edit func(*nethttp.Request)
	}{
		{"no credential", nil},
		{"Bearer zz", asBearer("zz")},
		{invalidCookie, asCookie(cookieName, "invalid")},
	}

	replies := make(map[string]map[string]reply, len(credentials))
	for _, cred := range credentials {
		replies[cred.name] = make(map[string]reply, len(classes))
	}
	var (
		optionalBearer, optionalCookie reply
		unsafeRoute, unsafeNowhere     reply
	)

	liveAssembly{
		backends: backends,
		routes: []liveRoute{
			{endpoint: "public", method: nethttp.MethodGet, path: "/orders"},
			{endpoint: "public", method: nethttp.MethodPost, path: "/orders"},
			{endpoint: "public", method: nethttp.MethodGet, path: "/health"},
			{endpoint: "public", method: nethttp.MethodGet, path: "/catalog"},
		},
		exempt:   []liveRoute{{endpoint: "public", method: nethttp.MethodGet, path: "/health"}},
		optional: []liveRoute{{endpoint: "public", method: nethttp.MethodGet, path: "/catalog"}},
	}.serve(t, func(l *live) {
		for _, cred := range credentials {
			for _, class := range classes {
				if cred.edit == nil {
					replies[cred.name][class.name] = l.send("public", class.method, class.path)
					continue
				}
				replies[cred.name][class.name] = l.send("public", class.method, class.path, cred.edit)
			}
		}
		optionalBearer = l.send("public", nethttp.MethodGet, "/catalog", asBearer("zz"))
		optionalCookie = l.send("public", nethttp.MethodGet, "/catalog", asCookie(cookieName, "invalid"))
		unsafeRoute = l.send("public", nethttp.MethodPost, "/orders", asCookie(cookieName, "invalid"), asUntrustedOrigin)
		unsafeNowhere = l.send("public", nethttp.MethodPost, "/nowhere", asCookie(cookieName, "invalid"), asUntrustedOrigin)
	})

	// The reference is the request no route matched: the answer that cannot
	// depend on a route at all.
	reference := classes[1].name
	for _, cred := range credentials {
		want := replies[cred.name][reference]
		for _, class := range classes {
			got := replies[cred.name][class.name]
			requireStatus(t, fmt.Sprintf("%s with %s", class.name, cred.name), got, nethttp.StatusUnauthorized)
			// A HEAD reply has no body by protocol, so the body is compared
			// wherever there is one.
			requireSameReply(t,
				fmt.Sprintf("%s with %s", class.name, cred.name),
				reference, want, class.name, got,
				class.method != nethttp.MethodHead)
		}
	}

	// The cookie half is not vacuous only if the backend was reached: the clear
	// is written by the backend that judged the cookie, and no anonymous refusal
	// carries it.
	for _, class := range classes {
		requireClearsCookie(t, class.name+" with an invalid cookie", replies[invalidCookie][class.name], cookieName)
	}
	// Five shapes plus the optional route reach the backend with the invalid
	// cookie; the two refused by the cross-origin check reach no backend at all.
	if want := int64(len(classes) + 1); cleared.Load() != want {
		t.Errorf("the cookie backend judged %d cookie presentation(s), want %d: the five shapes "+
			"of request and the optional route reach it, and the cross-origin refusal does not",
			cleared.Load(), want)
	}

	// An optional route with a credential that does not hold is refused exactly
	// as the unmatched path is: Optional only answers a request with no
	// credential.
	requireSameReply(t, "an optional route with an invalid Bearer", reference,
		replies["Bearer zz"][reference], "GET /catalog", optionalBearer, true)
	requireSameReply(t, "an optional route with an invalid cookie", reference,
		replies[invalidCookie][reference], "GET /catalog", optionalCookie, true)
	requireClearsCookie(t, "an optional route with an invalid cookie", optionalCookie, cookieName)

	// An unsafe request carrying a cookie and an untrusted Origin is refused by
	// the cross-origin check before any backend is asked, so it is the same on a
	// registered route and on a path no route carries, and it clears nothing.
	requireStatus(t, "POST /orders with a cookie and an untrusted Origin", unsafeRoute, nethttp.StatusUnauthorized)
	requireStatus(t, "POST /nowhere with a cookie and an untrusted Origin", unsafeNowhere, nethttp.StatusUnauthorized)
	requireSameReply(t, "an unsafe request with a cookie and an untrusted Origin",
		"POST /nowhere", unsafeNowhere, "POST /orders", unsafeRoute, true)
	if value := unsafeRoute.header.Get("Set-Cookie"); value != "" {
		t.Errorf("a request refused by the cross-origin check carries %q; the credential was not "+
			"judged, so nothing may clear it", value)
	}
}
