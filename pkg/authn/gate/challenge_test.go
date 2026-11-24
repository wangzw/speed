package gate

import (
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

func challengeOf(t *testing.T, g *gateway, endpoint string, edit func(*nethttp.Request) *nethttp.Request) string {
	t.Helper()
	w, _ := run(g, endpoint, edit(request("GET", "/x")))
	if w.Code != nethttp.StatusUnauthorized {
		t.Fatalf("the request answered %d, want %d", w.Code, nethttp.StatusUnauthorized)
	}
	return w.Header().Get(challengeHeader)
}

// TestARefusalTellsTheCallerWhatCredentialToSend holds the challenge each 401
// carries and the realm it is written with: the schemes the backends take, each
// as the scheme is conventionally written, once, in order, with the endpoint's
// own name as the realm.
func TestARefusalTellsTheCallerWhatCredentialToSend(t *testing.T) {
	g := testGateway(t, []string{"admin", "public"},
		basicBackend("static", "static."), schemeBackend("token", "bearer"))
	sealed(t, g, routerWith("admin", "public"))

	for endpoint, want := range map[string]string{
		"public": `Basic realm="public", Bearer realm="public"`,
		"admin":  `Basic realm="admin", Bearer realm="admin"`,
	} {
		if got := challengeOf(t, g, endpoint, func(r *nethttp.Request) *nethttp.Request { return r }); got != want {
			t.Errorf("a 401 on %s carries %q, want %q", endpoint, got, want)
		}
	}
}

// TestBasicIsOfferedOnlyWhenABackendTakesIt holds the one scheme that makes a
// browser open a password prompt: offering it where no backend reads it would
// prompt for a password that is refused unread.
func TestBasicIsOfferedOnlyWhenABackendTakesIt(t *testing.T) {
	g := testGateway(t, []string{"public"}, schemeBackend("token", "bearer", "dpop"))
	sealed(t, g, routerWith("public"))

	got := challengeOf(t, g, "public", func(r *nethttp.Request) *nethttp.Request { return r })
	if strings.Contains(strings.ToLower(got), "basic") {
		t.Errorf("a 401 carries %q, which offers Basic though no backend takes it", got)
	}
	if want := `Bearer realm="public", Dpop realm="public"`; got != want {
		t.Errorf("a 401 carries %q, want %q", got, want)
	}
}

// TestACookieOnlyAssemblyAnswersWithTheCookieChallenge holds the one new case
// of the challenge: RFC 9110 requires a 401 to carry at least one, and with no
// scheme taken the gateway writes the cookie convention, which no standard
// client knows and which opens no password prompt.
func TestACookieOnlyAssemblyAnswersWithTheCookieChallenge(t *testing.T) {
	g := testGateway(t, []string{"public"}, cookieBackend("session", "sid"))
	sealed(t, g, routerWith("public"))

	got := challengeOf(t, g, "public", func(r *nethttp.Request) *nethttp.Request { return r })
	if want := `Cookie realm="public"`; got != want {
		t.Errorf("a 401 carries %q, want %q", got, want)
	}

	mixed := testGateway(t, []string{"public"}, cookieBackend("session", "sid"), schemeBackend("token", "bearer"))
	sealed(t, mixed, routerWith("public"))
	if got := challengeOf(t, mixed, "public", func(r *nethttp.Request) *nethttp.Request { return r }); strings.Contains(got, "Cookie") {
		t.Errorf("a 401 carries %q, which adds the cookie challenge beside a scheme", got)
	}
}

// TestABackendWritesItsOwnPartOfTheChallenge holds the Challenger: its strings
// replace the backend's default challenge, they are given the endpoint's realm
// already escaped, and the rest of the challenge is as it was.
func TestABackendWritesItsOwnPartOfTheChallenge(t *testing.T) {
	var realms []string
	var flags []bool
	token := challengingVerifier{
		fakeVerifier: schemeBackend("token", "bearer"),
		challenge: func(realm string, rejected bool) []string {
			realms = append(realms, realm)
			flags = append(flags, rejected)
			if rejected {
				return []string{`Bearer realm="` + realm + `", error="invalid_token"`}
			}
			return []string{`Bearer realm="` + realm + `"`}
		},
	}
	g := testGateway(t, []string{`pub"lic`}, basicBackend("static", "static."), token)
	sealed(t, g, routerWith(`pub"lic`))

	got := challengeOf(t, g, `pub"lic`, func(r *nethttp.Request) *nethttp.Request { return r })
	if want := `Basic realm="pub\"lic", Bearer realm="pub\"lic"`; got != want {
		t.Errorf("a 401 carries %q, want %q", got, want)
	}
	for _, realm := range realms {
		if realm != `pub\"lic` {
			t.Errorf("the backend was given the realm %q, want the escaped one", realm)
		}
	}
	if len(flags) != 2 || flags[0] || !flags[1] {
		t.Errorf("the backend was asked with rejected=%v, want the plain challenge and the rejected one, once each", flags)
	}
}

// TestARejectedCredentialGetsTheRejectedChallengeOfItsBackendAlone holds when
// the rejected form is used: a 401 on a request whose credential reached a
// backend and was refused carries that backend's rejected challenge, and
// every other 401 carries the plain one.
func TestARejectedCredentialGetsTheRejectedChallengeOfItsBackendAlone(t *testing.T) {
	var cleared atomic.Int64
	token := challengingVerifier{
		fakeVerifier: schemeBackend("token", "bearer"),
		challenge: func(realm string, rejected bool) []string {
			if rejected {
				return []string{`Bearer realm="` + realm + `", error="invalid_token"`}
			}
			return []string{`Bearer realm="` + realm + `"`}
		},
	}
	other := challengingVerifier{
		fakeVerifier: schemeBackend("dpop", "dpop"),
		challenge: func(realm string, rejected bool) []string {
			if rejected {
				return []string{`DPoP realm="` + realm + `", error="invalid_token"`}
			}
			return []string{`DPoP realm="` + realm + `"`}
		},
	}
	session := clearingVerifier{fakeVerifier: cookieBackend("session", "sid"), cleared: &cleared}
	token.verify = func(_ context.Context, c authn.Credential) authn.Verdict {
		switch c.Value {
		case "good":
			return held(token.fakeVerifier, "alice")
		case "down":
			return authn.Unavailable(nil)
		case "silent":
			return authn.Verdict{}
		}
		return authn.Denied(nil)
	}
	g := testGateway(t, []string{"public"}, basicBackend("static", "static."), token, other, session)
	sealed(t, g, routerWith("public"))

	plain := `Basic realm="public", Bearer realm="public", DPoP realm="public"`
	rejected := `Basic realm="public", Bearer realm="public", error="invalid_token", DPoP realm="public"`
	for _, c := range []struct {
		name string
		edit func(*nethttp.Request) *nethttp.Request
		want string
	}{
		{"no credential at all", func(r *nethttp.Request) *nethttp.Request { return r }, plain},
		{"a scheme nobody takes", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set(authHeader, "Digest x")
			return r
		}, plain},
		{"a token that is refused", func(r *nethttp.Request) *nethttp.Request { return withBearer(r, "bad") }, rejected},
		{"a token the backend answers nothing about", func(r *nethttp.Request) *nethttp.Request { return withBearer(r, "silent") }, rejected},
		{"a Basic credential, whose backend writes no challenge of its own", func(r *nethttp.Request) *nethttp.Request {
			return withBasic(r, "a", "b")
		}, plain},
		{"a cookie that is refused", func(r *nethttp.Request) *nethttp.Request { return withCookie(r, "sid", "stale") }, plain},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := challengeOf(t, g, "public", c.edit); got != c.want {
				t.Errorf("a 401 carries %q, want %q", got, c.want)
			}
		})
	}

	w, _ := run(g, "public", withBearer(request("GET", "/x"), "down"))
	if w.Code != nethttp.StatusServiceUnavailable || w.Header().Get(challengeHeader) != "" {
		t.Errorf("an outage answered %d with the challenge %q, want a 503 with none", w.Code, w.Header().Get(challengeHeader))
	}
}

// TestAChallengerThatWritesNothingOrWritesABadLineDoesNotBreakTheResponse holds
// what the gateway does with what a backend wrote: empty strings are dropped, a
// line ending would be the start of a second header and is dropped, and a 401
// never goes out with no challenge at all.
func TestAChallengerThatWritesNothingOrWritesABadLineDoesNotBreakTheResponse(t *testing.T) {
	for name, given := range map[string][]string{
		"nothing":           nil,
		"empty strings":     {"", "  "},
		"a line ending":     {"Bearer realm=\"x\"\r\nX-Evil: 1"},
		"a bare line feed":  {"Bearer\nX-Evil: 1"},
		"a mix with a good": {"", "Bearer realm=\"x\"\nX-Evil: 1", "Bearer realm=\"public\""},
	} {
		t.Run(name, func(t *testing.T) {
			token := challengingVerifier{
				fakeVerifier: schemeBackend("token", "bearer"),
				challenge:    func(string, bool) []string { return given },
			}
			g := testGateway(t, []string{"public"}, token)
			sealed(t, g, routerWith("public"))

			w, _ := run(g, "public", request("GET", "/x"))
			got := w.Header().Get(challengeHeader)
			if got == "" {
				t.Fatal("a 401 went out with no challenge")
			}
			if strings.ContainsAny(got, "\r\n") || w.Header().Get("X-Evil") != "" {
				t.Errorf("the challenge %q smuggled a line ending or a header", got)
			}
			if name == "a mix with a good" && got != `Bearer realm="public"` {
				t.Errorf("the challenge reads %q, want the good line alone", got)
			}
		})
	}
}

// TestARealmCannotBreakOutOfTheChallenge holds the escaping of the endpoint
// name written into the quoted realm.
func TestARealmCannotBreakOutOfTheChallenge(t *testing.T) {
	for name, want := range map[string]string{
		`pu"blic`:       `Basic realm="pu\"blic"`,
		`pub\lic`:       `Basic realm="pub\\lic"`,
		"pub\r\nlic":    `Basic realm="public"`,
		`public", x="y`: `Basic realm="public\", x=\"y"`,
	} {
		g := testGateway(t, []string{name}, basicBackend("static", "static."))
		sealed(t, g, routerWith(name))
		w, _ := run(g, name, request("GET", "/x"))
		if got := w.Header().Get(challengeHeader); got != want {
			t.Errorf("the endpoint %q is challenged with %q, want %q", name, got, want)
		}
		if strings.ContainsAny(w.Header().Get(challengeHeader), "\r\n") {
			t.Errorf("the endpoint %q is challenged with a line ending", name)
		}
	}
}

// TestTheChallengeIsBuiltOncePerEndpointInInit holds that the request path does
// not call the backends' Challenger: it is called when the layer is built, for
// the plain and the rejected form, and never again however many requests come.
func TestTheChallengeIsBuiltOncePerEndpointInInit(t *testing.T) {
	var calls atomic.Int64
	token := challengingVerifier{
		fakeVerifier: schemeBackend("token", "bearer"),
		challenge: func(realm string, _ bool) []string {
			calls.Add(1)
			return []string{`Bearer realm="` + realm + `"`}
		},
	}
	g := testGateway(t, []string{"public"}, token)
	sealed(t, g, routerWith("public"))
	engine, _ := layerOn(g, "public")
	built := calls.Load()
	if built != 2 {
		t.Fatalf("building the layer called the Challenger %d time(s), want 2: the plain form and the rejected one", built)
	}

	for range 5 {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, request("GET", "/x"))
		engine.ServeHTTP(w, withBearer(request("GET", "/x"), "t"))
	}
	if got := calls.Load(); got != built {
		t.Errorf("the Challenger was called %d more time(s) on the request path, want none", got-built)
	}
}
