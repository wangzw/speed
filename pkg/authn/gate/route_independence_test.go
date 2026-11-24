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

// routeIndependenceRequest is one shape of request whose refusal must be
// identical to the others'.
type routeIndependenceRequest struct {
	name   string
	method string
	path   string
}

// replyHeaders renders a reply's headers without Date, and keeps same-name
// values in the order the server wrote them.
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

func asCookie(name, value string) func(*nethttp.Request) {
	return func(r *nethttp.Request) {
		r.AddCookie(&nethttp.Cookie{Name: name, Value: value})
	}
}

func asUntrustedOrigin(r *nethttp.Request) {
	r.Header.Set("Origin", "https://evil.example")
}

// TestTheRefusalDoesNotDependOnTheMatchedRoute answers five shapes of
// unauthenticated request, each with three credentials, identically.
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

	reference := classes[1].name
	for _, cred := range credentials {
		want := replies[cred.name][reference]
		for _, class := range classes {
			got := replies[cred.name][class.name]
			requireStatus(t, fmt.Sprintf("%s with %s", class.name, cred.name), got, nethttp.StatusUnauthorized)
			requireSameReply(t,
				fmt.Sprintf("%s with %s", class.name, cred.name),
				reference, want, class.name, got,
				class.method != nethttp.MethodHead)
		}
	}

	for _, class := range classes {
		requireClearsCookie(t, class.name+" with an invalid cookie", replies[invalidCookie][class.name], cookieName)
	}
	if want := int64(len(classes) + 1); cleared.Load() != want {
		t.Errorf("the cookie backend judged %d cookie presentation(s), want %d: the five shapes "+
			"of request and the optional route reach it, and the cross-origin refusal does not",
			cleared.Load(), want)
	}

	requireSameReply(t, "an optional route with an invalid Bearer", reference,
		replies["Bearer zz"][reference], "GET /catalog", optionalBearer, true)
	requireSameReply(t, "an optional route with an invalid cookie", reference,
		replies[invalidCookie][reference], "GET /catalog", optionalCookie, true)
	requireClearsCookie(t, "an optional route with an invalid cookie", optionalCookie, cookieName)

	requireStatus(t, "POST /orders with a cookie and an untrusted Origin", unsafeRoute, nethttp.StatusUnauthorized)
	requireStatus(t, "POST /nowhere with a cookie and an untrusted Origin", unsafeNowhere, nethttp.StatusUnauthorized)
	requireSameReply(t, "an unsafe request with a cookie and an untrusted Origin",
		"POST /nowhere", unsafeNowhere, "POST /orders", unsafeRoute, true)
	if value := unsafeRoute.header.Get("Set-Cookie"); value != "" {
		t.Errorf("a request refused by the cross-origin check carries %q; the credential was not "+
			"judged, so nothing may clear it", value)
	}
}
