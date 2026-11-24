package mount

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const wrapPublicURL = "https://app.example.com"

// wrapped is an engine whose routes go through Wrap; calls counts handler runs
// and panickedHeader is the Cache-Control the recovery read during a panic.
type wrapped struct {
	engine         *gin.Engine
	calls          map[string]int
	csrf           *http.CrossOriginProtection
	panickedHeader string
}

func newWrapped(t *testing.T, trusted ...string) *wrapped {
	t.Helper()
	csrf, err := CrossOrigin(wrapPublicURL, trusted)
	if err != nil {
		t.Fatal(err)
	}
	w := &wrapped{engine: gin.New(), calls: map[string]int{}, csrf: csrf}
	// The recovery reads the header the wrapper set while the handler ran: the
	// fallback's default shape drops the representation headers, Cache-Control
	// among them, so it is not on the response this recovery produces.
	w.engine.Use(gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
		w.panickedHeader = c.Writer.Header().Get("Cache-Control")
		c.AbortWithStatus(http.StatusInternalServerError)
	}))
	return w
}

func (w *wrapped) add(method, path string) {
	w.engine.Handle(method, path, Wrap(w.csrf, "main", func(c *gin.Context) {
		w.calls[method+" "+path]++
		c.String(http.StatusOK, "ran")
	}))
}

func (w *wrapped) do(method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	w.engine.ServeHTTP(rec, r)
	return rec
}

// TestEveryResponseCarriesNoStore checks every response kind the wrapper produces
// or lets through carries Cache-Control: no-store.
func TestEveryResponseCarriesNoStore(t *testing.T) {
	w := newWrapped(t)
	w.engine.GET("/ok", Wrap(w.csrf, "main", func(c *gin.Context) { c.String(http.StatusOK, "x") }))
	w.engine.GET("/fail", Wrap(w.csrf, "main", func(c *gin.Context) { Fail(c, CodeBadRequest) }))
	w.engine.GET("/bare", Wrap(w.csrf, "main", func(c *gin.Context) { c.Status(http.StatusNoContent) }))
	w.add(http.MethodPost, "/post")

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"a 200":                  w.do(http.MethodGet, "/ok", nil),
		"a problem":              w.do(http.MethodGet, "/fail", nil),
		"a 204":                  w.do(http.MethodGet, "/bare", nil),
		"a refused cross-origin": w.do(http.MethodPost, "/post", map[string]string{"Sec-Fetch-Site": "cross-site"}),
	} {
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s (status %d): Cache-Control = %q, want no-store", name, rec.Code, got)
		}
	}
}

// TestAHandlerThatPanickedRanWithTheHeaderOn checks the header is set before the
// handler and survives a panic.
func TestAHandlerThatPanickedRanWithTheHeaderOn(t *testing.T) {
	w := newWrapped(t)
	w.engine.GET("/panic", Wrap(w.csrf, "main", func(*gin.Context) { panic("boom") }))
	rec := w.do(http.MethodGet, "/panic", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("the panic ended in %d, want 500", rec.Code)
	}
	if w.panickedHeader != "no-store" {
		t.Errorf("while the handler ran and panicked Cache-Control = %q, want no-store", w.panickedHeader)
	}
}

// TestAHandlerMayReplaceTheCacheHeaderItself holds that the header is a default
// the handler may overwrite: the key set is public and cacheable, and says so.
func TestAHandlerMayReplaceTheCacheHeaderItself(t *testing.T) {
	w := newWrapped(t)
	w.engine.GET("/jwks", Wrap(w.csrf, "main", func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=300")
		c.String(http.StatusOK, "{}")
	}))
	if got := w.do(http.MethodGet, "/jwks", nil).Header().Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("Cache-Control = %q, want the handler's own", got)
	}
}

// TestACrossSiteUnsafeRequestIsRefusedBeforeTheHandler checks each unsafe method
// is refused with a 401 challenge before the handler runs.
func TestACrossSiteUnsafeRequestIsRefusedBeforeTheHandler(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		w := newWrapped(t)
		w.add(method, "/r")
		rec := w.do(method, "/r", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", method, rec.Code)
		}
		if w.calls[method+" /r"] != 0 {
			t.Errorf("%s: the handler ran %d times for a cross-site request", method, w.calls[method+" /r"])
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="main", Cookie realm="main"` {
			t.Errorf("%s: WWW-Authenticate = %q", method, got)
		}
		if strings.Contains(rec.Body.String(), "ran") {
			t.Errorf("%s: the body %q is the handler's", method, rec.Body)
		}
	}
}

// TestTheCrossOriginVectors checks the standard check's cases as the wrapper uses
// it.
func TestTheCrossOriginVectors(t *testing.T) {
	trusted := "https://admin.example.com"
	for _, c := range []struct {
		name   string
		method string
		hdr    map[string]string
		want   int
	}{
		{"same-origin", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-origin"}, 200},
		{"none (a navigation the user typed)", http.MethodPost, map[string]string{"Sec-Fetch-Site": "none"}, 200},
		{"no header at all (not a browser)", http.MethodPost, nil, 200},
		{"cross-site", http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site"}, 401},
		{"same-site is another origin", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-site"}, 401},
		{"same-site with an origin that is not trusted", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "https://evil.example.com"}, 401},
		{"same-site from a trusted origin", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-site", "Origin": trusted}, 200},
		{"cross-site from a trusted origin", http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": trusted}, 200},
		{"same-site from the public URL's own origin", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-site", "Origin": wrapPublicURL}, 200},
		{"a trusted origin written with its default port", http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": trusted + ":443"}, 401},
		{"an unknown Sec-Fetch-Site value", http.MethodPost, map[string]string{"Sec-Fetch-Site": "Cross-Site"}, 401},
		{"an empty-looking one", http.MethodPost, map[string]string{"Sec-Fetch-Site": " "}, 401},
		{"no fetch metadata, an origin that is the host", http.MethodPost, map[string]string{"Origin": "http://example.com"}, 200},
		{"no fetch metadata, an origin that is not the host", http.MethodPost, map[string]string{"Origin": "https://evil.example"}, 401},
		{"no fetch metadata, a trusted origin", http.MethodPost, map[string]string{"Origin": trusted}, 200},
		{"a safe method is not checked", http.MethodGet, map[string]string{"Sec-Fetch-Site": "cross-site"}, 200},
		{"HEAD is not checked", http.MethodHead, map[string]string{"Sec-Fetch-Site": "cross-site"}, 200},
		{"a cross-site PUT", http.MethodPut, map[string]string{"Sec-Fetch-Site": "cross-site"}, 401},
		{"a cross-site PATCH", http.MethodPatch, map[string]string{"Sec-Fetch-Site": "cross-site"}, 401},
		{"a cross-site DELETE", http.MethodDelete, map[string]string{"Sec-Fetch-Site": "cross-site"}, 401},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWrapped(t, trusted)
			w.add(c.method, "/r")
			rec := w.do(c.method, "/r", c.hdr)
			if rec.Code != c.want {
				t.Errorf("status = %d, want %d", rec.Code, c.want)
			}
			if ran := w.calls[c.method+" /r"] > 0; ran != (c.want == 200) {
				t.Errorf("the handler ran = %v, want %v", ran, c.want == 200)
			}
		})
	}
}

// TestACallbackBypassHoldsForTheRegisteredPatternAlone checks only the registered
// method and path bypass the check.
func TestACallbackBypassHoldsForTheRegisteredPatternAlone(t *testing.T) {
	w := newWrapped(t)
	AllowCallback(w.csrf, []string{http.MethodPost}, "/api/v1/auth/callback/:provider")
	for _, route := range [][2]string{
		{http.MethodPost, "/api/v1/auth/callback/:provider"},
		{http.MethodGet, "/api/v1/auth/callback/:provider"},
		{http.MethodPut, "/api/v1/auth/callback/:provider"},
		{http.MethodPost, "/api/v1/auth/flows"},
		{http.MethodPost, "/api/v1/auth/callback/:provider/extra"},
		{http.MethodPost, "/api/v1/auth/callback"},
	} {
		w.add(route[0], route[1])
	}
	cross := map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://idp.example"}

	for _, c := range []struct {
		name         string
		method, path string
		want         int
	}{
		{"the registered path with the registered method", http.MethodPost, "/api/v1/auth/callback/google", 200},
		{"the same with a different provider segment", http.MethodPost, "/api/v1/auth/callback/0198f0aa-0000-7000-8000-000000000001", 200},
		{"the registered path with a method that was not registered", http.MethodPut, "/api/v1/auth/callback/google", 401},
		{"another route of the same Mounter", http.MethodPost, "/api/v1/auth/flows", 401},
		{"a longer path under the pattern", http.MethodPost, "/api/v1/auth/callback/google/extra", 401},
		{"the parent of the pattern", http.MethodPost, "/api/v1/auth/callback", 401},
		{"a safe method needs no bypass", http.MethodGet, "/api/v1/auth/callback/google", 200},
	} {
		t.Run(c.name, func(t *testing.T) {
			if rec := w.do(c.method, c.path, cross); rec.Code != c.want {
				t.Errorf("%s %s: status = %d, want %d", c.method, c.path, rec.Code, c.want)
			}
		})
	}

	// A path that is not clean never reaches a route of the entry point, but the
	// check itself must not take it for the pattern either: cleaned, it is another
	// route.
	for _, path := range []string{"/api/v1/auth/callback/../flows", "/api/v1/auth//callback/google/../../flows", "/api/v1/auth/callback/google/.."} {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.URL.Path = path
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		if err := w.csrf.Check(r); err == nil {
			t.Errorf("the check lets the unclean path %q through on the callback's bypass", path)
		}
	}

	other := newWrapped(t)
	other.add(http.MethodPost, "/api/v1/auth/callback/:provider")
	if rec := other.do(http.MethodPost, "/api/v1/auth/callback/google", cross); rec.Code != http.StatusUnauthorized {
		t.Errorf("a second instance of the check let the callback through: status = %d, want 401", rec.Code)
	}
}

// TestACallbackPatternReadsTheEntryPointsWildcards checks a route's wildcards
// translate to the form the check matches.
func TestACallbackPatternReadsTheEntryPointsWildcards(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"/api/v1/auth/callback/:provider", "/api/v1/auth/callback/{provider}"},
		{"/a/:x/b/:y", "/a/{x}/b/{y}"},
		{"/files/*rest", "/files/{rest...}"},
		{"/plain", "/plain"},
		{"/", "/"},
	} {
		if got := servePattern(c.in); got != c.want {
			t.Errorf("servePattern(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	w := newWrapped(t)
	AllowCallback(w.csrf, []string{http.MethodPost}, "/files/*rest")
	w.add(http.MethodPost, "/files/*rest")
	cross := map[string]string{"Sec-Fetch-Site": "cross-site"}
	if rec := w.do(http.MethodPost, "/files/a/b/c", cross); rec.Code != http.StatusOK {
		t.Errorf("a catch-all callback refused a deep path: status = %d", rec.Code)
	}
}

// TestAWrapperWithNothingToCheckWithPanics holds that a wrapper that would check
// nothing is not built: it would read as a check.
func TestAWrapperWithNothingToCheckWithPanics(t *testing.T) {
	csrf, _ := CrossOrigin(wrapPublicURL, nil)
	h := func(*gin.Context) {}
	for name, f := range map[string]func(){
		"no check":   func() { Wrap(nil, "r", h) },
		"no handler": func() { Wrap(csrf, "r", nil) },
	} {
		if msg := mustPanic(t, name, f); !strings.HasPrefix(msg, "authn/mount: ") {
			t.Errorf("%s panicked with %q", name, msg)
		}
	}
}
