package gate

import (
	"context"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// The cases here hold the cookie side of the gateway: what counts as a cookie
// credential, which carrier wins when a request has both, what the cross-origin
// check lets through, and when a refused cookie is cleared. Each carrier is its
// own backend with its own call counter, because "which backend was asked, and
// how many times" is how these cases tell a request that was judged from one that
// was refused unread.

// carrierBackends builds the three backends a request can reach: a bearer one, a
// cookie one that can clear its cookie, and the Basic one. Each counts its calls.
type carrierBackends struct {
	token, session, basic *fakeVerifier
	tokenCalls            atomic.Int64
	sessionCalls          atomic.Int64
	basicCalls            atomic.Int64
	cleared               atomic.Int64
}

func newCarrierBackends() *carrierBackends {
	c := &carrierBackends{
		token:   schemeBackend("token", "bearer"),
		session: cookieBackend("session", "sid"),
		basic:   basicBackend("static", "static."),
	}
	c.token.asked = &c.tokenCalls
	c.session.asked = &c.sessionCalls
	c.basic.asked = &c.basicCalls
	return c
}

// verifiers lists the backends the gateway is built from, the cookie one wrapped
// so that clearing its cookie is observable.
func (c *carrierBackends) verifiers() []authn.Verifier {
	return []authn.Verifier{
		c.token,
		clearingVerifier{fakeVerifier: c.session, cleared: &c.cleared},
		c.basic,
	}
}

// gateway builds a gateway over the backends, sealed against a router with one
// endpoint named "public", and with the given settings filled in around it.
func (c *carrierBackends) gateway(t *testing.T, set settings) *gateway {
	t.Helper()
	set.endpoints = []string{"public"}
	set.budget, set.inFlight = time.Second, 8
	g := gatewayWith(t, set, c.verifiers()...)
	sealed(t, g, routerWith("public"))
	return g
}

// TestTwoCookiesOfOneNameAreAmbiguousEvenWhenTheValuesAgree holds the planting
// defence at its narrowest: a request carrying the same cookie name twice is
// refused whatever the values are, because the gateway cannot tell the one the
// browser set from the one a page of a sibling subdomain set, and equal values
// are not evidence that they are the same cookie.
func TestTwoCookiesOfOneNameAreAmbiguousEvenWhenTheValuesAgree(t *testing.T) {
	for name, values := range map[string][2]string{
		"different values": {"cookie-value-1", "cookie-value-2"},
		"equal values":     {"cookie-value-same", "cookie-value-same"},
	} {
		t.Run(name, func(t *testing.T) {
			b := newCarrierBackends()
			passes(b.session)
			g := b.gateway(t, settings{})

			h := &recorder{}
			r := withCookie(withCookie(recorded(h, "GET", "/x"), "sid", values[0]), "sid", values[1])
			engine, seen := layerOn(g, "public")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, r)

			if w.Code != nethttp.StatusUnauthorized || seen.reached {
				t.Fatalf("answered %d (reached %v), want a refusal", w.Code, seen.reached)
			}
			if b.sessionCalls.Load() != 0 {
				t.Errorf("the cookie backend was asked %d time(s) about an ambiguous request", b.sessionCalls.Load())
			}
			if got := g.tally()["denied"]; got != 1 {
				t.Errorf("counted %d time(s) as denied, want 1: %v", got, g.tally())
			}
			rec := h.only(t)
			if attrText(rec, "attributed_to") != ModuleName || attrText(rec, "cookie_name") != "sid" {
				t.Errorf("the record is attributed to %q about the cookie %q, want the gateway and sid",
					attrText(rec, "attributed_to"), attrText(rec, "cookie_name"))
			}
			for _, value := range values {
				if strings.Contains(rec.Message+attrText(rec, "cause")+attrText(rec, "cookie_name"), value) {
					t.Errorf("the record carries the cookie value %q", value)
				}
			}
		})
	}
}

// TestAHeaderThatDoesNotHoldIsTheOnlyCredentialAsked holds the header's priority
// from the side of the call counts: a header that is refused is asked of its own
// backend once, and the cookie that would have held is asked of nobody, so the
// refusal cannot be turned into a pass by adding a cookie.
func TestAHeaderThatDoesNotHoldIsTheOnlyCredentialAsked(t *testing.T) {
	b := newCarrierBackends()
	passes(b.session) // the cookie would hold; the header's backend refuses
	g := b.gateway(t, settings{})

	w, seen := run(g, "public", withCookie(withBearer(request("GET", "/x"), "stale"), "sid", "good"))

	if w.Code != nethttp.StatusUnauthorized || seen.reached {
		t.Fatalf("answered %d (reached %v), want a refusal", w.Code, seen.reached)
	}
	if b.tokenCalls.Load() != 1 {
		t.Errorf("the header's backend was asked %d time(s), want once", b.tokenCalls.Load())
	}
	if b.sessionCalls.Load() != 0 {
		t.Errorf("the cookie backend was asked %d time(s), want none: the header decides alone", b.sessionCalls.Load())
	}
	if handed := b.token.credentials(); len(handed) != 1 || handed[0].Scheme != "bearer" || handed[0].Value != "stale" || handed[0].Cookie != "" {
		t.Errorf("the header's backend was handed %+v, want the bearer credential and no cookie", handed)
	}
	if b.cleared.Load() != 0 {
		t.Error("a cookie was cleared for a request whose credential came in a header")
	}
}

// TestAValidHeaderLeavesTheCookieUnread holds the other half: a header that holds
// is judged alone, and a cookie beside it, even a second one under the same name
// that would be an ambiguity on its own, is neither read nor judged.
func TestAValidHeaderLeavesTheCookieUnread(t *testing.T) {
	b := newCarrierBackends()
	passes(b.token)
	passes(b.session)
	g := b.gateway(t, settings{})

	r := withCookie(withCookie(withBearer(request("GET", "/x"), "good"), "sid", "a"), "sid", "b")
	w, seen := run(g, "public", r)

	if w.Code != nethttp.StatusOK || !seen.carried {
		t.Fatalf("answered %d (identity %v), want the header to carry the request", w.Code, seen.carried)
	}
	if b.sessionCalls.Load() != 0 {
		t.Errorf("the cookie backend was asked %d time(s), want none", b.sessionCalls.Load())
	}
	if seen.id.Challenge() != "Bearer" {
		t.Errorf("the challenge name reads %q, want the header's scheme", seen.id.Challenge())
	}
}

// TestACookieNoBackendTakesIsNotACredential holds that a cookie is a carrier only
// when a backend claimed its name: any other cookie on the request, the site's
// own preferences and trackers included, reads as no credential at all.
func TestACookieNoBackendTakesIsNotACredential(t *testing.T) {
	t.Run("on a route that requires one", func(t *testing.T) {
		b := newCarrierBackends()
		g := b.gateway(t, settings{})

		h := &recorder{}
		engine, seen := layerOn(g, "public")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, withCookie(withCookie(recorded(h, "GET", "/x"), "theme", "dark"), "lang", "en"))

		if w.Code != nethttp.StatusUnauthorized || seen.reached {
			t.Fatalf("answered %d (reached %v), want a refusal", w.Code, seen.reached)
		}
		if b.sessionCalls.Load()+b.tokenCalls.Load() != 0 {
			t.Error("a backend was asked about a cookie nobody takes")
		}
		rec := h.only(t)
		if !strings.Contains(rec.Message, "no credential") || attrText(rec, "attributed_to") != ModuleName {
			t.Errorf("the record reads %q attributed to %q, want the gateway's no-credential refusal",
				rec.Message, attrText(rec, "attributed_to"))
		}
	})

	t.Run("on an optional route", func(t *testing.T) {
		b := newCarrierBackends()
		g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: time.Second, inFlight: 8}, b.verifiers()...)
		g.Optional("public", "GET", "/catalog")
		sealed(t, g, routerWith("public"))

		engine, seen := routedEngine(g, "public", [2]string{"GET", "/catalog"})
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, withCookie(request("GET", "/catalog"), "theme", "dark"))

		if w.Code != nethttp.StatusOK || seen.carried {
			t.Errorf("answered %d (identity %v), want a pass with no identity", w.Code, seen.carried)
		}
		if got := g.tally()["anonymous"]; got != 1 {
			t.Errorf("counted %d time(s) as anonymous, want 1: %v", got, g.tally())
		}
	})

	t.Run("a repeated cookie nobody takes beside a claimed one", func(t *testing.T) {
		b := newCarrierBackends()
		passes(b.session)
		g := b.gateway(t, settings{})

		r := withCookie(withCookie(withCookie(request("GET", "/x"), "theme", "dark"), "theme", "light"), "sid", "v")
		w, seen := run(g, "public", r)

		if w.Code != nethttp.StatusOK || !seen.carried {
			t.Errorf("answered %d (identity %v), want the claimed cookie to carry the request: a repeated "+
				"name nobody takes is not an ambiguity about a credential", w.Code, seen.carried)
		}
	})
}

// TestACookieCredentialOnAnOptionalRouteGoesAllTheWayThrough holds that optional
// means "no credential is fine" and nothing more: a cookie that holds gives an
// identity, stamped with the cookie challenge and the optional access level, and
// a cookie that does not hold is a 401 that clears it.
func TestACookieCredentialOnAnOptionalRouteGoesAllTheWayThrough(t *testing.T) {
	for _, c := range []struct {
		name    string
		answer  authn.Verdict
		status  int
		cleared int64
	}{
		{"a cookie that holds", held(cookieBackend("session", "sid"), "alice"), nethttp.StatusOK, 0},
		{"a cookie that does not", authn.Denied(nil), nethttp.StatusUnauthorized, 1},
		{"a cookie the backend cannot judge", authn.Unavailable(nil), nethttp.StatusServiceUnavailable, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newCarrierBackends()
			b.session.verify = func(context.Context, authn.Credential) authn.Verdict { return c.answer }
			g := gatewayWith(t, settings{endpoints: []string{"public"}, budget: time.Second, inFlight: 8}, b.verifiers()...)
			g.Optional("public", "GET", "/catalog")
			sealed(t, g, routerWith("public"))

			engine, seen := routedEngine(g, "public", [2]string{"GET", "/catalog"})
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, withCookie(request("GET", "/catalog"), "sid", "v"))

			if w.Code != c.status {
				t.Fatalf("answered %d, want %d", w.Code, c.status)
			}
			if b.sessionCalls.Load() != 1 {
				t.Errorf("the cookie backend was asked %d time(s), want once", b.sessionCalls.Load())
			}
			if b.cleared.Load() != c.cleared {
				t.Errorf("the cookie was cleared %d time(s), want %d", b.cleared.Load(), c.cleared)
			}
			if g.tally()["anonymous"] != 0 {
				t.Error("a request that carried a credential was counted as anonymous")
			}
			if c.status != nethttp.StatusOK {
				return
			}
			if !seen.carried || seen.id.Challenge() != "Cookie" || seen.access != authn.AccessOptional {
				t.Errorf("the identity reads carried=%v challenge=%q access=%v, want one stamped with "+
					"the Cookie challenge on an optional route", seen.carried, seen.id.Challenge(), seen.access)
			}
		})
	}
}

// TestACookieCredentialIsHandedOverWithItsNameAndNoScheme holds the shape of what
// a cookie backend is handed: the cookie's name and value as they arrived, and an
// empty scheme, because a cookie is not an authentication scheme and a backend
// that took the empty one for Basic would be reading something that is not there.
func TestACookieCredentialIsHandedOverWithItsNameAndNoScheme(t *testing.T) {
	b := newCarrierBackends()
	passes(b.session)
	g := b.gateway(t, settings{})

	r := withCookie(withCookie(request("GET", "/x"), "theme", "dark"), "sid", "opaque")
	r.RemoteAddr = "203.0.113.9:4711"
	if w, _ := run(g, "public", r); w.Code != nethttp.StatusOK {
		t.Fatalf("answered %d", w.Code)
	}

	handed := b.session.credentials()
	if len(handed) != 1 {
		t.Fatalf("the cookie backend was handed %d credential(s), want one", len(handed))
	}
	want := authn.Credential{Cookie: "sid", Value: "opaque", Peer: "203.0.113.9"}
	if handed[0] != want || handed[0].Scheme != "" {
		t.Errorf("the backend was handed %+v, want %+v", handed[0], want)
	}
}

// TestTheCrossOriginCheckHoldsEveryVectorTheDesignNames holds the check against
// the cases the rules in the design describe: Sec-Fetch-Site, Origin against
// Host, the trusted origins in the form a browser sends them, and a request that
// carries neither header. The check is the standard library's, so the cases are also a record of
// what it does with each, which is what the design's non-guarantee is written
// against.
//
// The request with neither Sec-Fetch-Site nor Origin is let through. That is the
// library's behaviour and a known boundary: a client that sends neither is a
// program or a browser too old for the check, and nothing here can tell the two
// apart. The case asserts that it passes so that the boundary is a recorded one.
//
// The check applies to the two carriers a browser attaches to a request by
// itself, a cookie and a Basic header, which the browser stores after a Basic
// challenge and sends on a cross-site navigation. A Bearer header is never
// attached by a browser, so a cross-site request carrying one is not held to it.
func TestTheCrossOriginCheckHoldsEveryVectorTheDesignNames(t *testing.T) {
	const sibling = "https://sibling.example.com"
	trusting := settings{trustedOrigins: []string{sibling}}

	type headers = map[string]string
	const (
		cookie = "cookie"
		bearer = "bearer"
		basic  = "basic"
	)
	for _, c := range []struct {
		name    string
		method  string
		headers headers
		set     settings
		carrier string
		passes  bool
	}{
		{"same-origin", "POST", headers{"Sec-Fetch-Site": "same-origin"}, settings{}, cookie, true},
		{"none, as a typed address or a bookmark", "POST", headers{"Sec-Fetch-Site": "none"}, settings{}, cookie, true},
		{"cross-site", "POST", headers{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, settings{}, cookie, false},
		{"same-site", "POST", headers{"Sec-Fetch-Site": "same-site", "Origin": sibling}, settings{}, cookie, false},
		{"same-site from a trusted origin", "POST", headers{"Sec-Fetch-Site": "same-site", "Origin": sibling}, trusting, cookie, true},
		{"cross-site from a trusted origin", "POST", headers{"Sec-Fetch-Site": "cross-site", "Origin": sibling}, trusting, cookie, true},
		{
			"cross-site from the public URL's origin", "POST",
			headers{"Sec-Fetch-Site": "cross-site", "Origin": "https://app.example.net"},
			settings{mount: mountConfig{PublicURL: "https://app.example.net"}},
			cookie, true,
		},
		{"no Sec-Fetch-Site, the Origin is the Host", "POST", headers{"Origin": "http://example.com"}, settings{}, cookie, true},
		{"no Sec-Fetch-Site, the Origin is not the Host", "POST", headers{"Origin": "https://evil.example"}, settings{}, cookie, false},
		{"no Sec-Fetch-Site, an Origin that is trusted", "POST", headers{"Origin": sibling}, trusting, cookie, true},
		{"neither header, a known boundary of the library's check", "POST", headers{}, settings{}, cookie, true},

		{"trusted origin in another case", "POST", headers{"Sec-Fetch-Site": "same-site", "Origin": "https://SIBLING.example.com"}, trusting, cookie, false},
		{"trusted origin on another port", "POST", headers{"Sec-Fetch-Site": "same-site", "Origin": sibling + ":8443"}, trusting, cookie, false},
		{"trusted origin on another scheme", "POST", headers{"Sec-Fetch-Site": "same-site", "Origin": "http://sibling.example.com"}, trusting, cookie, false},
		{"trusted origin with a trailing slash", "POST", headers{"Sec-Fetch-Site": "same-site", "Origin": sibling + "/"}, trusting, cookie, false},
		{"another host under the trusted one", "POST", headers{"Sec-Fetch-Site": "same-site", "Origin": "https://x.sibling.example.com"}, trusting, cookie, false},

		{"a cross-site PUT", "PUT", headers{"Sec-Fetch-Site": "cross-site"}, settings{}, cookie, false},
		{"a cross-site PATCH", "PATCH", headers{"Sec-Fetch-Site": "cross-site"}, settings{}, cookie, false},
		{"a cross-site DELETE", "DELETE", headers{"Sec-Fetch-Site": "cross-site"}, settings{}, cookie, false},
		{"a cross-site GET", "GET", headers{"Sec-Fetch-Site": "cross-site"}, settings{}, cookie, true},
		{"a cross-site HEAD", "HEAD", headers{"Sec-Fetch-Site": "cross-site"}, settings{}, cookie, true},
		{"a cross-site OPTIONS", "OPTIONS", headers{"Sec-Fetch-Site": "cross-site"}, settings{}, cookie, true},

		{"a cross-site POST whose credential is a Bearer header", "POST", headers{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, settings{}, bearer, true},
		{"a cross-site PUT whose credential is a Bearer header", "PUT", headers{"Sec-Fetch-Site": "cross-site"}, settings{}, bearer, true},

		{"a cross-site POST whose credential is a Basic header", "POST", headers{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, settings{}, basic, false},
		{"a cross-site DELETE whose credential is a Basic header", "DELETE", headers{"Sec-Fetch-Site": "cross-site"}, settings{}, basic, false},
		{"a same-site POST whose credential is a Basic header, from an origin nobody trusts", "POST", headers{"Sec-Fetch-Site": "same-site", "Origin": sibling}, settings{}, basic, false},
		{"a cross-site POST whose credential is a Basic header, from a trusted origin", "POST", headers{"Sec-Fetch-Site": "cross-site", "Origin": sibling}, trusting, basic, true},
		{"a same-origin POST whose credential is a Basic header", "POST", headers{"Sec-Fetch-Site": "same-origin"}, settings{}, basic, true},
		{"a POST with no browser headers whose credential is a Basic header, as a program sends it", "POST", headers{}, settings{}, basic, true},
		{"a cross-site GET whose credential is a Basic header", "GET", headers{"Sec-Fetch-Site": "cross-site"}, settings{}, basic, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newCarrierBackends()
			passes(b.session)
			passes(b.token)
			passes(b.basic)
			g := b.gateway(t, c.set)

			h := &recorder{}
			r := recorded(h, c.method, "/x")
			for k, v := range c.headers {
				r.Header.Set(k, v)
			}
			switch c.carrier {
			case bearer:
				withBearer(r, "t")
			case basic:
				withBasic(r, "static.bob", "pw")
			default:
				withCookie(r, "sid", "v")
			}
			engine, seen := layerOn(g, "public")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, r)

			if c.passes {
				if w.Code != nethttp.StatusOK || !seen.carried {
					t.Fatalf("answered %d (identity %v), want a pass", w.Code, seen.carried)
				}
				return
			}
			if w.Code != nethttp.StatusUnauthorized {
				t.Fatalf("answered %d, want 401: the gateway does not write 403", w.Code)
			}
			if seen.reached {
				t.Error("the request reached the layer behind the gateway")
			}
			if b.sessionCalls.Load() != 0 || b.basicCalls.Load() != 0 {
				t.Error("the backend was asked about a credential from a request that failed the check")
			}
			if b.cleared.Load() != 0 || w.Result().Header.Get("Set-Cookie") != "" {
				t.Errorf("the cookie was cleared (%d) or Set-Cookie written (%q) for a request nothing was judged about",
					b.cleared.Load(), w.Result().Header.Get("Set-Cookie"))
			}
			if got := g.tally()["denied"]; got != 1 {
				t.Errorf("counted %d time(s) as denied, want 1: %v", got, g.tally())
			}
			rec := h.only(t)
			if attrText(rec, "attributed_to") != ModuleName || rec.Level != slog.LevelInfo {
				t.Errorf("the record is attributed to %q at %v, want the gateway at Info",
					attrText(rec, "attributed_to"), rec.Level)
			}
			if w.Header().Get(challengeHeader) == "" {
				t.Errorf("the refusal carries no %s header", challengeHeader)
			}
		})
	}
}

// TestTheCrossOriginCheckSparesOnlyTheBearerScheme holds the criterion of the
// check at the scheme a backend that is not one of this module's own can
// declare. The gateway reads nothing of a credential and cannot know whether a
// browser stores and sends a scheme by itself, which it does for Basic, Digest,
// Negotiate and NTLM and for no scheme that a page's own script writes, so the
// one header scheme it knows is never attached by a browser is Bearer and every
// other is held to the check. A cross-site request that carries a scheme nobody
// listed is refused unread; a list of the schemes browsers keep would let the
// next one through.
func TestTheCrossOriginCheckSparesOnlyTheBearerScheme(t *testing.T) {
	const sibling = "https://sibling.example.com"
	crossSite := map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}

	for _, c := range []struct {
		scheme  string
		headers map[string]string
		set     settings
		passes  bool
	}{
		{"digest", crossSite, settings{}, false},
		{"negotiate", crossSite, settings{}, false},
		{"ntlm", crossSite, settings{}, false},
		{"apikey", crossSite, settings{}, false},
		{"bearer", crossSite, settings{}, true},
		{"Bearer", crossSite, settings{}, true},
		{"negotiate", map[string]string{"Sec-Fetch-Site": "same-origin"}, settings{}, true},
		{"negotiate", map[string]string{}, settings{}, true},
		{"negotiate", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": sibling}, settings{trustedOrigins: []string{sibling}}, true},
	} {
		name := c.scheme + " from " + c.headers["Origin"] + " (" + c.headers["Sec-Fetch-Site"] + ")"
		t.Run(name, func(t *testing.T) {
			backend := passes(schemeBackend("other", c.scheme))
			var asked atomic.Int64
			backend.asked = &asked
			c.set.endpoints = []string{"public"}
			c.set.budget, c.set.inFlight = time.Second, 8
			g := gatewayWith(t, c.set, backend)
			sealed(t, g, routerWith("public"))

			h := &recorder{}
			r := recorded(h, "POST", "/x")
			for k, v := range c.headers {
				r.Header.Set(k, v)
			}
			r.Header.Set(authHeader, c.scheme+" credential")
			engine, seen := layerOn(g, "public")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, r)

			if c.passes {
				if w.Code != nethttp.StatusOK || !seen.carried {
					t.Fatalf("answered %d (identity %v), want a pass", w.Code, seen.carried)
				}
				return
			}
			if w.Code != nethttp.StatusUnauthorized || seen.reached {
				t.Fatalf("answered %d (reached %v), want a 401 and nothing behind the gateway", w.Code, seen.reached)
			}
			if asked.Load() != 0 {
				t.Errorf("the backend was asked %d time(s) about a credential from a request that failed the check", asked.Load())
			}
			if rec := h.only(t); attrText(rec, "attributed_to") != ModuleName {
				t.Errorf("the refusal is attributed to %q, want the gateway", attrText(rec, "attributed_to"))
			}
		})
	}
}

// TestACookieIsClearedOnlyWhenTheBackendRefusedIt holds the one trigger of
// ClearCookie from every other way a request with a cookie can end badly: only a
// refusal by the backend that took the cookie clears it. A refusal the gateway
// made itself, before the backend was asked or after it answered, says nothing
// about whether the cookie holds, and clearing it would log a user out for the
// gateway's own decision.
func TestACookieIsClearedOnlyWhenTheBackendRefusedIt(t *testing.T) {
	cookieOnly := func(r *nethttp.Request) *nethttp.Request { return withCookie(r, "sid", "v") }
	for _, c := range []struct {
		name   string
		answer func(*fakeVerifier) authn.Verdict
		edit   func(*nethttp.Request) *nethttp.Request
		clear  bool
	}{
		{"the backend refuses the cookie", func(*fakeVerifier) authn.Verdict { return authn.Denied(nil) }, cookieOnly, true},
		{"the backend cannot answer", func(*fakeVerifier) authn.Verdict { return authn.Unavailable(nil) }, cookieOnly, false},
		{"the backend answers nothing", func(*fakeVerifier) authn.Verdict { return authn.Verdict{} }, cookieOnly, false},
		{
			"the cookie is refused by the cross-origin check", func(*fakeVerifier) authn.Verdict { return authn.Denied(nil) },
			func(r *nethttp.Request) *nethttp.Request {
				r.Method = "POST"
				r.Header.Set("Sec-Fetch-Site", "cross-site")
				return withCookie(r, "sid", "v")
			}, false,
		},
		{"the principal fails the exit check", func(f *fakeVerifier) authn.Verdict {
			p := principalOf(f, "x")
			p.Account = "static.root"
			return authn.Authenticated(p)
		}, cookieOnly, false},
		{"the cookie names an impersonation the route does not admit", func(f *fakeVerifier) authn.Verdict {
			p := principalOf(f, "customer-1")
			p.Actor, p.Session, p.Scope = "admin-9", "0198f0aa-0000-7000-8000-000000000002", "tenant-a"
			return authn.Authenticated(p)
		}, cookieOnly, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newCarrierBackends()
			b.session.accepts.Impersonation = true
			answer := c.answer
			b.session.verify = func(context.Context, authn.Credential) authn.Verdict { return answer(b.session) }
			g := b.gateway(t, settings{})

			w, _ := run(g, "public", c.edit(request("GET", "/x")))

			if w.Code < 400 {
				t.Fatalf("answered %d, want a refusal", w.Code)
			}
			if got := b.cleared.Load(); (got == 1) != c.clear || got > 1 {
				t.Errorf("the cookie was cleared %d time(s), want %v", got, c.clear)
			}
			if cleared := strings.Contains(w.Result().Header.Get("Set-Cookie"), "Max-Age=0"); cleared != c.clear {
				t.Errorf("the response clears the cookie: %v, want %v", cleared, c.clear)
			}
		})
	}

	t.Run("a header credential that is refused", func(t *testing.T) {
		b := newCarrierBackends()
		g := b.gateway(t, settings{})
		run(g, "public", withBearer(request("GET", "/x"), "stale"))
		if b.cleared.Load() != 0 {
			t.Error("a cookie was cleared for a refused header")
		}
	})

	t.Run("a cookie that holds", func(t *testing.T) {
		b := newCarrierBackends()
		passes(b.session)
		g := b.gateway(t, settings{})
		run(g, "public", cookieOnly(request("GET", "/x")))
		if b.cleared.Load() != 0 {
			t.Error("a cookie that holds was cleared")
		}
	})
}
