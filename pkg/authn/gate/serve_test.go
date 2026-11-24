package gate

import (
	"context"
	"errors"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/identity"
	"github.com/vislake/speed/pkg/log"
)

func init() {
	// gin prints a debug banner for every engine it builds in debug mode, and
	// the entry point's own engines run in release mode; the engines these
	// cases build do the same.
	gin.SetMode(gin.ReleaseMode)
}

type arrival struct {
	reached bool
	id      identity.Identity
	carried bool
	access  authn.Access
}

func run(g *gateway, endpoint string, r *nethttp.Request) (*httptest.ResponseRecorder, *arrival) {
	engine, seen := layerOn(g, endpoint)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, r)
	return w, seen
}

// layerOn builds the gateway's layer for one endpoint without serving anything
// through it; a case that breaks the gateway's state builds the layer first and
// breaks it afterwards, since the layer is assembled in Init.
func layerOn(g *gateway, endpoint string) (*gin.Engine, *arrival) {
	return engineWith(g.middlewareFor(endpoint).Handler)
}

// engineWith is a gin engine carrying one layer, with a handler behind it that
// records what it was handed and answers every request, whatever its path. A
// request matching no route still reaches the layer; routedEngine is for routes.
func engineWith(layer gin.HandlerFunc) (*gin.Engine, *arrival) {
	seen := &arrival{}
	engine := gin.New()
	engine.Use(layer)
	engine.NoRoute(func(c *gin.Context) {
		record(seen, c)
		c.Status(nethttp.StatusOK)
	})
	return engine, seen
}

func record(seen *arrival, c *gin.Context) {
	seen.reached = true
	seen.id, seen.carried = identity.From(c.Request.Context())
	seen.access = identity.AccessFrom(c.Request.Context())
}

// routedEngine is engineWith for route cases: the gateway's layer in front of
// the routes named, each a method and a path, whose handler records the
// request; anything else gets the engine's own 404 and reaches only the layer.
func routedEngine(g *gateway, endpoint string, routes ...[2]string) (*gin.Engine, *arrival) {
	seen := &arrival{}
	engine := gin.New()
	engine.Use(g.middlewareFor(endpoint).Handler)
	for _, r := range routes {
		engine.Handle(r[0], r[1], func(c *gin.Context) {
			record(seen, c)
			c.Status(nethttp.StatusOK)
		})
	}
	return engine, seen
}

// conclusionOf runs a request through the gateway with a recorder behind the
// request's logger and returns the response with the one record the conclusion
// wrote. A pass writes none, so the record is returned only for a refusal.
func conclusionOf(t *testing.T, g *gateway, endpoint string, edit func(*nethttp.Request) *nethttp.Request) (*httptest.ResponseRecorder, slog.Record) {
	t.Helper()
	h := &recorder{}
	w := httptest.NewRecorder()
	engine, _ := layerOn(g, endpoint)
	engine.ServeHTTP(w, edit(recorded(h, "GET", "/orders")))
	return w, h.only(t)
}

// TestARequestWithNoUsableCredentialIsRefusedByTheGateway holds the refusals
// the gateway reaches without asking anybody.
func TestARequestWithNoUsableCredentialIsRefusedByTheGateway(t *testing.T) {
	for _, c := range []struct {
		name    string
		prepare func(*nethttp.Request) *nethttp.Request
	}{
		{"no credential at all", func(r *nethttp.Request) *nethttp.Request { return r }},
		{"an empty Authorization header", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set(authHeader, "")
			return r
		}},
		{"a blank Authorization header", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set(authHeader, "   ")
			return r
		}},
		{"two Authorization headers", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Add(authHeader, "Basic YWxpY2U6c2VjcmV0")
			r.Header.Add(authHeader, "Basic Ym9iOnNlY3JldA==")
			return r
		}},
		{"two Authorization headers of one scheme and one that nobody takes", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Add(authHeader, "Basic YWxpY2U6c2VjcmV0")
			r.Header.Add(authHeader, "Digest abc")
			return r
		}},
		{"a scheme nobody takes", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set(authHeader, "Digest an.opaque.token")
			return r
		}},
		{"a scheme taken only as a cookie name", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set(authHeader, "sid abc")
			return r
		}},
		{"a cookie nobody takes", func(r *nethttp.Request) *nethttp.Request {
			return withCookie(r, "other", "abc")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var asked atomic.Int64
			basic := passes(basicBackend("static", "static."))
			basic.asked = &asked
			cookie := passes(cookieBackend("session", "sid"))
			cookie.asked = &asked

			g := testGateway(t, []string{"public"}, basic, cookie)
			sealed(t, g, routerWith("public"))

			w, seen := run(g, "public", c.prepare(request("GET", "/orders")))

			if w.Code != nethttp.StatusUnauthorized {
				t.Errorf("the request answered %d, want %d", w.Code, nethttp.StatusUnauthorized)
			}
			if seen.reached {
				t.Error("the layer behind the gateway was reached by a request the gateway refused")
			}
			if asked.Load() != 0 {
				t.Errorf("a backend was asked %d time(s) about a request the gateway refused unread, want none", asked.Load())
			}
			if got := g.tally()["denied"]; got != 1 {
				t.Errorf("the refusal was counted %d time(s) in the denied bucket, want 1", got)
			}
			if w.Header().Get(challengeHeader) == "" {
				t.Errorf("the refusal carries no %s header", challengeHeader)
			}
		})
	}
}

// TestAGatewayRefusalIsChargedToTheGatewayAtInfo charges a gateway refusal to
// the gateway at Info.
func TestAGatewayRefusalIsChargedToTheGatewayAtInfo(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(basicBackend("static", "static.")), passes(cookieBackend("session", "sid")))
	sealed(t, g, routerWith("public"))

	for name, edit := range map[string]func(*nethttp.Request) *nethttp.Request{
		"no credential": func(r *nethttp.Request) *nethttp.Request { return r },
		"an unclaimed scheme": func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set(authHeader, "Digest x")
			return r
		},
		"a cookie twice": func(r *nethttp.Request) *nethttp.Request {
			return withCookie(withCookie(r, "sid", "a"), "sid", "b")
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, rec := conclusionOf(t, g, "public", edit)
			if w.Code != nethttp.StatusUnauthorized {
				t.Fatalf("answered %d, want %d", w.Code, nethttp.StatusUnauthorized)
			}
			if rec.Level != slog.LevelInfo {
				t.Errorf("written at %v, want %v", rec.Level, slog.LevelInfo)
			}
			if got := attrText(rec, "attributed_to"); got != ModuleName {
				t.Errorf("attributed to %q, want the gateway %q", got, ModuleName)
			}
			if got := attrText(rec, "outcome"); got != "denied" {
				t.Errorf("counted as %q, want denied", got)
			}
		})
	}
}

// TestTheCredentialsTheGatewayHandsOverAreTheCarriersAsTheyArrived hands a
// backend the carrier as it arrived, with the peer address the entry point reported.
func TestTheCredentialsTheGatewayHandsOverAreTheCarriersAsTheyArrived(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*nethttp.Request) *nethttp.Request
		want authn.Credential
	}{
		{"a header in capitals", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set(authHeader, "BEARER   the.token  ")
			return r
		}, authn.Credential{Scheme: "bearer", Value: "the.token"}},
		{"a header in mixed case", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set(authHeader, "BeArEr x.y.z")
			return r
		}, authn.Credential{Scheme: "bearer", Value: "x.y.z"}},
		{"a scheme with no value", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set(authHeader, "Bearer")
			return r
		}, authn.Credential{Scheme: "bearer", Value: ""}},
		{"a cookie", func(r *nethttp.Request) *nethttp.Request {
			return withCookie(r, "sid", "opaque-value")
		}, authn.Credential{Cookie: "sid", Value: "opaque-value"}},
		{"a cookie with an empty value", func(r *nethttp.Request) *nethttp.Request {
			return withCookie(r, "sid", "")
		}, authn.Credential{Cookie: "sid", Value: ""}},
	} {
		t.Run(c.name, func(t *testing.T) {
			tokens := passes(schemeBackend("token", "Bearer"))
			sessions := passes(cookieBackend("session", "sid"))
			g := testGateway(t, []string{"public"}, tokens, sessions)
			sealed(t, g, routerWith("public"))

			r := c.edit(request("GET", "/orders"))
			r.RemoteAddr = "203.0.113.9:4711"
			w, _ := run(g, "public", r)
			if w.Code != nethttp.StatusOK {
				t.Fatalf("the request answered %d, want %d", w.Code, nethttp.StatusOK)
			}

			handed := append(tokens.credentials(), sessions.credentials()...)
			if len(handed) != 1 {
				t.Fatalf("the backends were handed %d credential(s), want one", len(handed))
			}
			want := c.want
			want.Peer = "203.0.113.9"
			if handed[0] != want {
				t.Errorf("the backend was handed %+v, want %+v", handed[0], want)
			}
		})
	}
}

// TestACredentialReachesOnlyTheBackendThatTakesItsCarrier holds the dispatch:
// one carrier, one backend, and the other is not asked.
func TestACredentialReachesOnlyTheBackendThatTakesItsCarrier(t *testing.T) {
	var basicAsked, tokenAsked, cookieAsked atomic.Int64
	basic := passes(basicBackend("static", "static."))
	basic.asked = &basicAsked
	token := passes(schemeBackend("token", "bearer"))
	token.asked = &tokenAsked
	cookie := passes(cookieBackend("session", "sid"))
	cookie.asked = &cookieAsked

	g := testGateway(t, []string{"public"}, basic, token, cookie)
	sealed(t, g, routerWith("public"))

	run(g, "public", withBasic(request("GET", "/x"), "alice", "secret"))
	run(g, "public", withBearer(request("GET", "/x"), "t"))
	run(g, "public", withCookie(request("GET", "/x"), "sid", "v"))

	if basicAsked.Load() != 1 || tokenAsked.Load() != 1 || cookieAsked.Load() != 1 {
		t.Errorf("the backends were asked basic %d, token %d, cookie %d times, want once each",
			basicAsked.Load(), tokenAsked.Load(), cookieAsked.Load())
	}
}

// TestTheAuthorizationHeaderIsReadAloneAndDoesNotFallBackToACookie reads the
// header alone: a cookie does not answer for a header that is absent or refused.
func TestTheAuthorizationHeaderIsReadAloneAndDoesNotFallBackToACookie(t *testing.T) {
	t.Run("a header and a cookie that both hold", func(t *testing.T) {
		var tokenAsked, cookieAsked atomic.Int64
		token := passes(schemeBackend("token", "bearer"))
		token.asked = &tokenAsked
		cookie := passes(cookieBackend("session", "sid"))
		cookie.asked = &cookieAsked
		g := testGateway(t, []string{"public"}, token, cookie)
		sealed(t, g, routerWith("public"))

		w, _ := run(g, "public", withCookie(withBearer(request("GET", "/x"), "t"), "sid", "v"))

		if w.Code != nethttp.StatusOK {
			t.Fatalf("answered %d", w.Code)
		}
		if tokenAsked.Load() != 1 || cookieAsked.Load() != 0 {
			t.Errorf("the token backend was asked %d time(s) and the cookie backend %d, want 1 and 0: "+
				"the header is read alone", tokenAsked.Load(), cookieAsked.Load())
		}
	})

	t.Run("a header that does not hold beside a cookie that does", func(t *testing.T) {
		var cookieAsked atomic.Int64
		token := schemeBackend("token", "bearer")
		cookie := passes(cookieBackend("session", "sid"))
		cookie.asked = &cookieAsked
		g := testGateway(t, []string{"public"}, token, cookie)
		sealed(t, g, routerWith("public"))

		w, seen := run(g, "public", withCookie(withBearer(request("GET", "/x"), "stale"), "sid", "v"))

		if w.Code != nethttp.StatusUnauthorized || seen.reached {
			t.Errorf("answered %d (reached %v), want a refusal: a cookie must not make up for a header", w.Code, seen.reached)
		}
		if cookieAsked.Load() != 0 {
			t.Error("the cookie backend was asked after the header was refused")
		}
	})

	t.Run("a header nobody takes beside a cookie that holds", func(t *testing.T) {
		var cookieAsked atomic.Int64
		cookie := passes(cookieBackend("session", "sid"))
		cookie.asked = &cookieAsked
		g := testGateway(t, []string{"public"}, cookie)
		sealed(t, g, routerWith("public"))

		r := withCookie(request("GET", "/x"), "sid", "v")
		r.Header.Set(authHeader, "Digest abc")
		w, _ := run(g, "public", r)

		if w.Code != nethttp.StatusUnauthorized {
			t.Errorf("answered %d, want %d", w.Code, nethttp.StatusUnauthorized)
		}
		if cookieAsked.Load() != 0 {
			t.Error("the cookie backend was asked after the header was refused as one nobody takes")
		}
	})
}

// TestTwoCookiesABackendTakesAreAmbiguousAndRefused refuses an ambiguous cookie
// set rather than letting a page choose which credential is judged.
func TestTwoCookiesABackendTakesAreAmbiguousAndRefused(t *testing.T) {
	var asked atomic.Int64
	one := passes(cookieBackend("one", "sid"))
	one.asked = &asked
	two := passes(cookieBackend("two", "other"))
	two.asked = &asked
	g := testGateway(t, []string{"public"}, one, two)
	sealed(t, g, routerWith("public"))

	for name, r := range map[string]*nethttp.Request{
		"one name twice":          withCookie(withCookie(request("GET", "/x"), "sid", "a"), "sid", "b"),
		"two names, two backends": withCookie(withCookie(request("GET", "/x"), "sid", "a"), "other", "b"),
	} {
		t.Run(name, func(t *testing.T) {
			w, seen := run(g, "public", r)
			if w.Code != nethttp.StatusUnauthorized || seen.reached {
				t.Errorf("answered %d (reached %v), want a refusal", w.Code, seen.reached)
			}
		})
	}
	if asked.Load() != 0 {
		t.Errorf("a backend was asked %d time(s) about an ambiguous request", asked.Load())
	}

	for _, r := range []*nethttp.Request{
		withCookie(request("GET", "/x"), "sid", "a"), withCookie(request("GET", "/x"), "other", "b"),
	} {
		if w, _ := run(g, "public", r); w.Code != nethttp.StatusOK {
			t.Errorf("a single cookie answered %d, want %d", w.Code, nethttp.StatusOK)
		}
	}
}

// TestACredentialABrowserAttachesOnAnUnsafeRequestMustPassTheCrossOriginCheck
// applies the cross-origin check to cookie and Basic credentials on unsafe methods.
func TestACredentialABrowserAttachesOnAnUnsafeRequestMustPassTheCrossOriginCheck(t *testing.T) {
	forged := func(r *nethttp.Request) *nethttp.Request {
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Origin", "https://evil.example")
		return r
	}
	for _, c := range []struct {
		name    string
		method  string
		prepare func(*nethttp.Request) *nethttp.Request
		set     settings
		passes  bool
	}{
		{"a cross-site POST with a cookie", "POST", func(r *nethttp.Request) *nethttp.Request { return forged(withCookie(r, "sid", "v")) }, settings{}, false},
		{"a cross-site DELETE with a cookie", "DELETE", func(r *nethttp.Request) *nethttp.Request { return forged(withCookie(r, "sid", "v")) }, settings{}, false},
		{"a cross-site PUT with a cookie", "PUT", func(r *nethttp.Request) *nethttp.Request { return forged(withCookie(r, "sid", "v")) }, settings{}, false},
		{"a cross-site PATCH with a cookie", "PATCH", func(r *nethttp.Request) *nethttp.Request { return forged(withCookie(r, "sid", "v")) }, settings{}, false},
		{"a same-origin POST with a cookie", "POST", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			return withCookie(r, "sid", "v")
		}, settings{}, true},
		{"a POST with no browser headers, as a program sends it", "POST", func(r *nethttp.Request) *nethttp.Request { return withCookie(r, "sid", "v") }, settings{}, true},
		{"a cross-site GET with a cookie", "GET", func(r *nethttp.Request) *nethttp.Request { return forged(withCookie(r, "sid", "v")) }, settings{}, true},
		{"a cross-site HEAD with a cookie", "HEAD", func(r *nethttp.Request) *nethttp.Request { return forged(withCookie(r, "sid", "v")) }, settings{}, true},
		{"a cross-site POST with a Bearer credential", "POST", func(r *nethttp.Request) *nethttp.Request { return forged(withBearer(r, "t")) }, settings{}, true},
		{"a cross-site POST with a Basic credential", "POST", func(r *nethttp.Request) *nethttp.Request { return forged(withBasic(r, "static.bob", "pw")) }, settings{}, false},
		{"a cross-site PATCH with a Basic credential", "PATCH", func(r *nethttp.Request) *nethttp.Request { return forged(withBasic(r, "static.bob", "pw")) }, settings{}, false},
		{"a cross-site GET with a Basic credential", "GET", func(r *nethttp.Request) *nethttp.Request { return forged(withBasic(r, "static.bob", "pw")) }, settings{}, true},
		{"a cross-site POST with a Basic credential in capitals", "POST", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set(authHeader, "BASIC dXNlcjpwdw==")
			return forged(r)
		}, settings{}, false},
		{
			"a cross-site POST from a trusted origin", "POST", func(r *nethttp.Request) *nethttp.Request { return forged(withCookie(r, "sid", "v")) },
			settings{trustedOrigins: []string{"https://evil.example"}},
			true,
		},
		{
			"a cross-site POST from the public URL's origin", "POST", func(r *nethttp.Request) *nethttp.Request { return forged(withCookie(r, "sid", "v")) },
			settings{mount: mountConfig{PublicURL: "https://evil.example"}},
			true,
		},
		{"a same-site POST from an origin nobody trusts", "POST", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set("Sec-Fetch-Site", "same-site")
			return withCookie(r, "sid", "v")
		}, settings{}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var cookieAsked, cleared atomic.Int64
			cookie := passes(cookieBackend("session", "sid"))
			cookie.asked = &cookieAsked
			session := clearingVerifier{fakeVerifier: cookie, cleared: &cleared}
			token := passes(schemeBackend("token", "bearer"))
			var basicAsked atomic.Int64
			basic := passes(basicBackend("static", "static."))
			basic.asked = &basicAsked

			set := c.set
			set.endpoints = []string{"public"}
			set.budget, set.inFlight = time.Second, 8
			g := gatewayWith(t, set, session, token, basic)
			sealed(t, g, routerWith("public"))

			w, seen := run(g, "public", c.prepare(request(c.method, "/x")))

			if c.passes {
				if w.Code != nethttp.StatusOK {
					t.Errorf("answered %d, want %d", w.Code, nethttp.StatusOK)
				}
				return
			}
			if w.Code != nethttp.StatusUnauthorized || seen.reached {
				t.Errorf("answered %d (reached %v), want a 401 that ends the request", w.Code, seen.reached)
			}
			if cookieAsked.Load() != 0 || basicAsked.Load() != 0 {
				t.Errorf("the backend was asked about a credential from a request that failed the cross-origin check")
			}
			if cleared.Load() != 0 {
				t.Error("the cookie was cleared for a request that failed the cross-origin check, though nothing was judged about it")
			}
			if got := g.tally()["denied"]; got != 1 {
				t.Errorf("counted %d time(s) as denied, want 1", got)
			}
		})
	}
}

// TestACrossOriginRefusalIsTheGatewaysOwnAtInfo charges the refusal to the
// gateway at Info as a 401, never a 403.
func TestACrossOriginRefusalIsTheGatewaysOwnAtInfo(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(cookieBackend("session", "sid")))
	sealed(t, g, routerWith("public"))

	h := &recorder{}
	r := withCookie(recorded(h, "POST", "/x"), "sid", "v")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	engine, _ := layerOn(g, "public")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, r)

	if w.Code != nethttp.StatusUnauthorized {
		t.Fatalf("answered %d, want %d", w.Code, nethttp.StatusUnauthorized)
	}
	rec := h.only(t)
	if rec.Level != slog.LevelInfo || attrText(rec, "attributed_to") != ModuleName {
		t.Errorf("written at %v and attributed to %q, want Info and the gateway", rec.Level, attrText(rec, "attributed_to"))
	}
}

// TestAnOptionalRouteLetsARequestWithNoCredentialThroughAsAnonymous passes a
// credential-free request as anonymous, counted in a bucket of its own.
func TestAnOptionalRouteLetsARequestWithNoCredentialThroughAsAnonymous(t *testing.T) {
	var asked atomic.Int64
	basic := passes(basicBackend("static", "static."))
	basic.asked = &asked
	g := testGateway(t, []string{"public"}, basic)
	g.Optional("public", "GET", "/catalog")
	sealed(t, g, routerWith("public"))

	engine, seen := routedEngine(g, "public", [2]string{"GET", "/catalog"}, [2]string{"GET", "/orders"})
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, request("GET", "/catalog"))

	if w.Code != nethttp.StatusOK || !seen.reached {
		t.Fatalf("answered %d (reached %v), want the request let through", w.Code, seen.reached)
	}
	if seen.carried {
		t.Error("an anonymous request carries an identity")
	}
	if seen.access != authn.AccessOptional {
		t.Errorf("the access level reads %v, want %v", seen.access, authn.AccessOptional)
	}
	if asked.Load() != 0 {
		t.Error("a backend was asked about a request with no credential")
	}
	tally := g.tally()
	if tally["anonymous"] != 1 || tally["authenticated"] != 0 || tally["denied"] != 0 {
		t.Errorf("the buckets read %v, want one anonymous and nothing else", tally)
	}

	w = httptest.NewRecorder()
	engine.ServeHTTP(w, request("GET", "/orders"))
	if w.Code != nethttp.StatusUnauthorized {
		t.Errorf("a request with no credential on a guarded route answered %d, want %d", w.Code, nethttp.StatusUnauthorized)
	}
	engine2, _ := routedEngine(g, "public", [2]string{"POST", "/catalog"})
	w = httptest.NewRecorder()
	engine2.ServeHTTP(w, request("POST", "/catalog"))
	if w.Code != nethttp.StatusUnauthorized {
		t.Errorf("a POST to a path registered optional for GET answered %d, want %d", w.Code, nethttp.StatusUnauthorized)
	}
}

// TestACredentialOnAnOptionalRouteIsAuthenticatedAsAnywhereElse judges a
// credential present on an optional route, refusing one that does not hold.
func TestACredentialOnAnOptionalRouteIsAuthenticatedAsAnywhereElse(t *testing.T) {
	for _, c := range []struct {
		name    string
		prepare func(*nethttp.Request) *nethttp.Request
		verify  func(context.Context, authn.Credential) authn.Verdict
		status  int
		bucket  string
		carries bool
	}{
		{
			"a credential that holds", func(r *nethttp.Request) *nethttp.Request { return withBasic(r, "alice", "s") },
			nil, nethttp.StatusOK, "authenticated", true,
		},
		{
			"a credential that does not", func(r *nethttp.Request) *nethttp.Request { return withBasic(r, "alice", "wrong") },
			func(context.Context, authn.Credential) authn.Verdict { return authn.Denied(errors.New("no")) },
			nethttp.StatusUnauthorized, "denied", false,
		},
		{
			"a credential the backend cannot judge", func(r *nethttp.Request) *nethttp.Request { return withBasic(r, "alice", "s") },
			func(context.Context, authn.Credential) authn.Verdict { return authn.Unavailable(errors.New("down")) },
			nethttp.StatusServiceUnavailable, "unavailable", false,
		},
		{
			"a credential the backend answers nothing about", func(r *nethttp.Request) *nethttp.Request { return withBasic(r, "alice", "s") },
			func(context.Context, authn.Credential) authn.Verdict { return authn.Verdict{} },
			nethttp.StatusUnauthorized, "unspoken", false,
		},
		{"an empty Authorization header", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set(authHeader, "")
			return r
		}, nil, nethttp.StatusUnauthorized, "denied", false},
		{"a scheme nobody takes", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set(authHeader, "Digest x")
			return r
		}, nil, nethttp.StatusUnauthorized, "denied", false},
		{"two Authorization headers", func(r *nethttp.Request) *nethttp.Request {
			r.Header.Add(authHeader, "Basic YTpi")
			r.Header.Add(authHeader, "Basic YTpi")
			return r
		}, nil, nethttp.StatusUnauthorized, "denied", false},
		{
			"a cookie that is refused", func(r *nethttp.Request) *nethttp.Request { return withCookie(r, "sid", "stale") },
			nil, nethttp.StatusUnauthorized, "denied", false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			basic := passes(basicBackend("static", "static."))
			cookie := cookieBackend("session", "sid")
			if c.verify != nil {
				basic.verify = c.verify
			}
			g := testGateway(t, []string{"public"}, basic, cookie)
			g.Optional("public", "GET", "/catalog")
			sealed(t, g, routerWith("public"))

			engine, seen := routedEngine(g, "public", [2]string{"GET", "/catalog"})
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, c.prepare(request("GET", "/catalog")))

			if w.Code != c.status {
				t.Errorf("answered %d, want %d", w.Code, c.status)
			}
			if seen.carried != c.carries {
				t.Errorf("the request carries an identity: %v, want %v", seen.carried, c.carries)
			}
			if got := g.tally()[c.bucket]; got != 1 {
				t.Errorf("counted %d time(s) in %s, want 1: %v", got, c.bucket, g.tally())
			}
			if g.tally()["anonymous"] != 0 {
				t.Error("a request that carried a credential was counted as anonymous")
			}
			if c.status == nethttp.StatusOK && seen.access != authn.AccessOptional {
				t.Errorf("a credential that held on an optional route reads the access level %v, want %v",
					seen.access, authn.AccessOptional)
			}
		})
	}
}

// TestWhatABackendAnswersDecidesTheStatusAndTheBucket holds the mapping from
// what a backend says to what the caller is told and which bucket counts it.
func TestWhatABackendAnswersDecidesTheStatusAndTheBucket(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer func(*fakeVerifier) authn.Verdict
		panics bool
		status int
		bucket string
	}{
		{"the credential holds", func(f *fakeVerifier) authn.Verdict { return held(f, "alice") }, false, nethttp.StatusOK, "authenticated"},
		{"the credential does not hold", func(*fakeVerifier) authn.Verdict { return authn.Denied(errors.New("wrong password")) }, false, nethttp.StatusUnauthorized, "denied"},
		{"the backend cannot say right now", func(*fakeVerifier) authn.Verdict { return authn.Unavailable(errors.New("down")) }, false, nethttp.StatusServiceUnavailable, "unavailable"},
		{"the backend returned without answering", func(*fakeVerifier) authn.Verdict { return authn.Verdict{} }, false, nethttp.StatusUnauthorized, "unspoken"},
		{"the backend panicked", nil, true, nethttp.StatusServiceUnavailable, "unavailable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := basicBackend("static", "static.")
			b.verify = func(context.Context, authn.Credential) authn.Verdict {
				if c.panics {
					panic("the backend's store is nil")
				}
				return c.answer(b)
			}
			g := testGateway(t, []string{"public"}, b)
			sealed(t, g, routerWith("public"))

			w, _ := run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))

			if w.Code != c.status {
				t.Errorf("the request answered %d, want %d", w.Code, c.status)
			}
			if got := g.tally()[c.bucket]; got != 1 {
				t.Errorf("counted %d time(s) in the %s bucket, want 1: %v", got, c.bucket, g.tally())
			}
			total := int64(0)
			for _, n := range g.tally() {
				total += n
			}
			if total != 1 {
				t.Errorf("the conclusion was counted %d times across the buckets, want exactly once", total)
			}
		})
	}
}

// TestAPassingRequestCarriesTheIdentityTheGatewayStamped carries the principal's
// fields, except backend and challenge, which the gateway stamps itself.
func TestAPassingRequestCarriesTheIdentityTheGatewayStamped(t *testing.T) {
	b := &fakeVerifier{
		name:    "issuer",
		accepts: authn.Accepts{Schemes: []string{"bearer"}, Accounts: "", SSO: true},
	}
	provider := "0198f0aa-0000-7000-8000-000000000001"
	authTime := time.Now().Add(-time.Hour)
	b.verify = func(context.Context, authn.Credential) authn.Verdict {
		return authn.Authenticated(authn.Principal{
			Account:    "0198f0aa-0000-7000-8000-00000000000a",
			Session:    "0198f0aa-0000-7000-8000-00000000000b",
			Methods:    []authn.Method{authn.MethodOIDC, authn.MethodTOTP},
			Assurance:  authn.AssuranceMulti,
			IDPAssured: true,
			AuthTime:   authTime,
			Provider:   provider,
			Attributes: map[string]string{"idp.team": "ops"},
		})
	}
	g := testGateway(t, []string{"public"}, b)
	sealed(t, g, routerWith("public"))

	w, seen := run(g, "public", withBearer(request("GET", "/orders"), "t"))

	if w.Code != nethttp.StatusOK || !seen.carried {
		t.Fatalf("answered %d (identity carried %v), want a pass with an identity", w.Code, seen.carried)
	}
	id := seen.id
	for name, check := range map[string]bool{
		"the account":    id.Account() == "0198f0aa-0000-7000-8000-00000000000a",
		"the key":        id.Key() == "0198f0aa-0000-7000-8000-00000000000a",
		"the actor":      id.Actor() == id.Account() && !id.Impersonating(),
		"the session":    id.Session() == "0198f0aa-0000-7000-8000-00000000000b",
		"the assurance":  id.Assurance() == authn.AssuranceMulti && id.AssuranceFromIDP(),
		"the provider":   id.Provider() == provider,
		"the auth time":  id.AuthTime().Equal(authTime),
		"the backend":    id.Backend() == "issuer",
		"the challenge":  id.Challenge() == "Bearer",
		"the attributes": len(id.Attributes()) == 1 && id.Attributes()["idp.team"] == "ops",
	} {
		if !check {
			t.Errorf("%s of the identity is not what the backend handed over or the gateway stamps", name)
		}
	}
	if got := id.Methods(); len(got) != 2 || got[0] != authn.MethodOIDC || got[1] != authn.MethodTOTP {
		t.Errorf("the identity's methods are %v", got)
	}
	if seen.access != authn.AccessRequired {
		t.Errorf("the access level is %v, want %v for a guarded route with no registration", seen.access, authn.AccessRequired)
	}
	if got := g.tally()["authenticated"]; got != 1 {
		t.Errorf("the pass was counted %d time(s) as authenticated, want 1", got)
	}
}

// TestTheChallengeNameFollowsTheCarrier names Identity.Challenge after the
// carrier: the scheme's conventional capitalisation, or Cookie for a cookie.
func TestTheChallengeNameFollowsTheCarrier(t *testing.T) {
	g := testGateway(t, []string{"public"},
		passes(basicBackend("static", "static.")), passes(schemeBackend("token", "bearer", "dpop")),
		passes(cookieBackend("session", "sid")))
	sealed(t, g, routerWith("public"))

	for name, c := range map[string]struct {
		edit func(*nethttp.Request) *nethttp.Request
		want string
	}{
		"basic":  {func(r *nethttp.Request) *nethttp.Request { return withBasic(r, "a", "b") }, "Basic"},
		"bearer": {func(r *nethttp.Request) *nethttp.Request { return withBearer(r, "t") }, "Bearer"},
		"cookie": {func(r *nethttp.Request) *nethttp.Request { return withCookie(r, "sid", "v") }, "Cookie"},
		"dpop": {func(r *nethttp.Request) *nethttp.Request {
			r.Header.Set(authHeader, "dPoP x")
			return r
		}, "Dpop"},
	} {
		t.Run(name, func(t *testing.T) {
			_, seen := run(g, "public", c.edit(request("GET", "/x")))
			if !seen.carried || seen.id.Challenge() != c.want {
				t.Errorf("the challenge name reads %q, want %q", seen.id.Challenge(), c.want)
			}
		})
	}
}

// TestTheBackendAndTheChallengeAreNotTheBackendsToChoose keeps attributes from
// reaching the backend and challenge fields, which only the gateway stamps.
func TestTheBackendAndTheChallengeAreNotTheBackendsToChoose(t *testing.T) {
	b := schemeBackend("honest", "bearer")
	b.verify = func(context.Context, authn.Credential) authn.Verdict {
		p := principalOf(b, "alice")
		p.Attributes = map[string]string{"backend": "static", "challenge": "Basic"}
		return authn.Authenticated(p)
	}
	g := testGateway(t, []string{"public"}, b)
	sealed(t, g, routerWith("public"))

	_, seen := run(g, "public", withBearer(request("GET", "/x"), "t"))
	if seen.id.Backend() != "honest" || seen.id.Challenge() != "Bearer" {
		t.Errorf("the identity reads backend %q and challenge %q, want the gateway's own stamp", seen.id.Backend(), seen.id.Challenge())
	}
}

// TestAPassBindsTheCorrelationFieldsOnTheLoggerOfTheLayersBehindTheGateway
// binds the account and backend, plus the actor and session of an impersonation.
func TestAPassBindsTheCorrelationFieldsOnTheLoggerOfTheLayersBehindTheGateway(t *testing.T) {
	plain := basicBackend("static", "static.")
	plain.verify = func(context.Context, authn.Credential) authn.Verdict { return held(plain, "ops") }
	imp := &fakeVerifier{name: "imp", accepts: authn.Accepts{Schemes: []string{"bearer"}, Impersonation: true}}
	imp.verify = func(context.Context, authn.Credential) authn.Verdict {
		p := principalOf(imp, "customer-1")
		p.Actor, p.Session, p.Scope = "admin-9", "sess-1", "tenant-a"
		return authn.Authenticated(p)
	}
	g := testGateway(t, []string{"public"}, plain, imp)
	g.Impersonable("public", "GET", "/x")
	sealed(t, g, routerWith("public"))

	read := func(edit func(*nethttp.Request) *nethttp.Request) slog.Record {
		engine := gin.New()
		engine.Use(g.middlewareFor("public").Handler)
		engine.GET("/x", func(c *gin.Context) {
			log.FromContext(c.Request.Context()).Info("a record from behind the gateway")
			c.Status(nethttp.StatusOK)
		})
		h := &recorder{}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, edit(recorded(h, "GET", "/x")))
		if w.Code != nethttp.StatusOK {
			t.Fatalf("answered %d, want %d", w.Code, nethttp.StatusOK)
		}
		return h.only(t)
	}

	rec := read(func(r *nethttp.Request) *nethttp.Request { return withBasic(r, "ops", "s") })
	if got := attrText(rec, "account"); got != "static.ops" {
		t.Errorf("the account field reads %q, want static.ops", got)
	}
	if got := attrText(rec, "backend"); got != "static" {
		t.Errorf("the backend field reads %q, want static", got)
	}
	for _, absent := range []string{"actor", "impersonating", "session", "subject"} {
		if _, found := attr(rec, absent); found {
			t.Errorf("a plain pass carries the field %q", absent)
		}
	}

	rec = read(func(r *nethttp.Request) *nethttp.Request { return withBearer(r, "t") })
	if attrText(rec, "account") != "customer-1" || attrText(rec, "actor") != "admin-9" ||
		attrText(rec, "session") != "sess-1" || attrText(rec, "backend") != "imp" {
		t.Errorf("an impersonation binds account %q, actor %q, session %q, backend %q, want customer-1, admin-9, sess-1, imp",
			attrText(rec, "account"), attrText(rec, "actor"), attrText(rec, "session"), attrText(rec, "backend"))
	}
	if v, found := attr(rec, "impersonating"); !found || !v.Bool() {
		t.Error("an impersonation does not carry impersonating=true")
	}
}

// TestTheGatewaysOwnDefectBecomesAStatusCode holds the recover the gateway
// does for itself.
func TestTheGatewaysOwnDefectBecomesAStatusCode(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(basicBackend("static", "static.")))
	sealed(t, g, routerWith("public"))

	engine, seen := layerOn(g, "public")
	g.table = nil

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, withBasic(request("GET", "/orders"), "alice", "secret"))

	if w.Code != nethttp.StatusInternalServerError {
		t.Errorf("the gateway's own panic answered %d, want %d", w.Code, nethttp.StatusInternalServerError)
	}
	if got := w.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("the gateway's own panic answered with the media type %q, want the entry point's error response", got)
	}
	if seen.reached {
		t.Error("the layer behind the gateway was reached after the gateway panicked")
	}
	for bucket, count := range g.tally() {
		if count != 0 {
			t.Errorf("the gateway's own panic was counted in the %s bucket (%d), want none: those "+
				"count judgements about a credential, and this is not one", bucket, count)
		}
	}
}

// TestALayerCannotBeBuiltBeforeTheDispatchTable pins an ordering inside Init
// that has no other signal.
func TestALayerCannotBeBuiltBeforeTheDispatchTable(t *testing.T) {
	g, err := newGateway(settings{endpoints: []string{"public"}, budget: time.Second, inFlight: 1})
	if err != nil {
		t.Fatal(err)
	}

	assertPanics(t, "building a layer before the dispatch table", func() {
		g.middlewareFor("public")
	})
}

// TestTheGatewayStopsAcceptingBackendCallsWhenItIsStopped ends a request after
// Stop as an outage, without asking a backend.
func TestTheGatewayStopsAcceptingBackendCallsWhenItIsStopped(t *testing.T) {
	var asked atomic.Int64
	b := passes(basicBackend("static", "static."))
	b.asked = &asked

	g := testGateway(t, []string{"public"}, b)
	sealed(t, g, routerWith("public"))
	g.stop()

	w, _ := run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))

	if w.Code != nethttp.StatusServiceUnavailable {
		t.Errorf("a request after Stop answered %d, want %d", w.Code, nethttp.StatusServiceUnavailable)
	}
	if asked.Load() != 0 {
		t.Errorf("a backend was asked %d time(s) after Stop, want none", asked.Load())
	}
}

// TestDrainingReturnsOnceTheCallsInFlightAreDone holds the Close contract on the
// ordinary path: nothing is in flight, so there is nothing to wait for.
func TestDrainingReturnsOnceTheCallsInFlightAreDone(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(basicBackend("static", "static.")))
	sealed(t, g, routerWith("public"))
	run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))
	g.stop()

	if err := g.drain(context.Background()); err != nil {
		t.Errorf("draining with nothing in flight gave %v, want it to return", err)
	}
}

// TestAnOutageIsChargedToWhoCouldNotAnswer holds the attribution of a 503.
func TestAnOutageIsChargedToWhoCouldNotAnswer(t *testing.T) {
	t.Run("after the gateway has stopped", func(t *testing.T) {
		g := testGateway(t, []string{"public"}, passes(basicBackend("static", "static.")))
		sealed(t, g, routerWith("public"))
		g.stop()

		w, rec := conclusionOf(t, g, "public", func(r *nethttp.Request) *nethttp.Request { return withBasic(r, "alice", "s") })
		if w.Code != nethttp.StatusServiceUnavailable {
			t.Errorf("answered %d, want %d", w.Code, nethttp.StatusServiceUnavailable)
		}
		if got := attrText(rec, "attributed_to"); got != ModuleName {
			t.Errorf("the outage is charged to %q, want the gateway: the backend was never asked", got)
		}
	})

	t.Run("when a backend reports a failure", func(t *testing.T) {
		broken := basicBackend("static", "static.")
		broken.verify = func(context.Context, authn.Credential) authn.Verdict {
			return authn.Unavailable(errors.New("the store is unreachable"))
		}
		g := testGateway(t, []string{"public"}, broken)
		sealed(t, g, routerWith("public"))

		_, rec := conclusionOf(t, g, "public", func(r *nethttp.Request) *nethttp.Request { return withBasic(r, "alice", "s") })
		if got := attrText(rec, "attributed_to"); got != "static" {
			t.Errorf("the outage is charged to %q, want the backend that could not answer", got)
		}
		if rec.Level != slog.LevelWarn {
			t.Errorf("written at %v, want %v", rec.Level, slog.LevelWarn)
		}
	})
}

// TestARefusalMayCarryRetryAfterOnlyWhenTheCapacityWasInUse holds the one
// header a 503 may carry.
func TestARefusalMayCarryRetryAfterOnlyWhenTheCapacityWasInUse(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer func() authn.Verdict
		want   string
	}{
		{"the capacity was in use", func() authn.Verdict { return authn.Unavailable(authn.ErrBusy) }, "1"},
		{"the capacity was in use, wrapped", func() authn.Verdict {
			return authn.Unavailable(errors.Join(errors.New("static"), authn.ErrBusy))
		}, "1"},
		{"the backend is down", func() authn.Verdict { return authn.Unavailable(errors.New("down")) }, ""},
		{"no cause at all", func() authn.Verdict { return authn.Unavailable(nil) }, ""},
		{"a refusal that mentions the capacity", func() authn.Verdict { return authn.Denied(authn.ErrBusy) }, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := basicBackend("static", "static.")
			b.verify = func(context.Context, authn.Credential) authn.Verdict { return c.answer() }
			g := testGateway(t, []string{"public"}, b)
			sealed(t, g, routerWith("public"))

			w, _ := run(g, "public", withBasic(request("GET", "/x"), "a", "b"))
			if got := w.Header().Get("Retry-After"); got != c.want {
				t.Errorf("Retry-After reads %q, want %q", got, c.want)
			}
		})
	}

	t.Run("the gateway's own outages carry none", func(t *testing.T) {
		g := testGateway(t, []string{"public"}, passes(basicBackend("static", "static.")))
		sealed(t, g, routerWith("public"))
		g.stop()
		w, _ := run(g, "public", withBasic(request("GET", "/x"), "a", "b"))
		if w.Code != nethttp.StatusServiceUnavailable || w.Header().Get("Retry-After") != "" {
			t.Errorf("a stopped gateway answered %d with Retry-After %q, want a 503 with none", w.Code, w.Header().Get("Retry-After"))
		}
	})
}

// TestEveryResponseOnAGuardedRouteVariesByTheCredentialCarriers sets Vary to
// the credential carriers on every guarded response.
func TestEveryResponseOnAGuardedRouteVariesByTheCredentialCarriers(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(basicBackend("static", "static.")))
	g.Exempt("public", "GET", "/status")
	g.Optional("public", "GET", "/catalog")
	sealed(t, g, routerWith("public"))
	engine, _ := routedEngine(g, "public", [2]string{"GET", "/status"}, [2]string{"GET", "/catalog"}, [2]string{"GET", "/orders"})

	for name, c := range map[string]struct {
		path string
		edit func(*nethttp.Request) *nethttp.Request
		want string
	}{
		"a pass":                  {"/orders", func(r *nethttp.Request) *nethttp.Request { return withBasic(r, "a", "b") }, "Authorization, Cookie"},
		"a refusal":               {"/orders", func(r *nethttp.Request) *nethttp.Request { return r }, "Authorization, Cookie"},
		"an anonymous pass":       {"/catalog", func(r *nethttp.Request) *nethttp.Request { return r }, "Authorization, Cookie"},
		"an exempt route's reply": {"/status", func(r *nethttp.Request) *nethttp.Request { return r }, ""},
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, c.edit(request("GET", c.path)))
			if got := strings.Join(w.Header().Values("Vary"), ", "); got != c.want {
				t.Errorf("Vary reads %q, want %q", got, c.want)
			}
		})
	}
}

// TestACookieThatIsRefusedIsClearedAndNoOtherCredentialIs clears a refused
// cookie and clears nothing else.
func TestACookieThatIsRefusedIsClearedAndNoOtherCredentialIs(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer authn.Verdict
		edit   func(*nethttp.Request) *nethttp.Request
		clear  bool
	}{
		{"a cookie the backend refuses", authn.Denied(nil), func(r *nethttp.Request) *nethttp.Request { return withCookie(r, "sid", "stale") }, true},
		{"a cookie the backend cannot judge", authn.Unavailable(nil), func(r *nethttp.Request) *nethttp.Request { return withCookie(r, "sid", "v") }, false},
		{"a cookie the backend answers nothing about", authn.Verdict{}, func(r *nethttp.Request) *nethttp.Request { return withCookie(r, "sid", "v") }, false},
		{"a cookie that holds", held(cookieBackend("session", "sid"), "alice"), func(r *nethttp.Request) *nethttp.Request { return withCookie(r, "sid", "v") }, false},
		{"a header the backend refuses", authn.Denied(nil), func(r *nethttp.Request) *nethttp.Request { return withBearer(r, "stale") }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var cleared atomic.Int64
			cookie := cookieBackend("session", "sid")
			cookie.verify = func(context.Context, authn.Credential) authn.Verdict { return c.answer }
			session := clearingVerifier{fakeVerifier: cookie, cleared: &cleared}
			token := schemeBackend("token", "bearer")
			token.verify = func(context.Context, authn.Credential) authn.Verdict { return c.answer }
			g := testGateway(t, []string{"public"}, session, token)
			sealed(t, g, routerWith("public"))

			w, _ := run(g, "public", c.edit(request("GET", "/x")))

			if c.clear {
				if cleared.Load() != 1 {
					t.Fatalf("the cookie was cleared %d time(s), want once", cleared.Load())
				}
				if got := w.Header().Get("Set-Cookie"); !strings.Contains(got, "sid=") || !strings.Contains(got, "Max-Age=0") {
					t.Errorf("the response carries Set-Cookie %q, want the header that clears sid", got)
				}
			} else if cleared.Load() != 0 || w.Header().Get("Set-Cookie") != "" {
				t.Errorf("the cookie was cleared (%d) or a Set-Cookie was written (%q) where it should not be",
					cleared.Load(), w.Header().Get("Set-Cookie"))
			}
		})
	}

	t.Run("a backend that cannot clear", func(t *testing.T) {
		cookie := cookieBackend("session", "sid")
		g := testGateway(t, []string{"public"}, cookie)
		sealed(t, g, routerWith("public"))
		w, _ := run(g, "public", withCookie(request("GET", "/x"), "sid", "stale"))
		if w.Code != nethttp.StatusUnauthorized || w.Header().Get("Set-Cookie") != "" {
			t.Errorf("answered %d with Set-Cookie %q, want a plain 401", w.Code, w.Header().Get("Set-Cookie"))
		}
	})
}

// TestAPassCarriesNoChallenge holds that a request let through is not given a
// WWW-Authenticate header, which belongs to a 401.
func TestAPassCarriesNoChallenge(t *testing.T) {
	g := testGateway(t, []string{"public"}, passes(basicBackend("static", "static.")))
	sealed(t, g, routerWith("public"))

	w, _ := run(g, "public", withBasic(request("GET", "/orders"), "alice", "secret"))
	if got := w.Header().Get(challengeHeader); got != "" {
		t.Errorf("a pass carries %s %q, want none", challengeHeader, got)
	}
}
