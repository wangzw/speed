package mounttest_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
)

// seen is what a handler read out of its request, which is what a module's test
// asserts about.
type seen struct {
	calls   int
	id      authn.Identity
	has     bool
	access  authn.Access
	account authn.AccountID
}

func record(s *seen) gin.HandlerFunc {
	return func(c *gin.Context) {
		s.calls++
		s.id, s.has = authn.FromContext(c.Request.Context())
		s.access = authn.AccessFromContext(c.Request.Context())
		if s.has {
			s.account = s.id.Account()
		}
		c.String(http.StatusOK, "ran")
	}
}

func get(m *mounttest.Mounter, path string, hdr ...string) *httptest.ResponseRecorder {
	return send(m, http.MethodGet, path, hdr...)
}

func send(m *mounttest.Mounter, method, path string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	return m.Do(r)
}

// TestTheRegistrationsAreRecordedWithTheirKindAndTheImpersonableMark holds what
// a module's test reads back: for each Mounter method, the kind, the methods,
// the full path and the mark.
func TestTheRegistrationsAreRecordedWithTheirKindAndTheImpersonableMark(t *testing.T) {
	m := mounttest.New(t)
	h := func(*gin.Context) {}
	m.Public(http.MethodGet, "/methods", h)
	m.Optional(http.MethodGet, "/session", h, mount.Impersonable())
	m.Guarded(http.MethodPost, "/logout", h, mount.Impersonable())
	m.Guarded(http.MethodDelete, "/sessions/:id", h)
	m.Callback([]string{http.MethodGet, http.MethodPost}, "/callback/:provider", h)

	want := []mounttest.Registration{
		{Kind: mounttest.Public, Methods: []string{"GET"}, Path: "/api/v1/auth/methods"},
		{Kind: mounttest.Optional, Methods: []string{"GET"}, Path: "/api/v1/auth/session", Impersonable: true},
		{Kind: mounttest.Guarded, Methods: []string{"POST"}, Path: "/api/v1/auth/logout", Impersonable: true},
		{Kind: mounttest.Guarded, Methods: []string{"DELETE"}, Path: "/api/v1/auth/sessions/:id"},
		{Kind: mounttest.Callback, Methods: []string{"GET", "POST"}, Path: "/api/v1/auth/callback/:provider"},
	}
	got := m.Registrations()
	if len(got) != len(want) {
		t.Fatalf("Registrations() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Kind != want[i].Kind || got[i].Path != want[i].Path || got[i].Impersonable != want[i].Impersonable ||
			!slices.Equal(got[i].Methods, want[i].Methods) {
			t.Errorf("registration %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	if r, ok := m.Registered(http.MethodPost, "/api/v1/auth/callback/:provider"); !ok || r.Kind != mounttest.Callback {
		t.Errorf("Registered(POST, callback) = %+v, %v", r, ok)
	}
	if _, ok := m.Registered(http.MethodPut, "/api/v1/auth/callback/:provider"); ok {
		t.Error("Registered finds a method the callback was not registered with")
	}
	if _, ok := m.Registered(http.MethodGet, "/api/v1/auth/missing"); ok {
		t.Error("Registered finds a route nobody registered")
	}

	// What Registrations returns is a copy: a test that edits it cannot
	// change what the next read says.
	got[0].Methods[0] = "PATCH"
	got[0].Kind = mounttest.Guarded
	if again := m.Registrations(); again[0].Kind != mounttest.Public || again[0].Methods[0] != "GET" {
		t.Errorf("a write to the returned slice changed the record: %+v", again[0])
	}
}

// TestTheMounterReportsItsBaseOriginAndEndpoint holds the three reads a handler
// makes of its Mounter and that each option changes its own.
func TestTheMounterReportsItsBaseOriginAndEndpoint(t *testing.T) {
	m := mounttest.New(t)
	if m.Base() != mounttest.DefaultBase || m.PublicURL() != mounttest.DefaultPublicURL ||
		m.Endpoint() != mounttest.DefaultEndpoint || m.CSRF() == nil {
		t.Errorf("defaults: base %q, public URL %q, endpoint %q, csrf %v", m.Base(), m.PublicURL(), m.Endpoint(), m.CSRF())
	}
	c := mounttest.New(t, mounttest.WithBase("/identity"), mounttest.WithPublicURL("https://id.example.org"), mounttest.WithEndpoint("public"))
	if c.Base() != "/identity" || c.PublicURL() != "https://id.example.org" || c.Endpoint() != "public" {
		t.Errorf("options: base %q, public URL %q, endpoint %q", c.Base(), c.PublicURL(), c.Endpoint())
	}
	c.Public(http.MethodGet, "/x", func(g *gin.Context) { g.String(http.StatusOK, "ok") })
	if rec := get(c, "/identity/x"); rec.Code != http.StatusOK {
		t.Errorf("a route under the configured base answered %d", rec.Code)
	}
	if rec := get(c, "/api/v1/auth/x"); rec.Code != http.StatusNotFound {
		t.Errorf("the default base still answers: %d", rec.Code)
	}
}

// TestAPublicRouteCarriesNoIdentityAndTheOpenLevel holds that a registration
// made Public is read as exempt by the handler, whoever the test chose to be: the
// gateway reads no credential on such a route.
func TestAPublicRouteCarriesNoIdentityAndTheOpenLevel(t *testing.T) {
	m := mounttest.New(t)
	var s seen
	m.Public(http.MethodGet, "/methods", record(&s))
	m.As(authntest.WithAccount("alice"))
	if rec := get(m, "/api/v1/auth/methods"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if s.has || s.access != authn.AccessOpen {
		t.Errorf("a public route saw identity %v at access %d, want none at AccessOpen", s.has, s.access)
	}
}

// TestAnOptionalRouteCarriesTheChosenIdentityOrNone holds both readings of a
// route that accepts no credential: the access level is AccessOptional either
// way, and the identity is the one the test chose.
func TestAnOptionalRouteCarriesTheChosenIdentityOrNone(t *testing.T) {
	m := mounttest.New(t)
	var s seen
	m.Optional(http.MethodGet, "/session", record(&s))

	if rec := get(m, "/api/v1/auth/session"); rec.Code != http.StatusOK {
		t.Fatalf("anonymous status = %d", rec.Code)
	}
	if s.has || s.access != authn.AccessOptional {
		t.Errorf("anonymous: identity %v, access %d, want none at AccessOptional", s.has, s.access)
	}

	m.As(authntest.WithAccount("alice"), authntest.WithAssurance(authn.AssuranceMulti))
	get(m, "/api/v1/auth/session")
	if !s.has || s.account != "alice" || s.id.Assurance() != authn.AssuranceMulti || s.access != authn.AccessOptional {
		t.Errorf("with an identity: has %v, account %q, assurance %d, access %d", s.has, s.account, s.id.Assurance(), s.access)
	}

	m.AsAnonymous()
	get(m, "/api/v1/auth/session")
	if s.has {
		t.Error("AsAnonymous left the identity in place")
	}
}

// TestAGuardedRouteNeedsTheIdentityTheTestChose holds that without an identity a
// guarded route is refused as the gateway refuses it, with the handler never
// reached, and with one the handler reads it at AccessRequired.
func TestAGuardedRouteNeedsTheIdentityTheTestChose(t *testing.T) {
	m := mounttest.New(t, mounttest.WithEndpoint("main"))
	var s seen
	m.Guarded(http.MethodGet, "/sessions", record(&s))

	rec := get(m, "/api/v1/auth/sessions")
	if rec.Code != http.StatusUnauthorized || s.calls != 0 {
		t.Fatalf("no identity: status %d, handler ran %d times, want 401 and none", rec.Code, s.calls)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="main", Cookie realm="main"` {
		t.Errorf("WWW-Authenticate = %q", got)
	}

	m.As(authntest.WithAccount("alice"))
	if rec := get(m, "/api/v1/auth/sessions"); rec.Code != http.StatusOK {
		t.Fatalf("with an identity: status = %d", rec.Code)
	}
	if !s.has || s.account != "alice" || s.access != authn.AccessRequired {
		t.Errorf("handler read has %v, account %q, access %d, want alice at AccessRequired", s.has, s.account, s.access)
	}

	// The identity is set per Mounter and read per request: changing it changes
	// the next request and not the ones already served.
	m.As(authntest.WithAccount("bob"))
	get(m, "/api/v1/auth/sessions")
	if s.account != "bob" {
		t.Errorf("after As(bob) the handler read %q", s.account)
	}
}

// TestAnImpersonatingIdentityReachesOnlyTheRoutesMarkedForIt holds the gateway's
// rule that the stand-in applies: an identity whose actor is not its account is
// refused on a route that is not Impersonable and admitted on one that is.
func TestAnImpersonatingIdentityReachesOnlyTheRoutesMarkedForIt(t *testing.T) {
	m := mounttest.New(t)
	var plain, marked seen
	m.Guarded(http.MethodGet, "/sessions", record(&plain))
	m.Guarded(http.MethodGet, "/session", record(&marked), mount.Impersonable())
	m.Optional(http.MethodGet, "/me", record(&plain))
	m.As(authntest.WithAccount("victim"), authntest.WithActor("admin"), authntest.WithSession("sess-1"), authntest.WithScope("acme"))

	if rec := get(m, "/api/v1/auth/sessions"); rec.Code != http.StatusUnauthorized {
		t.Errorf("a guarded route that is not impersonable answered %d, want 401", rec.Code)
	}
	if rec := get(m, "/api/v1/auth/me"); rec.Code != http.StatusUnauthorized {
		t.Errorf("an optional route that is not impersonable answered %d, want 401", rec.Code)
	}
	if plain.calls != 0 {
		t.Errorf("the handlers of the refused routes ran %d times", plain.calls)
	}
	if rec := get(m, "/api/v1/auth/session"); rec.Code != http.StatusOK || !marked.id.Impersonating() {
		t.Errorf("the impersonable route answered %d, impersonating = %v", rec.Code, marked.id.Impersonating())
	}
	if marked.id.Account() != "victim" || marked.id.Actor() != "admin" {
		t.Errorf("the handler read account %q, actor %q", marked.id.Account(), marked.id.Actor())
	}
}

// TestTheWrapperIsTheProductionOne holds that what a registration runs through is
// mount.Wrap: no-store on the response, the cross-origin check before the
// handler, and the callback's bypass, each as in production.
func TestTheWrapperIsTheProductionOne(t *testing.T) {
	m := mounttest.New(t)
	var public, guarded, callback seen
	m.Public(http.MethodPost, "/flows", record(&public))
	m.Guarded(http.MethodPost, "/logout", record(&guarded))
	m.Callback([]string{http.MethodPost}, "/callback/:provider", record(&callback))
	m.As(authntest.WithAccount("alice"))

	cross := []string{"Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example"}
	if rec := send(m, http.MethodPost, "/api/v1/auth/flows", cross...); rec.Code != http.StatusUnauthorized || public.calls != 0 {
		t.Errorf("a cross-site POST to a public route: status %d, handler ran %d times", rec.Code, public.calls)
	}
	if rec := send(m, http.MethodPost, "/api/v1/auth/logout", cross...); rec.Code != http.StatusUnauthorized || guarded.calls != 0 {
		t.Errorf("a cross-site POST to a guarded route: status %d, handler ran %d times", rec.Code, guarded.calls)
	}
	if rec := send(m, http.MethodPost, "/api/v1/auth/callback/google", cross...); rec.Code != http.StatusOK || callback.calls != 1 {
		t.Errorf("a cross-site POST to the callback: status %d, handler ran %d times", rec.Code, callback.calls)
	}
	same := send(m, http.MethodPost, "/api/v1/auth/logout", "Sec-Fetch-Site", "same-origin")
	if same.Code != http.StatusOK || guarded.calls != 1 {
		t.Errorf("a same-origin POST: status %d, handler ran %d times", same.Code, guarded.calls)
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"a served response":  same,
		"a refused response": send(m, http.MethodPost, "/api/v1/auth/flows", cross...),
	} {
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", name, got)
		}
	}
}

// TestTheTrustedOriginsAreTheOptionsOwn holds that the public URL and the trusted
// origins of the Mounter are what its check trusts, so a test of a handler that
// depends on a trusted origin can say which.
func TestTheTrustedOriginsAreTheOptionsOwn(t *testing.T) {
	m := mounttest.New(t, mounttest.WithPublicURL("https://id.example.org"), mounttest.WithTrustedOrigins("https://partner.example.net"))
	m.Public(http.MethodPost, "/flows", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for _, c := range []struct {
		origin string
		want   int
	}{
		{"https://id.example.org", 204},
		{"https://partner.example.net", 204},
		{mounttest.DefaultPublicURL, 401},
		{"https://evil.example", 401},
	} {
		rec := send(m, http.MethodPost, "/api/v1/auth/flows", "Sec-Fetch-Site", "same-site", "Origin", c.origin)
		if rec.Code != c.want {
			t.Errorf("a same-site POST from %s: status = %d, want %d", c.origin, rec.Code, c.want)
		}
	}
}

// TestAHandlerThatPanicsEndsInA500 holds that the stand-in survives a handler's
// panic, so a test that drives one gets a response instead of the panic ending the
// test. The panic's own response belongs to the entry point's fallback, not to the
// stand-in, whose default shape drops the representation headers, so its headers
// are not held here.
func TestAHandlerThatPanicsEndsInA500(t *testing.T) {
	m := mounttest.New(t)
	m.Public(http.MethodGet, "/boom", func(*gin.Context) { panic("boom") })
	if rec := get(m, "/api/v1/auth/boom"); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// TestARouteIsReachableOverTheLoopback holds that the Mounter is an http.Handler
// a real server can serve: a test that needs real connections, cookies set and
// read by a client, drives its routes through it.
func TestARouteIsReachableOverTheLoopback(t *testing.T) {
	m := mounttest.New(t)
	m.Public(http.MethodGet, "/hello", func(c *gin.Context) {
		http.SetCookie(c.Writer, &http.Cookie{Name: "sid", Value: "v", Path: "/"})
		c.String(http.StatusOK, "hello")
	})
	srv := httptest.NewServer(m)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/v1/auth/hello")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "hello" || len(resp.Cookies()) != 1 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("status %d, body %q, %d cookies, Cache-Control %q", resp.StatusCode, body, len(resp.Cookies()), resp.Header.Get("Cache-Control"))
	}
	if m.Engine() == nil {
		t.Error("Engine() is nil")
	}
}

// TestAConflictingRegistrationPanicsAsTheRouterDoes holds the registration
// convention the module follows: a route registered twice is a defect at the call
// site and panics.
func TestAConflictingRegistrationPanicsAsTheRouterDoes(t *testing.T) {
	m := mounttest.New(t)
	m.Public(http.MethodGet, "/x", func(*gin.Context) {})
	defer func() {
		if recover() == nil {
			t.Error("a second registration of the same route did not panic")
		}
	}()
	m.Guarded(http.MethodGet, "/x", func(*gin.Context) {})
}

// refuses stands in for the *testing.T handed to New, so that the refusal of an
// origin can be observed without failing the case that provokes it.
type refuses struct {
	testing.TB
	failed bool
	text   string
}

func (*refuses) Helper() {}
func (r *refuses) Fatalf(format string, args ...any) {
	r.failed = true
	r.text = fmt.Sprintf(format, args...)
}

// TestAnOriginTheCheckCannotTrustFailsTheTest holds that a public URL or a
// trusted origin that is not an origin is a failure of the test that gave it, not
// a Mounter that trusts nothing without saying so.
func TestAnOriginTheCheckCannotTrustFailsTheTest(t *testing.T) {
	for name, opt := range map[string]mounttest.Option{
		"a public URL with a scheme the check cannot use": mounttest.WithPublicURL("ftp://x.example"),
		"a trusted origin with a path":                    mounttest.WithTrustedOrigins("https://x.example/app"),
		"a wildcard origin":                               mounttest.WithTrustedOrigins("https://*.example.com"),
	} {
		r := &refuses{TB: t}
		mounttest.New(r, opt)
		if !r.failed {
			t.Errorf("%s did not fail the test", name)
		}
		if strings.Contains(r.text, "x.example") {
			t.Errorf("the failure text %q echoes the origin", r.text)
		}
	}
}
