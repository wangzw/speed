package mount

import (
	nethttp "net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

const trustedSibling = "https://sibling.example.com"

// TestTheCrossOriginCheckHoldsEveryUnsafeMethodAndNoSafeOne holds the check through the real entry point and gateway on each access level.
func TestTheCrossOriginCheckHoldsEveryUnsafeMethodAndNoSafeOne(t *testing.T) {
	var calls atomic.Int64
	liveStack{
		gateItems: `"trusted-origins":["` + trustedSibling + `"]`,
		register: func(m mount.Mounter) {
			for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "GET"} {
				m.Public(method, "/pub", counted(&calls, reachedAs))
				m.Optional(method, "/opt", counted(&calls, reachedAs))
				m.Guarded(method, "/guard", counted(&calls, reachedAs))
			}
		},
	}.serve(t, func(l *live) {
		vectors := []struct {
			name    string
			headers []string
			admit   bool
		}{
			{"cross-site", []string{"Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example.com"}, false},
			{"cross-site with no Origin", []string{"Sec-Fetch-Site", "cross-site"}, false},
			{"cross-site from a trusted origin", []string{"Sec-Fetch-Site", "cross-site", "Origin", trustedSibling}, true},
			{"same-origin", []string{"Sec-Fetch-Site", "same-origin"}, true},
			{"a user-initiated navigation", []string{"Sec-Fetch-Site", "none"}, true},
			{"no fetch metadata and no Origin", nil, true},
			{"same-site, no Origin", []string{"Sec-Fetch-Site", "same-site"}, false},
			{"same-site from an untrusted origin", []string{"Sec-Fetch-Site", "same-site", "Origin", "https://other.example.com"}, false},
			{"same-site from the public URL's origin", []string{"Sec-Fetch-Site", "same-site", "Origin", defaultPublicURL}, true},
			{"same-site from a trusted origin", []string{"Sec-Fetch-Site", "same-site", "Origin", trustedSibling}, true},
			{"an Origin from elsewhere and no fetch metadata", []string{"Origin", "https://evil.example.com"}, false},
			{"a trusted Origin and no fetch metadata", []string{"Origin", trustedSibling}, true},
		}
		for _, level := range []struct {
			path string
			edit func(*nethttp.Request)
		}{
			{"/api/v1/auth/pub", nil},
			{"/api/v1/auth/opt", bearer(goodToken)},
			{"/api/v1/auth/guard", bearer(goodToken)},
		} {
			for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
				for _, v := range vectors {
					edits := []func(*nethttp.Request){headers(v.headers...)}
					if level.edit != nil {
						edits = append(edits, level.edit)
					}
					before := calls.Load()
					got := l.send("main", method, level.path, edits...)
					what := method + " " + level.path + " " + v.name
					requireNoStore(t, what, got)
					if v.admit {
						requireStatus(t, what, got, 200)
						if calls.Load() != before+1 {
							t.Errorf("%s: the handler was not called", what)
						}
						continue
					}
					requireStatus(t, what, got, 401)
					if calls.Load() != before {
						t.Errorf("%s: the handler was called although the request was refused", what)
					}
					if got.header.Get("WWW-Authenticate") == "" {
						t.Errorf("%s: the refusal carries no challenge", what)
					}
					if strings.Contains(strings.ToLower(got.header.Get("WWW-Authenticate")), "basic") {
						t.Errorf("%s: the refusal offers Basic, which would open the browser's own prompt: %q",
							what, got.header.Get("WWW-Authenticate"))
					}
				}
			}
			for _, v := range vectors {
				edits := []func(*nethttp.Request){headers(v.headers...)}
				if level.edit != nil {
					edits = append(edits, level.edit)
				}
				got := l.send("main", "GET", level.path, edits...)
				requireStatus(t, "GET "+level.path+" "+v.name, got, 200)
			}
		}
	})
}

// TestACallbackIsReachedFromAnotherSiteAndNothingElseUnderTheBaseIs holds the bypass through the real entry point.
func TestACallbackIsReachedFromAnotherSiteAndNothingElseUnderTheBaseIs(t *testing.T) {
	var callback, other atomic.Int64
	liveStack{
		register: func(m mount.Mounter) {
			m.Callback([]string{"GET", "POST"}, "/sso/:provider/callback", counted(&callback, reachedAs))
			m.Public("POST", "/sso/:provider/begin", counted(&other, reachedAs))
			m.Public("POST", "/other", counted(&other, reachedAs))
			m.Guarded("POST", "/me", counted(&other, reachedAs))
		},
	}.serve(t, func(l *live) {
		cross := headers("Sec-Fetch-Site", "cross-site", "Origin", "https://idp.example.org")
		for _, provider := range []string{"google", "okta-2"} {
			for _, method := range []string{"GET", "POST"} {
				got := l.send("main", method, "/api/v1/auth/sso/"+provider+"/callback", cross)
				requireStatus(t, method+" callback for "+provider, got, 200)
				requireNoStore(t, method+" callback for "+provider, got)
				if got.body != "nobody [open]" {
					t.Errorf("%s callback for %s answered %q, want an unauthenticated public route", method, provider, got.body)
				}
			}
		}
		if callback.Load() != 4 {
			t.Errorf("the callback handler ran %d times, want 4", callback.Load())
		}
		for _, path := range []string{"/api/v1/auth/sso/google/begin", "/api/v1/auth/other", "/api/v1/auth/me"} {
			got := l.send("main", "POST", path, cross, bearer(goodToken))
			requireStatus(t, "a cross-site POST to "+path, got, 401)
		}
		if other.Load() != 0 {
			t.Errorf("a handler outside the callback ran %d times for cross-site requests", other.Load())
		}
	})
}

// TestTheTrustSetAgreesWithTheGatewaysForEverySpellingOfAnOrigin compares the module's check with the gateway's for each vector.
func TestTheTrustSetAgreesWithTheGatewaysForEverySpellingOfAnOrigin(t *testing.T) {
	var cookieRoute atomic.Int64
	liveStack{
		publicURL: originPublicURL,
		gateItems: `"trusted-origins":[` + quoted(originTrusted) + `]`,
		backends:  []authn.Verifier{bearerBackend(), cookieBackend()},
		register:  func(m mount.Mounter) { m.Public("POST", "/pub", reachedAs) },
		hand: func(r speedhttp.Router, _ authn.Gate) {
			endpoint, err := r.Endpoint("main")
			if err != nil {
				t.Errorf("taking up the endpoint: %v", err)
				return
			}
			endpoint.Route("POST", "/hand/session", func(c *gin.Context) {
				cookieRoute.Add(1)
				c.Status(nethttp.StatusNoContent)
			})
		},
	}.serve(t, func(l *live) {
		for _, v := range originVectors {
			headersOf := []string{"Sec-Fetch-Site", "same-site"}
			if v.origin != "" {
				headersOf = append(headersOf, "Origin", v.origin)
			}
			ours := l.send("main", "POST", "/api/v1/auth/pub", headers(headersOf...))
			before := cookieRoute.Load()
			theirs := l.send("main", "POST", "/hand/session", headers(headersOf...), withCookie)
			gatewayAdmitted := cookieRoute.Load() == before+1

			if ours.status != 200 && ours.status != 401 {
				t.Errorf("%s: the module's check answered %d", v.name, ours.status)
			}
			moduleAdmitted := ours.status == 200
			if moduleAdmitted != v.trusted {
				t.Errorf("%s: the module's check admitted %v, want %v", v.name, moduleAdmitted, v.trusted)
			}
			if gatewayAdmitted != v.trusted {
				t.Errorf("%s: the gateway's check admitted %v (status %d), want %v", v.name, gatewayAdmitted, theirs.status, v.trusted)
			}
		}
	})
}

func quoted(items []string) string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = `"` + s + `"`
	}
	return strings.Join(out, ",")
}

// TestEveryResponseOnAMountedRouteCarriesNoStore holds the header on handler answers, errors and the check's refusal.
func TestEveryResponseOnAMountedRouteCarriesNoStore(t *testing.T) {
	liveStack{
		register: func(m mount.Mounter) {
			m.Public("GET", "/ok", reachedAs)
			m.Public("GET", "/bad", func(c *gin.Context) { c.String(nethttp.StatusBadRequest, "no") })
			m.Public("GET", "/gone", func(c *gin.Context) { c.Status(nethttp.StatusNoContent) })
			m.Public("GET", "/own", func(c *gin.Context) {
				c.Header("Cache-Control", "private, max-age=60")
				c.Status(nethttp.StatusOK)
			})
			m.Optional("GET", "/opt", reachedAs)
			m.Guarded("GET", "/guard", reachedAs)
			m.Public("POST", "/post", reachedAs)
			m.Callback([]string{"GET"}, "/cb/:provider", reachedAs)
		},
	}.serve(t, func(l *live) {
		for _, c := range []struct {
			name, method, path string
			edits              []func(*nethttp.Request)
			status             int
		}{
			{"a 200", "GET", "/api/v1/auth/ok", nil, 200},
			{"an error the handler writes", "GET", "/api/v1/auth/bad", nil, 400},
			{"a 204", "GET", "/api/v1/auth/gone", nil, 204},
			{"an optional route", "GET", "/api/v1/auth/opt", nil, 200},
			{"a guarded route with a credential", "GET", "/api/v1/auth/guard", []func(*nethttp.Request){bearer(goodToken)}, 200},
			{
				"the refusal of the cross-origin check", "POST", "/api/v1/auth/post",
				[]func(*nethttp.Request){headers("Sec-Fetch-Site", "cross-site")},
				401,
			},
			{"a callback", "GET", "/api/v1/auth/cb/x", nil, 200},
		} {
			got := l.send("main", c.method, c.path, c.edits...)
			requireStatus(t, c.name, got, c.status)
			requireNoStore(t, c.name, got)
		}
		got := l.send("main", "GET", "/api/v1/auth/own")
		if v := got.header.Get("Cache-Control"); v != "private, max-age=60" {
			t.Errorf("a handler that set its own Cache-Control got %q, want its value to stand", v)
		}
	})
}
