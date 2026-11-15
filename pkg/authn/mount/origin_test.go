package mount

import (
	"net/http"
	"net/http/httptest"
	"testing"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// originVector is one Origin header value and whether the trust set of the
// configuration in originConfig holds it. The values are what a browser sends, or
// a spelling it never sends: the standard library compares the header to the set
// byte for byte, so the set is written in the browser's form and nothing else is
// in it.
type originVector struct {
	name    string
	origin  string
	trusted bool
}

// originConfig is the public URL and the trusted origins the vectors are judged
// against. Each is written in a spelling a browser does not send: upper case, the
// scheme's default port, a trailing dot.
const (
	originPublicURL = "https://App.Example.com:443"
)

var originTrusted = []string{
	"HTTP://Other.Example.com:80",
	"https://third.example.com.",
	"https://fourth.example.com:8443",
	"https://Fifth.Example.com:443",
	"http://[::1]:3000",
}

var originVectors = []originVector{
	{"the public URL as a browser sends it", "https://app.example.com", true},
	{"a trusted origin written upper case with the default http port", "http://other.example.com", true},
	{"a trusted origin written with a trailing dot", "https://third.example.com", true},
	{"a trusted origin with a port that is not the default", "https://fourth.example.com:8443", true},
	{"a trusted origin written upper case with the default https port", "https://fifth.example.com", true},
	{"a trusted IPv6 origin", "http://[::1]:3000", true},

	{"the public URL with its default port spelled out", "https://app.example.com:443", false},
	{"the public URL in upper case", "https://App.Example.com", false},
	{"a trusted origin with the trailing dot a browser does not send", "https://third.example.com.", false},
	{"the public URL on another scheme", "http://app.example.com", false},
	{"the public URL on another port", "https://app.example.com:8443", false},
	{"a trusted origin on another port", "https://fourth.example.com", false},
	{"a trusted origin on another scheme", "https://other.example.com", false},
	{"another host", "https://evil.example.com", false},
	{"the public host as a prefix of another", "https://app.example.com.evil.example", false},
	{"the public host as a suffix of another", "https://evilapp.example.com", false},
	{"a sandboxed page", "null", false},
	{"no Origin header at all", "", false},
}

// sameSitePost builds a POST a browser would send from another origin of the same
// site: the fetch metadata says same-site, and the Origin header is the vector's.
func sameSitePost(target, origin string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, target, nil)
	r.Header.Set("Sec-Fetch-Site", "same-site")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func originSettings() settings {
	var s settings
	s.Mount.Bases = defaultBases()
	s.Mount.PublicURL = originPublicURL
	s.TrustedOrigins = originTrusted
	return s
}

// TestTheTrustSetIsTheCanonicalFormOfThePublicURLAndTheTrustedOrigins holds the
// check the capability hands out against every spelling of an origin. A same-site
// request is trusted exactly when its Origin is the canonical form of one of the
// configured origins, which is what a browser sends: case, a default port and a
// trailing dot are normalised in the set and not in the header.
func TestTheTrustSetIsTheCanonicalFormOfThePublicURLAndTheTrustedOrigins(t *testing.T) {
	p, err := newProduct(originSettings())
	if err != nil {
		t.Fatalf("building the product: %v", err)
	}
	for _, v := range originVectors {
		t.Run(v.name, func(t *testing.T) {
			err := p.CSRF().Check(sameSitePost("https://app.example.com/api/v1/auth/x", v.origin))
			if v.trusted && err != nil {
				t.Errorf("a same-site request from %q was refused: %v", v.origin, err)
			}
			if !v.trusted && err == nil {
				t.Errorf("a same-site request from %q was admitted", v.origin)
			}
		})
	}
}

// TestThePublicURLIsHandedOutAsTheOriginABrowserSends holds the value the
// capability reports: the canonical origin, so that a module comparing an origin
// it was given with it compares like with like, and empty when nothing is
// configured.
func TestThePublicURLIsHandedOutAsTheOriginABrowserSends(t *testing.T) {
	for _, c := range []struct{ configured, want string }{
		{"https://App.Example.com:443", "https://app.example.com"},
		{"https://app.example.com:8443", "https://app.example.com:8443"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"https://app.example.com.", "https://app.example.com"},
		{"", ""},
	} {
		var s settings
		s.Mount.Bases = defaultBases()
		s.Mount.PublicURL = c.configured
		p, err := newProduct(s)
		if err != nil {
			t.Fatalf("building the product for %q: %v", c.configured, err)
		}
		if got := p.PublicURL(); got != c.want {
			t.Errorf("public URL %q is reported as %q, want %q", c.configured, got, c.want)
		}
	}
}

// TestACallbackBypassHoldsForTheRegisteredPatternAndNoOtherPost holds the bypass
// on the capability's own check: a cross-site POST to the callback pattern, for
// whatever value the provider segment takes, is let through, and a POST to any
// other path under the same base, a path that extends the callback's, or a
// sibling of it is not.
func TestACallbackBypassHoldsForTheRegisteredPatternAndNoOtherPost(t *testing.T) {
	p, err := newProduct(originSettings())
	if err != nil {
		t.Fatalf("building the product: %v", err)
	}
	// The bypass is written the way the module writes it for a callback; the
	// check is read directly so that the pattern is what is under test.
	p.wired.Store(&wiring{
		defaultName: "main",
		endpoints: map[speedhttp.Scope]wiredEndpoint{
			"": {name: "main", endpoint: standInEndpoint{name: "main", ledger: &ledger{}}},
		},
		gate: standInGate{ledger: &ledger{}},
	})
	p.Callback([]string{"POST"}, "/sso/:provider/callback", handler)
	p.Callback([]string{"POST"}, "/files/*rest", handler)

	crossSite := func(path string) error {
		r := httptest.NewRequest(http.MethodPost, "https://app.example.com"+path, nil)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		return p.CSRF().Check(r)
	}
	for _, path := range []string{"/api/v1/auth/sso/google/callback", "/api/v1/auth/sso/okta-2/callback", "/api/v1/auth/files/a", "/api/v1/auth/files/a/b/c"} {
		if err := crossSite(path); err != nil {
			t.Errorf("a cross-site POST to the callback %s was refused: %v", path, err)
		}
	}
	for _, path := range []string{
		"/api/v1/auth/login", "/api/v1/auth/sso/google/start", "/api/v1/auth/sso/google/callback/extra", "/api/v1/auth/sso/callback",
		"/api/v1/auth/sso/google/okta/callback", "/other/sso/google/callback", "/api/v1/auth/sso//callback", "/api/v1/auth/file/a",
	} {
		if err := crossSite(path); err == nil {
			t.Errorf("a cross-site POST to %s was admitted by a bypass that was registered for other paths", path)
		}
	}
}
