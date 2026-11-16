package session

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/gate"
)

// The session cookie's three names, written out so that the tests pin the
// strings a browser sees.
const (
	hostCookieName   = "__Host-authn-sid"
	secureCookieName = "__Secure-authn-sid"
	bareCookieName   = "authn-sid"
)

// The cookie's name and its attributes are what the deployment's public address
// and the two configuration items come to. Each branch is asserted on its own,
// with the address that selects it.
func TestTheCookieNameFollowsThePublicAddressAndTheTwoItems(t *testing.T) {
	for name, c := range map[string]struct {
		cfg      moduleConfig
		gate     gateMount
		want     cookieSpec
		wantName string
		secure   bool
	}{
		"an https host": {
			cfg:      moduleConfig{SameSite: "lax"},
			gate:     gateMount{PublicURL: "https://app.example.com"},
			want:     cookieSpec{publicURL: "https://app.example.com", sameSite: http.SameSiteLaxMode},
			wantName: hostCookieName, secure: true,
		},
		"https on localhost keeps the prefix": {
			cfg:      moduleConfig{SameSite: "lax"},
			gate:     gateMount{PublicURL: "https://localhost:8443"},
			want:     cookieSpec{publicURL: "https://localhost:8443", sameSite: http.SameSiteLaxMode},
			wantName: hostCookieName, secure: true,
		},
		"localhost over http carries no prefix": {
			cfg:      moduleConfig{SameSite: "strict"},
			gate:     gateMount{PublicURL: "http://localhost:8080"},
			want:     cookieSpec{publicURL: "http://localhost:8080", sameSite: http.SameSiteStrictMode},
			wantName: bareCookieName,
		},
		"an IPv4 loopback address over http carries no prefix": {
			cfg:      moduleConfig{SameSite: "lax"},
			gate:     gateMount{PublicURL: "http://127.0.0.1"},
			want:     cookieSpec{publicURL: "http://127.0.0.1", sameSite: http.SameSiteLaxMode},
			wantName: bareCookieName,
		},
		"the IPv6 loopback address over http carries no prefix": {
			cfg:      moduleConfig{SameSite: "lax"},
			gate:     gateMount{PublicURL: "http://[::1]:8080"},
			want:     cookieSpec{publicURL: "http://[::1]:8080", sameSite: http.SameSiteLaxMode},
			wantName: bareCookieName,
		},
		"a cookie domain drops the host-only prefix and writes Domain": {
			cfg:      moduleConfig{SameSite: "lax", CookieDomain: ".example.com"},
			gate:     gateMount{PublicURL: "https://app.example.com"},
			want:     cookieSpec{publicURL: "https://app.example.com", domain: "example.com", sameSite: http.SameSiteLaxMode},
			wantName: secureCookieName, secure: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := c.cfg.resolve(c.gate)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got != c.want {
				t.Errorf("cookie = %+v, want %+v", got, c.want)
			}
			if got.name() != c.wantName {
				t.Errorf("the cookie is named %q, want %q", got.name(), c.wantName)
			}
			if built := got.cookie("", 0); built.Name != c.wantName || built.Secure != c.secure {
				t.Errorf("the cookie is built as %q with Secure %v, want %q with Secure %v",
					built.Name, built.Secure, c.wantName, c.secure)
			}
		})
	}
}

// What cannot be run on is refused: none as a SameSite, an http address whose
// host is not a loopback one, a Domain that is malformed or that a browser would
// drop, and a Domain on an http address, since a cookie with a Domain needs the
// __Secure- prefix and so Secure, which an http address leaves off.
func TestTheCookieConfigurationRefusesWhatCannotRunOn(t *testing.T) {
	cases := map[string]struct {
		cfg  moduleConfig
		gate gateMount
		want error
	}{
		"same-site none": {
			cfg: moduleConfig{SameSite: "none"}, gate: gateMount{PublicURL: "https://app.example.com"},
			want: authn.ErrInsecureConfig,
		},
		"an http address that is not loopback": {
			cfg: moduleConfig{SameSite: "lax"}, gate: gateMount{PublicURL: "http://app.example.com"},
			want: authn.ErrInsecureConfig,
		},
		"a same-site value that is not a level": {
			cfg: moduleConfig{SameSite: "sometimes"}, gate: gateMount{PublicURL: "https://app.example.com"},
			want: authn.ErrInvalidConfig,
		},
		"an empty address": {
			cfg: moduleConfig{SameSite: "lax"}, gate: gateMount{},
			want: authn.ErrInvalidConfig,
		},
		"an address with no scheme": {
			cfg: moduleConfig{SameSite: "lax"}, gate: gateMount{PublicURL: "app.example.com"},
			want: authn.ErrInvalidConfig,
		},
		"a domain that is not a host name": {
			cfg:  moduleConfig{SameSite: "lax", CookieDomain: "exa mple.com"},
			gate: gateMount{PublicURL: "https://app.example.com"},
			want: authn.ErrInvalidConfig,
		},
		"a domain the address is not under": {
			cfg:  moduleConfig{SameSite: "lax", CookieDomain: "other.example.net"},
			gate: gateMount{PublicURL: "https://app.example.com"},
			want: authn.ErrInvalidConfig,
		},
		"a domain on a loopback http address": {
			cfg:  moduleConfig{SameSite: "lax", CookieDomain: "localhost"},
			gate: gateMount{PublicURL: "http://localhost:8080"},
			want: authn.ErrInvalidConfig,
		},
		"a domain on a loopback http address that the address is not under": {
			cfg:  moduleConfig{SameSite: "lax", CookieDomain: "example.com"},
			gate: gateMount{PublicURL: "http://127.0.0.1"},
			want: authn.ErrInvalidConfig,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := c.cfg.resolve(c.gate)
			if !errors.Is(err, c.want) {
				t.Errorf("resolve = %v, want %v", err, c.want)
			}
		})
	}
}

// SetCookie writes the cookie with the attributes the design fixes on https: the
// __Host- prefix, Secure, HttpOnly, Path=/, SameSite=Lax, no Domain, and a session
// lifetime (no Max-Age and no Expires).
func TestTheSetCookieHeaderCarriesTheFixedAttributes(t *testing.T) {
	f := newFixture(t, dialects[0])
	f.accounts.add(alice, nil)
	issued := f.create(newBrowser(alice))

	rec := httptest.NewRecorder()
	f.p.SetCookie(rec, issued)
	cookies := cookiesOf(rec.Header())
	if len(cookies) != 1 {
		t.Fatalf("SetCookie wrote %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != hostCookieName || c.Value != issued.Token {
		t.Errorf("cookie name %q, value %q, want %q and the token", c.Name, c.Value, hostCookieName)
	}
	if !c.Secure || !c.HttpOnly || c.Path != "/" {
		t.Errorf("attributes: Secure %v, HttpOnly %v, Path %q, want all set with Path=/", c.Secure, c.HttpOnly, c.Path)
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
	if c.Domain != "" {
		t.Errorf("Domain = %q, want none for a host-only cookie", c.Domain)
	}
	if c.MaxAge != 0 || !c.Expires.IsZero() {
		t.Errorf("MaxAge %d, Expires %v, want a session cookie with neither", c.MaxAge, c.Expires)
	}
}

// ClearCookie writes the header that has the browser drop the cookie.
func TestClearCookieDropsTheCookie(t *testing.T) {
	f := newFixture(t, dialects[0])
	rec := httptest.NewRecorder()
	f.p.ClearCookie(rec)
	cookies := cookiesOf(rec.Header())
	if len(cookies) != 1 {
		t.Fatalf("ClearCookie wrote %d cookies, want 1", len(cookies))
	}
	if c := cookies[0]; c.Name != hostCookieName || c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("the clearing cookie is %+v, want %q with no value and a non-positive Max-Age", c, hostCookieName)
	}
}

// The startup diagnostics name the protection a configuration can give up: a
// Domain gives up the __Host- guarantee, and the Warn names the setting that
// carries it.
func TestTheStartupDiagnosticsWarnAboutEachGivenUpProtection(t *testing.T) {
	t.Run("a cookie domain", func(t *testing.T) {
		f := newFixture(t, dialects[0], withCookie(cookieSpec{
			publicURL: "https://app.example.com", domain: "example.com", sameSite: http.SameSiteLaxMode,
		}))
		f.p.diagnose()
		out := f.logs.String()
		if !strings.Contains(out, "level=WARN") || !strings.Contains(out, ConfigNamespace+"."+cookieDomainItem) {
			t.Errorf("the diagnostics do not warn about the Domain:\n%s", out)
		}
	})

	t.Run("a host-only cookie warns about nothing", func(t *testing.T) {
		f := newFixture(t, dialects[0])
		f.p.diagnose()
		if out := f.logs.String(); strings.Contains(out, "level=WARN") {
			t.Errorf("a secure deployment still warned:\n%s", out)
		}
	})
}

// The token a browser session carries is the cookie's value, and a session with
// no token sets no cookie.
func TestSetCookieWritesNothingForASessionWithoutAToken(t *testing.T) {
	f := newFixture(t, dialects[0])
	rec := httptest.NewRecorder()
	f.p.SetCookie(rec, authn.Issued{ID: "sid", ExpiresAt: time.Now().Add(time.Hour)})
	if got := cookiesOf(rec.Header()); len(got) != 0 {
		t.Errorf("SetCookie wrote %d cookies for a session with no token, want none", len(got))
	}
}

// A deployment that omits same-site gets the declared default, and that default
// is lax. The cookie decides its SameSite from the shared value and not from the
// item being present.
func TestAnOmittedSameSiteFallsToTheDeclaredDefault(t *testing.T) {
	if configDefaults.SameSite != "lax" {
		t.Fatalf("the declared same-site default is %q, want lax", configDefaults.SameSite)
	}
	reader := authntest.ConfigReaderAll(t,
		`{"authn":{"gate":{"mount":{"public-url":"https://app.example.com"}}}}`, Module(), gate.Module())
	spec, err := readConfig(reader)
	noErr(t, err)
	if spec.sameSite != http.SameSiteLaxMode {
		t.Errorf("with same-site omitted the cookie's SameSite is %v, want Lax", spec.sameSite)
	}
	if spec.name() != hostCookieName {
		t.Errorf("the cookie is %q, want the host-only cookie", spec.name())
	}
}

// A private but not loopback address over http is not this machine and is not
// treated as secure: it is refused, as any other http address is.
func TestAPrivateAddressOverHttpIsNotTreatedAsSecure(t *testing.T) {
	if _, err := (moduleConfig{SameSite: "lax"}).resolve(gateMount{PublicURL: "http://10.0.0.5"}); !errors.Is(err, authn.ErrInsecureConfig) {
		t.Errorf("an http private address resolved with %v, want ErrInsecureConfig", err)
	}
}

// Every Set-Cookie the module writes for the session cookie, on SetCookie, on
// ClearCookie and on the clearing cookie a reply attaches, carries Secure exactly
// when the public URL is https. On an http address, which starts only on a
// loopback host, the cookie is authn-sid with HttpOnly, Path=/, no Domain and
// the configured SameSite and without Secure, which WebKit on Linux does not
// store when it is set over http. On https, https://localhost included, it is
// __Host-authn-sid and Secure.
func TestTheSessionCookieIsSecureOnAnHTTPSAddressAlone(t *testing.T) {
	for _, c := range []struct {
		address, name string
		secure        bool
	}{
		{"http://localhost:8080", bareCookieName, false},
		{"http://127.0.0.1", bareCookieName, false},
		{"http://[::1]:8080", bareCookieName, false},
		{"https://app.example.com", hostCookieName, true},
		{"https://localhost:8443", hostCookieName, true},
	} {
		t.Run(c.address, func(t *testing.T) {
			spec, err := (moduleConfig{SameSite: "strict"}).resolve(gateMount{PublicURL: c.address})
			noErr(t, err)
			f := newFixture(t, dialects[0], withCookie(spec))
			f.accounts.add(alice, nil)
			issued := f.create(newBrowser(alice))

			set := httptest.NewRecorder()
			f.p.SetCookie(set, issued)
			cleared := httptest.NewRecorder()
			f.p.ClearCookie(cleared)
			lines := map[string][]string{
				"SetCookie":           set.Header().Values("Set-Cookie"),
				"ClearCookie":         cleared.Header().Values("Set-Cookie"),
				"the reply's clearer": {f.p.clearingCookie().String()},
			}
			for source, written := range lines {
				if len(written) != 1 {
					t.Fatalf("%s wrote %d Set-Cookie headers, want 1", source, len(written))
				}
				line := written[0]
				parsed := cookiesOf(http.Header{"Set-Cookie": {line}})
				if len(parsed) != 1 {
					t.Fatalf("%s wrote %q, which does not parse as one cookie", source, line)
				}
				got := parsed[0]
				if hasSecure := strings.Contains(line, "; Secure"); hasSecure != c.secure || got.Secure != c.secure {
					t.Errorf("%s wrote %q, whose Secure is %v, want %v", source, line, got.Secure, c.secure)
				}
				if got.Name != c.name || !got.HttpOnly || got.Path != "/" || got.Domain != "" ||
					got.SameSite != http.SameSiteStrictMode {
					t.Errorf("%s wrote %q, want %s with HttpOnly, Path=/, no Domain and Strict", source, line, c.name)
				}
			}
		})
	}
}

// The clearing cookie a reply attaches has to name the same cookie the session
// was written as, or the browser keeps the session cookie and adds a second one:
// every attribute that identifies the cookie or that a browser checks before
// replacing it is the session cookie's, in each configuration of the name, the
// Domain and the SameSite. The library's own clearing header is held to the same
// attributes, so the two ways a cookie is cleared agree.
func TestTheClearingCookieMatchesTheSessionCookie(t *testing.T) {
	const https = "https://app.example.com"
	for name, c := range map[string]struct {
		spec   cookieSpec
		secure bool
	}{
		"lax":                      {cookieSpec{publicURL: https, sameSite: http.SameSiteLaxMode}, true},
		"strict":                   {cookieSpec{publicURL: https, sameSite: http.SameSiteStrictMode}, true},
		"lax with a domain":        {cookieSpec{publicURL: https, domain: "example.com", sameSite: http.SameSiteLaxMode}, true},
		"strict with a domain":     {cookieSpec{publicURL: https, domain: "example.com", sameSite: http.SameSiteStrictMode}, true},
		"lax on a loopback origin": {cookieSpec{publicURL: "http://localhost:8080", sameSite: http.SameSiteLaxMode}, false},
	} {
		spec := c.spec
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, dialects[0], withCookie(spec))
			f.accounts.add(alice, nil)
			issued := f.create(newBrowser(alice))

			set := httptest.NewRecorder()
			f.p.SetCookie(set, issued)
			written := cookiesOf(set.Header())
			if len(written) != 1 {
				t.Fatalf("SetCookie wrote %d cookies, want 1", len(written))
			}
			session := written[0]
			if session.Secure != c.secure || !session.HttpOnly || session.SameSite != spec.sameSite {
				t.Fatalf("the session cookie is Secure=%v HttpOnly=%v SameSite=%v, want Secure %v, HttpOnly and %v",
					session.Secure, session.HttpOnly, session.SameSite, c.secure, spec.sameSite)
			}

			reply := cookiesOf(http.Header{"Set-Cookie": {f.p.clearingCookie().String()}})
			cleared := httptest.NewRecorder()
			f.p.ClearCookie(cleared)
			library := cookiesOf(cleared.Header())
			if len(reply) != 1 || len(library) != 1 {
				t.Fatalf("the reply's clearing cookie parsed as %d cookies and the library's as %d, want 1 each",
					len(reply), len(library))
			}
			for source, c := range map[string]*http.Cookie{"the reply's": reply[0], "the library's": library[0]} {
				if c.Name != session.Name || c.Domain != session.Domain || c.Path != session.Path ||
					c.Secure != session.Secure || c.HttpOnly != session.HttpOnly ||
					c.SameSite != session.SameSite || c.Partitioned != session.Partitioned {
					t.Errorf("%s clearing cookie is %+v, which does not name the session cookie %+v", source, c, session)
				}
				if c.Value != "" || c.MaxAge >= 0 {
					t.Errorf("%s clearing cookie has value %q and Max-Age %d, want no value and a negative Max-Age",
						source, c.Value, c.MaxAge)
				}
			}
		})
	}
}

// The session is read under the one name the public URL gives: a live token sent
// under another form of the name finds no session, while the same token under
// the configured name does.
func TestASessionUnderAnotherFormOfTheNameIsNotFound(t *testing.T) {
	for _, c := range []struct {
		publicURL, configured string
		others                []string
	}{
		{"http://localhost:8080", bareCookieName, []string{hostCookieName, secureCookieName}},
		{"https://app.example.com", hostCookieName, []string{bareCookieName, secureCookieName}},
	} {
		configured, others := c.configured, c.others
		t.Run(configured, func(t *testing.T) {
			f := newFixture(t, dialects[0], withCookie(cookieSpec{publicURL: c.publicURL, sameSite: http.SameSiteLaxMode}))
			if f.p.cookie.name() != configured {
				t.Fatalf("the session cookie is named %q, want %q", f.p.cookie.name(), configured)
			}
			f.accounts.add(alice, nil)
			issued := f.create(newBrowser(alice))

			if id, ok, err := f.p.Current(newRequest(configured, issued.Token).r); err != nil || !ok || id != issued.ID {
				t.Fatalf("the session read under %s is %q, %v, %v, want %q", configured, id, ok, err, issued.ID)
			}
			for _, other := range others {
				if id, ok, err := f.p.Current(newRequest(other, issued.Token).r); err != nil || ok {
					t.Errorf("the session read under %s is %q, %v, %v, want none", other, id, ok, err)
				}
			}
		})
	}
}
