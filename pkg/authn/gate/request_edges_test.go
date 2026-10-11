package gate

import (
	"context"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
)

func TestRefusedCookieIsClearedInTheCommittedResponse(t *testing.T) {
	b := newCarrierBackends()
	g := b.gateway(t, settings{})
	w, _ := run(g, "public", withCookie(request("GET", "/x"), "sid", "stale"))
	if w.Code != nethttp.StatusUnauthorized {
		t.Fatalf("answered %d, want 401", w.Code)
	}
	// Result() is the snapshot taken when the header was committed; the
	// recorder's live Header() map also shows writes made after the response
	// went out, which a real connection would have dropped.
	if got := strings.Join(w.Result().Header.Values("Set-Cookie"), ";"); !strings.Contains(got, "Max-Age=0") {
		t.Errorf("the committed response carries Set-Cookie %q, want the cookie cleared", got)
	}
}

func TestCrossOriginCheckHoldsOnOptionalAndImpersonableRoutes(t *testing.T) {
	for _, c := range []struct {
		name     string
		register func(*gateway)
	}{
		{"optional", func(g *gateway) { g.Optional("public", "POST", "/catalog") }},
		{"impersonable", func(g *gateway) { g.Impersonable("public", "POST", "/catalog") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			var asked atomic.Int64
			cookie := passes(cookieBackend("session", "sid"))
			cookie.asked = &asked
			g := testGateway(t, []string{"public"}, cookie)
			c.register(g)
			sealed(t, g, routerWith("public"))

			engine, seen := routedEngine(g, "public", [2]string{"POST", "/catalog"})
			r := withCookie(request("POST", "/catalog"), "sid", "v")
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			r.Header.Set("Origin", "https://evil.example")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, r)

			if w.Code != nethttp.StatusUnauthorized || seen.reached {
				t.Errorf("answered %d (reached %v), want a 401 that ends the request", w.Code, seen.reached)
			}
			if asked.Load() != 0 {
				t.Error("the backend was asked about a cookie on a request that failed the cross-origin check")
			}
		})
	}
}

// TestExitAndScopeRefusalsAreChallengedAsARejectedCredential holds that a refusal
// the gateway judged itself, a pass that fails the exit check and an impersonating
// pass on a route that does not admit one, is challenged the way a refusal of a
// credential the backend took is: the request carries a credential that backend
// claimed, and it is refused, so the 401 carries the backend's rejected
// challenge, which for a token backend is the one with error="invalid_token". The
// plain challenge would tell the caller to send a credential it already sent.
func TestExitAndScopeRefusalsAreChallengedAsARejectedCredential(t *testing.T) {
	for name, principal := range map[string]func(*fakeVerifier) authn.Principal{
		"the principal fails the exit check": func(f *fakeVerifier) authn.Principal {
			p := principalOf(f, "x")
			p.Account = "static.root"
			return p
		},
		"an impersonation on a route that does not admit one": func(f *fakeVerifier) authn.Principal {
			p := principalOf(f, "customer-1")
			p.Actor, p.Session, p.Scope = "admin-9", "0198f0aa-0000-7000-8000-000000000002", "tenant-a"
			return p
		},
	} {
		t.Run(name, func(t *testing.T) {
			token := challengingVerifier{
				fakeVerifier: schemeBackend("token", "bearer"),
				challenge: func(realm string, rejected bool) []string {
					if rejected {
						return []string{`Bearer realm="` + realm + `", error="invalid_token"`}
					}
					return []string{`Bearer realm="` + realm + `"`}
				},
			}
			token.accepts.Impersonation = true
			token.verify = func(context.Context, authn.Credential) authn.Verdict {
				return authn.Authenticated(principal(token.fakeVerifier))
			}
			g := testGateway(t, []string{"public"}, token)
			sealed(t, g, routerWith("public"))

			got := challengeOf(t, g, "public", func(r *nethttp.Request) *nethttp.Request { return withBearer(r, "t") })
			if want := `Bearer realm="public", error="invalid_token"`; got != want {
				t.Errorf("a 401 carries %q, want %q", got, want)
			}
		})
	}
}

func TestHeaderRefusalClearsNoCookieOfABackendThatTakesBothCarriers(t *testing.T) {
	var cleared atomic.Int64
	hybrid := clearingVerifier{
		fakeVerifier: &fakeVerifier{name: "hybrid", accepts: authn.Accepts{Schemes: []string{"bearer"}, Cookies: []string{"sid"}}},
		cleared:      &cleared,
	}
	g := testGateway(t, []string{"public"}, hybrid)
	sealed(t, g, routerWith("public"))

	w, _ := run(g, "public", withBearer(request("GET", "/x"), "stale"))
	if w.Code != nethttp.StatusUnauthorized {
		t.Fatalf("answered %d, want 401", w.Code)
	}
	if cleared.Load() != 0 || len(w.Result().Header.Values("Set-Cookie")) != 0 {
		t.Errorf("a refused Authorization header cleared the cookie (%d call(s), Set-Cookie %v)",
			cleared.Load(), w.Result().Header.Values("Set-Cookie"))
	}
	run(g, "public", withCookie(request("GET", "/x"), "sid", "stale"))
	if cleared.Load() != 1 {
		t.Errorf("a refused cookie was cleared %d time(s), want once", cleared.Load())
	}
}

func TestRegistrationsAreComparedCaseSensitively(t *testing.T) {
	for _, exempt := range []string{"/health", "/Health"} {
		t.Run(exempt, func(t *testing.T) {
			g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
			g.Exempt("public", "GET", exempt)
			sealed(t, g, routerWith("public"))
			engine, seen := routedEngine(g, "public", [2]string{"GET", "/health"}, [2]string{"GET", "/Health"})
			for _, path := range []string{"/health", "/Health"} {
				seen.reached = false
				w := httptest.NewRecorder()
				engine.ServeHTTP(w, request("GET", path))
				if path == exempt {
					if w.Code != nethttp.StatusOK || !seen.reached {
						t.Errorf("%s is exempt and answered %d (reached %v)", path, w.Code, seen.reached)
					}
					continue
				}
				if w.Code != nethttp.StatusUnauthorized || seen.reached {
					t.Errorf("%s is a different route from the exempt %s and answered %d (reached %v), want a 401",
						path, exempt, w.Code, seen.reached)
				}
			}
		})
	}
}

func TestAnExemptRouteReadsNoCookieEither(t *testing.T) {
	b := newCarrierBackends()
	g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: time.Second, inFlight: 8}, b.verifiers()...)
	g.Exempt("public", "GET", "/health")
	sealed(t, g, routerWith("public"))

	engine, seen := routedEngine(g, "public", [2]string{"GET", "/health"})
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, withCookie(request("GET", "/health"), "sid", "stale"))

	if w.Code != nethttp.StatusOK || !seen.reached {
		t.Errorf("answered %d (reached %v), want the request let through", w.Code, seen.reached)
	}
	if b.sessionCalls.Load() != 0 || b.cleared.Load() != 0 || len(w.Result().Header.Values("Set-Cookie")) != 0 {
		t.Errorf("a cookie on an exempt route was judged (%d call(s)), cleared (%d) or answered with Set-Cookie %v",
			b.sessionCalls.Load(), b.cleared.Load(), w.Result().Header.Values("Set-Cookie"))
	}
}

func TestTheCallContextEndsOnceTheCallReturns(t *testing.T) {
	var handed context.Context
	basic := basicBackend("static", "static.")
	basic.verify = func(ctx context.Context, _ authn.Credential) authn.Verdict {
		handed = ctx
		return held(basic, "alice")
	}
	g := testGateway(t, []string{"public"}, basic) // a budget of a second
	sealed(t, g, routerWith("public"))

	w, _ := run(g, "public", withBasic(request("GET", "/x"), "alice", "secret"))
	if w.Code != nethttp.StatusOK {
		t.Fatalf("answered %d, want 200", w.Code)
	}
	if handed == nil || !errors.Is(handed.Err(), context.Canceled) {
		t.Errorf("the backend's context reads %v after the call returned, want it cancelled: its timer "+
			"and any goroutine watching it live on until the budget runs out", handed)
	}
}

func TestRejectedChallengeKeepsTheChallengesOfBackendsThatSortAfterIt(t *testing.T) {
	first := challengingVerifier{
		fakeVerifier: schemeBackend("aaa", "bearer"),
		challenge: func(realm string, rejected bool) []string {
			if rejected {
				return []string{`Bearer realm="` + realm + `", error="invalid_token"`}
			}
			return []string{`Bearer realm="` + realm + `"`}
		},
	}
	g := testGateway(t, []string{"public"}, first, schemeBackend("zzz", "dpop"))
	sealed(t, g, routerWith("public"))
	got := challengeOf(t, g, "public", func(r *nethttp.Request) *nethttp.Request { return withBearer(r, "bad") })
	if want := `Bearer realm="public", error="invalid_token", Dpop realm="public"`; got != want {
		t.Errorf("a 401 carries %q, want %q", got, want)
	}
}

func TestBackendChallengeLinesAreTrimmedDeduplicatedAndFreeOfLineEndings(t *testing.T) {
	for _, c := range []struct {
		name  string
		given []string
		want  string
	}{
		{"surrounding blanks", []string{`  Bearer realm="x"  `}, `Bearer realm="x"`},
		{"a bare carriage return", []string{"Bearer\rX-Evil: 1", `Bearer realm="public"`}, `Bearer realm="public"`},
		{"the same line twice, then another", []string{`Bearer realm="x"`, `Bearer realm="x"`, `DPoP realm="x"`}, `Bearer realm="x", DPoP realm="x"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			token := challengingVerifier{
				fakeVerifier: schemeBackend("token", "bearer"),
				challenge:    func(string, bool) []string { return c.given },
			}
			g := testGateway(t, []string{"public"}, token)
			sealed(t, g, routerWith("public"))
			w, _ := run(g, "public", request("GET", "/x"))
			if got := w.Header().Get(challengeHeader); got != c.want {
				t.Errorf("a 401 carries %q, want %q", got, c.want)
			}
		})
	}
}

// TestCookieNameMatchedExactlyOnTheRequestPath holds that the request path matches a cookie name exactly, as the backend declared it.
func TestCookieNameMatchedExactlyOnTheRequestPath(t *testing.T) {
	for _, c := range []struct {
		name    string
		claimed string
		sent    string
		taken   bool
	}{
		{"mixed-case name as declared", "__Host-sid", "__Host-sid", true},
		{"mixed-case name sent in lower case", "__Host-sid", "__host-sid", false},
		{"mixed-case name sent in upper case", "__Host-sid", "__HOST-SID", false},
		{"lower-case name sent capitalised", "sid", "Sid", false},
		{"lower-case name sent in upper case", "sid", "SID", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var asked atomic.Int64
			b := passes(cookieBackend("session", c.claimed))
			b.asked = &asked
			g := testGateway(t, []string{"public"}, b)
			sealed(t, g, routerWith("public"))
			w, seen := run(g, "public", withCookie(request("GET", "/x"), c.sent, "v"))
			if c.taken {
				if w.Code != nethttp.StatusOK || !seen.carried || asked.Load() != 1 {
					t.Fatalf("answered %d (carried %v, asked %d), want the cookie taken", w.Code, seen.carried, asked.Load())
				}
				return
			}
			if w.Code != nethttp.StatusUnauthorized || asked.Load() != 0 {
				t.Fatalf("answered %d (asked %d), want %q to be no credential", w.Code, asked.Load(), c.sent)
			}
		})
	}
}

// TestThePreExistingVaryIsKept holds that an earlier layer's Vary survives.
func TestThePreExistingVaryIsKept(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(basicBackend("static", "static.")))
	sealed(t, g, routerWith("public"))
	seen := &arrival{}
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Header("Vary", "Origin") })
	engine.Use(g.middlewareFor("public").Handler)
	engine.NoRoute(func(c *gin.Context) { record(seen, c); c.Status(nethttp.StatusOK) })
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, withBasic(request("GET", "/x"), "a", "b"))
	if got := strings.Join(w.Header().Values("Vary"), ", "); got != "Origin, Authorization, Cookie" {
		t.Errorf("Vary reads %q", got)
	}
}

// TestPeerIsTheTransportPeerWhateverTheForwardingHeadersSay holds that peer is the transport peer whatever a client writes in forwarding headers.
func TestPeerIsTheTransportPeerWhateverTheForwardingHeadersSay(t *testing.T) {
	b := passes(basicBackend("static", "static."))
	g := testGateway(t, []string{"public"}, b)
	sealed(t, g, routerWith("public"))
	seen := &arrival{}
	engine := gin.New()
	if err := engine.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}
	engine.Use(g.middlewareFor("public").Handler)
	engine.NoRoute(func(c *gin.Context) { record(seen, c); c.Status(nethttp.StatusOK) })
	r := withBasic(request("GET", "/x"), "a", "b")
	r.RemoteAddr = "203.0.113.9:4711"
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	r.Header.Set("X-Real-IP", "198.51.100.8")
	engine.ServeHTTP(httptest.NewRecorder(), r)
	if got := b.credentials(); len(got) != 1 || got[0].Peer != "203.0.113.9" {
		t.Errorf("the backend was handed %+v, want the peer 203.0.113.9", got)
	}
}

// TestWhitespaceAroundTheAuthorizationFieldIsIgnored holds that whitespace around the whole Authorization field is not part of the scheme.
func TestWhitespaceAroundTheAuthorizationFieldIsIgnored(t *testing.T) {
	for _, field := range []string{" Bearer t", "Bearer t ", "\tBearer t\t"} {
		t.Run(strings.ReplaceAll(field, "\t", "<tab>"), func(t *testing.T) {
			b := passes(schemeBackend("token", "bearer"))
			g := testGateway(t, []string{"public"}, b)
			sealed(t, g, routerWith("public"))
			r := request("GET", "/x")
			r.Header.Set(authHeader, field)
			w, seen := run(g, "public", r)
			if w.Code != nethttp.StatusOK || !seen.carried {
				t.Fatalf("answered %d (carried %v)", w.Code, seen.carried)
			}
			if got := b.credentials(); len(got) != 1 || got[0].Value != "t" {
				t.Errorf("the backend was handed %+v", got)
			}
		})
	}
}

// TestTwoCookieHeaderLinesOfOneNameAreAmbiguous holds that a second Cookie header line is read like the first.
func TestTwoCookieHeaderLinesOfOneNameAreAmbiguous(t *testing.T) {
	var asked atomic.Int64
	b := passes(cookieBackend("session", "sid"))
	b.asked = &asked
	g := testGateway(t, []string{"public"}, b)
	sealed(t, g, routerWith("public"))
	r := request("GET", "/x")
	r.Header.Add("Cookie", "sid=a")
	r.Header.Add("Cookie", "sid=b")
	w, seen := run(g, "public", r)
	if w.Code != nethttp.StatusUnauthorized || seen.reached || asked.Load() != 0 {
		t.Errorf("answered %d (reached %v, asked %d), want an ambiguity refusal", w.Code, seen.reached, asked.Load())
	}
}

// TestCrossOriginRefusalRecordNamesBasicAndCause holds that the refusal of a Basic
// credential names the scheme it came under and the cause and carries no cookie
// name.
func TestCrossOriginRefusalRecordNamesBasicAndCause(t *testing.T) {
	b := newCarrierBackends()
	passes(b.basic)
	g := b.gateway(t, settings{})
	h := &recorder{}
	r := recorded(h, "POST", "/x")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	withBasic(r, "static.bob", "secretvalue")
	engine, _ := layerOn(g, "public")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, r)
	rec := h.only(t)
	if !strings.Contains(rec.Message, "basic credential") || attrText(rec, "cause") == "" || attrText(rec, "cookie_name") != "" {
		t.Errorf("the record reads %q with cause %q and cookie name %q", rec.Message, attrText(rec, "cause"), attrText(rec, "cookie_name"))
	}
	if strings.Contains(rec.Message+attrText(rec, "cause"), "secretvalue") {
		t.Errorf("the record carries the password: %q", rec.Message)
	}
	if b.basicCalls.Load() != 0 {
		t.Error("the Basic backend was asked about a request that failed the cross-origin check")
	}
}

// TestCrossOriginRefusalRecordNamesCookieAndCause holds that the cross-origin refusal names the cookie and the cause.
func TestCrossOriginRefusalRecordNamesCookieAndCause(t *testing.T) {
	b := newCarrierBackends()
	passes(b.session)
	g := b.gateway(t, settings{})
	h := &recorder{}
	r := recorded(h, "POST", "/x")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	withCookie(r, "sid", "secretvalue")
	engine, _ := layerOn(g, "public")
	engine.ServeHTTP(httptest.NewRecorder(), r)
	rec := h.only(t)
	if attrText(rec, "cookie_name") != "sid" || attrText(rec, "cause") == "" {
		t.Errorf("the record carries cookie %q and cause %q", attrText(rec, "cookie_name"), attrText(rec, "cause"))
	}
}

func TestOutageLogsWhyTheBackendCouldNotAnswer(t *testing.T) {
	basic := basicBackend("static", "static.")
	basic.verify = func(context.Context, authn.Credential) authn.Verdict {
		return authn.Unavailable(errors.New("credential store unreachable"))
	}
	g := testGateway(t, []string{"public"}, basic)
	sealed(t, g, routerWith("public"))
	_, rec := conclusionOf(t, g, "public", func(r *nethttp.Request) *nethttp.Request { return withBasic(r, "a", "b") })
	if got := attrText(rec, "cause"); !strings.Contains(got, "credential store unreachable") {
		t.Errorf("the record carries the cause %q, want the backend's own", got)
	}
}

func TestAFullGateAndAStoppedGatewayAreToldApartInTheLog(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(*gateway)
		want  string
	}{
		{"full", func(g *gateway) {
			for range cap(g.slots) {
				g.slots <- struct{}{}
			}
		}, "full"},
		{"stopped", func(g *gateway) { g.stop() }, "stopped"},
	} {
		t.Run(c.name, func(t *testing.T) {
			basic := passes(basicBackend("static", "static."))
			g := testGateway(t, []string{"public"}, basic)
			sealed(t, g, routerWith("public"))
			c.setup(g)
			_, rec := conclusionOf(t, g, "public", func(r *nethttp.Request) *nethttp.Request { return withBasic(r, "a", "b") })
			if got := attrText(rec, "cause"); !strings.Contains(got, c.want) {
				t.Errorf("the record carries the cause %q, want it to say %q", got, c.want)
			}
		})
	}
}

func TestAnEmptyAuthorizationHeaderIsRefusedAsHavingNoScheme(t *testing.T) {
	g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
	sealed(t, g, routerWith("public"))
	_, rec := conclusionOf(t, g, "public", func(r *nethttp.Request) *nethttp.Request {
		r.Header.Set(authHeader, "")
		return r
	})
	if !strings.Contains(rec.Message, "names no scheme") {
		t.Errorf("the refusal reads %q, want it to say the header names no scheme", rec.Message)
	}
}

// TestClipBoundsAndNeutralisesWhatCameFromOutside holds what clip does to a value that came from outside: a value of exactly the limit is kept, one over it is cut and marked, control characters are replaced and printable characters are kept.
func TestClipBoundsAndNeutralisesWhatCameFromOutside(t *testing.T) {
	if got := clip(strings.Repeat("a", 64)); got != strings.Repeat("a", 64) {
		t.Errorf("a value of exactly the limit was changed: %q", got)
	}
	if got := clip(strings.Repeat("a", 65)); got != strings.Repeat("a", 64)+"..." {
		t.Errorf("a value over the limit reads %q", got)
	}
	if got := clip("a\x7fb\x01c\x1fd"); got != "a?b?c?d" {
		t.Errorf("control characters read %q", got)
	}
	if got := clip(" ~é"); got != " ~é" {
		t.Errorf("printable characters were changed: %q", got)
	}
}

// bareEngine is an engine whose only handler behind the layer writes a status, so
// that concurrent requests share no recording state.
func bareEngine(g *gateway, endpoint string) *gin.Engine {
	engine := gin.New()
	engine.Use(g.middlewareFor(endpoint).Handler)
	engine.NoRoute(func(c *gin.Context) { c.Status(nethttp.StatusOK) })
	return engine
}

// TestACookieCredentialOnAnOptionalRouteIsHeldToTheSameChecks holds that a route
// which accepts a request with no credential does not accept a cookie credential
// on easier terms: the cross-origin check applies to an unsafe request, and a
// cookie that cannot be told from another is a refusal, not an anonymous caller.
// Reading either as "no credential" would let a forged or planted cookie through
// as a visitor, on the routes that are public to begin with.
func TestACookieCredentialOnAnOptionalRouteIsHeldToTheSameChecks(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*nethttp.Request) *nethttp.Request
	}{
		{"a cross-site unsafe request carrying a claimed cookie", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			r.Header.Set("Origin", "https://evil.example")
			return withCookie(r, "sid", "v")
		}},
		{"a cross-site unsafe request carrying a Basic header", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			r.Header.Set("Origin", "https://evil.example")
			return withBasic(r, "static.bob", "pw")
		}},
		{"one cookie name twice", func(r *nethttp.Request) *nethttp.Request {
			return withCookie(withCookie(r, "sid", "a"), "sid", "b")
		}},
		{"two claimed cookie names of two backends", func(r *nethttp.Request) *nethttp.Request {
			return withCookie(withCookie(r, "sid", "a"), "other", "b")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newCarrierBackends()
			passes(b.session)
			passes(b.basic)
			other := passes(cookieBackend("other", "other"))
			g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: time.Second, inFlight: 8},
				append(b.verifiers(), other)...)
			g.Optional("public", "POST", "/catalog")
			sealed(t, g, routerWith("public"))

			engine, seen := routedEngine(g, "public", [2]string{"POST", "/catalog"})
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, c.edit(request("POST", "/catalog")))

			if w.Code != nethttp.StatusUnauthorized || seen.reached {
				t.Fatalf("answered %d (reached %v), want a 401 that ends the request", w.Code, seen.reached)
			}
			if b.sessionCalls.Load() != 0 || b.basicCalls.Load() != 0 || len(other.credentials()) != 0 {
				t.Error("a backend was asked about a request that was refused before any was")
			}
			if b.cleared.Load() != 0 || len(w.Result().Header.Values("Set-Cookie")) != 0 {
				t.Error("a cookie was cleared for a request nothing was judged about")
			}
			if got := g.tally(); got["denied"] != 1 || got["anonymous"] != 0 {
				t.Errorf("counted %v, want one denied and no anonymous", got)
			}
		})
	}
}

// TestAtMostOneClaimedCookieMayComeInARequest holds the reading of two claimed
// cookies of different names: which was meant cannot be told, so the request is
// refused as a whole, whether the two names belong to one backend or to two, the
// same as for one name twice. The refusal clears neither cookie, so a browser that
// holds both stays refused until one of them expires: a gateway that picked one,
// or cleared one, would let a cookie planted for this site decide which credential
// is judged. A name is therefore never claimed beside the one it replaces: the old
// cookie is let expire before the new name is enabled. Each cookie alone is a
// credential, so the refusal is the pair and not either.
func TestAtMostOneClaimedCookieMayComeInARequest(t *testing.T) {
	for _, c := range []struct {
		name     string
		backends func(cleared *atomic.Int64, asked *atomic.Int64) []authn.Verifier
	}{
		{"two names of one backend", func(cleared, asked *atomic.Int64) []authn.Verifier {
			b := passes(cookieBackend("session", "sid", "sid-next"))
			b.asked = asked
			return []authn.Verifier{clearingVerifier{fakeVerifier: b, cleared: cleared}}
		}},
		{"a name of each of two backends", func(cleared, asked *atomic.Int64) []authn.Verifier {
			one := passes(cookieBackend("session", "sid"))
			one.asked = asked
			two := passes(cookieBackend("session-next", "sid-next"))
			two.asked = asked
			return []authn.Verifier{
				clearingVerifier{fakeVerifier: one, cleared: cleared},
				clearingVerifier{fakeVerifier: two, cleared: cleared},
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var asked, cleared atomic.Int64
			g := testGateway(t, []string{"public"}, c.backends(&cleared, &asked)...)
			sealed(t, g, routerWith("public"))

			w, seen := run(g, "public", withCookie(withCookie(request("GET", "/x"), "sid", "a"), "sid-next", "b"))
			if w.Code != nethttp.StatusUnauthorized || seen.reached || asked.Load() != 0 {
				t.Errorf("answered %d (reached %v, asked %d), want a refusal before the backend is asked",
					w.Code, seen.reached, asked.Load())
			}
			if cleared.Load() != 0 || len(w.Result().Header.Values("Set-Cookie")) != 0 {
				t.Errorf("a cookie was cleared (%d call(s), Set-Cookie %v) for a request nothing was judged about",
					cleared.Load(), w.Result().Header.Values("Set-Cookie"))
			}
			if w.Header().Get(challengeHeader) == "" {
				t.Errorf("the refusal carries no %s header", challengeHeader)
			}
			if got := g.tally(); got["denied"] != 1 {
				t.Errorf("counted %v, want one denied", got)
			}

			// Once one of the two is gone, the other is a credential again.
			for _, name := range []string{"sid", "sid-next"} {
				if w, _ := run(g, "public", withCookie(request("GET", "/x"), name, "v")); w.Code != nethttp.StatusOK {
					t.Errorf("the cookie %s alone answered %d, want %d", name, w.Code, nethttp.StatusOK)
				}
			}
		})
	}
}
