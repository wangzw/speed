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

// The cookie's name and its attributes are what the deployment's public address
// and the two configuration items come to. Each branch is asserted on its own,
// with the address that selects it.
func TestTheCookieNameFollowsThePublicAddressAndTheTwoItems(t *testing.T) {
	for name, c := range map[string]struct {
		cfg  moduleConfig
		gate gateMount
		want cookieSpec
	}{
		"an https host": {
			cfg:  moduleConfig{SameSite: "lax"},
			gate: gateMount{PublicURL: "https://app.example.com"},
			want: cookieSpec{name: hostCookieName, sameSite: http.SameSiteLaxMode},
		},
		"a loopback host over http is treated as secure": {
			cfg:  moduleConfig{SameSite: "strict"},
			gate: gateMount{PublicURL: "http://localhost:8080"},
			want: cookieSpec{name: hostCookieName, sameSite: http.SameSiteStrictMode},
		},
		"a cookie domain drops the host-only prefix and writes Domain": {
			cfg:  moduleConfig{SameSite: "lax", CookieDomain: ".example.com"},
			gate: gateMount{PublicURL: "https://app.example.com"},
			want: cookieSpec{name: secureCookieName, domain: "example.com", sameSite: http.SameSiteLaxMode},
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
		})
	}
}

// What cannot be run on is refused: none as a SameSite, an http address whose
// host is not a loopback one, and a Domain that is malformed or that a browser
// would drop.
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

// SetCookie writes the cookie with the attributes the design fixes: the __Host-
// prefix, Secure, HttpOnly, Path=/, SameSite=Lax, no Domain, and a session
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
			name: secureCookieName, domain: "example.com", sameSite: http.SameSiteLaxMode,
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
	if spec.name != hostCookieName {
		t.Errorf("the cookie is %q, want the host-only cookie", spec.name)
	}
}

// A private but not loopback address over http is not this machine and is not
// treated as secure: it is refused, as any other http address is.
func TestAPrivateAddressOverHttpIsNotTreatedAsSecure(t *testing.T) {
	if _, err := (moduleConfig{SameSite: "lax"}).resolve(gateMount{PublicURL: "http://10.0.0.5"}); !errors.Is(err, authn.ErrInsecureConfig) {
		t.Errorf("an http private address resolved with %v, want ErrInsecureConfig", err)
	}
}

// A loopback deployment over http, the one http address that starts, writes the
// same cookie an https one does: __Host-sid, Secure and HttpOnly, which a browser
// keeps for a loopback host.
func TestALoopbackHTTPDeploymentWritesTheSecureHostCookie(t *testing.T) {
	for _, address := range []string{"http://localhost:8080", "http://127.0.0.1", "http://[::1]:8080"} {
		t.Run(address, func(t *testing.T) {
			spec, err := (moduleConfig{SameSite: "lax"}).resolve(gateMount{PublicURL: address})
			noErr(t, err)
			f := newFixture(t, dialects[0], withCookie(spec))
			f.accounts.add(alice, nil)
			issued := f.create(newBrowser(alice))

			rec := httptest.NewRecorder()
			f.p.SetCookie(rec, issued)
			cookies := cookiesOf(rec.Header())
			if len(cookies) != 1 {
				t.Fatalf("SetCookie wrote %d cookies, want 1", len(cookies))
			}
			if c := cookies[0]; c.Name != hostCookieName || !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" {
				t.Errorf("the cookie is %q Secure=%v HttpOnly=%v Path=%q Domain=%q, want %q, Secure, HttpOnly, "+
					"Path=/ and no Domain", c.Name, c.Secure, c.HttpOnly, c.Path, c.Domain, hostCookieName)
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
	for name, spec := range map[string]cookieSpec{
		"lax":                  {name: hostCookieName, sameSite: http.SameSiteLaxMode},
		"strict":               {name: hostCookieName, sameSite: http.SameSiteStrictMode},
		"lax with a domain":    {name: secureCookieName, domain: "example.com", sameSite: http.SameSiteLaxMode},
		"strict with a domain": {name: secureCookieName, domain: "example.com", sameSite: http.SameSiteStrictMode},
	} {
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
			if !session.Secure || !session.HttpOnly || session.SameSite != spec.sameSite {
				t.Fatalf("the session cookie is Secure=%v HttpOnly=%v SameSite=%v, want Secure, HttpOnly and %v",
					session.Secure, session.HttpOnly, session.SameSite, spec.sameSite)
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
