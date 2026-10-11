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
			want: cookieSpec{name: hostCookieName, sameSite: http.SameSiteLaxMode, secure: true},
		},
		"a loopback host over http is treated as secure": {
			cfg:  moduleConfig{SameSite: "strict"},
			gate: gateMount{PublicURL: "http://localhost:8080"},
			want: cookieSpec{name: hostCookieName, sameSite: http.SameSiteStrictMode, secure: true},
		},
		"a cookie domain drops the host-only prefix and writes Domain": {
			cfg:  moduleConfig{SameSite: "lax", CookieDomain: ".example.com"},
			gate: gateMount{PublicURL: "https://app.example.com"},
			want: cookieSpec{name: secureCookieName, domain: "example.com", sameSite: http.SameSiteLaxMode, secure: true},
		},
		"allow-insecure drops the prefix and Secure": {
			cfg:  moduleConfig{SameSite: "lax"},
			gate: gateMount{PublicURL: "http://app.example.com", AllowInsecure: true},
			want: cookieSpec{name: insecureCookieName, sameSite: http.SameSiteLaxMode, secure: false, insecureAllowed: true},
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

// What cannot be run on is refused: none as a SameSite, an address that is not
// https and had not explicitly allowed insecure use, and a Domain that is malformed
// or that a browser would drop.
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
		"an http address with no allow-insecure": {
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

// An insecure deployment drops the prefix and Secure, which is the whole point of
// allow-insecure and why it warns.
func TestAnInsecureCookieHasNoPrefixAndIsNotSecure(t *testing.T) {
	f := newFixture(t, dialects[0], withCookie(cookieSpec{
		name: insecureCookieName, sameSite: http.SameSiteLaxMode, secure: false, insecureAllowed: true,
	}))
	f.accounts.add(alice, nil)
	issued := f.create(newBrowser(alice))

	rec := httptest.NewRecorder()
	f.p.SetCookie(rec, issued)
	cookies := cookiesOf(rec.Header())
	if len(cookies) != 1 {
		t.Fatalf("SetCookie wrote %d cookies, want 1", len(cookies))
	}
	if c := cookies[0]; c.Name != insecureCookieName || c.Secure {
		t.Errorf("the insecure cookie is %q Secure=%v, want %q and not Secure", c.Name, c.Secure, insecureCookieName)
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

// The startup diagnostics name each protection the configuration gives up: a
// Domain (the __Host- guarantee) and allow-insecure. Each is a Warn with the
// setting that carries it.
func TestTheStartupDiagnosticsWarnAboutEachGivenUpProtection(t *testing.T) {
	t.Run("a cookie domain", func(t *testing.T) {
		f := newFixture(t, dialects[0], withCookie(cookieSpec{
			name: secureCookieName, domain: "example.com", sameSite: http.SameSiteLaxMode, secure: true,
		}))
		f.p.diagnose()
		out := f.logs.String()
		if !strings.Contains(out, "level=WARN") || !strings.Contains(out, ConfigNamespace+"."+cookieDomainItem) {
			t.Errorf("the diagnostics do not warn about the Domain:\n%s", out)
		}
	})

	t.Run("allow-insecure", func(t *testing.T) {
		f := newFixture(t, dialects[0], withCookie(cookieSpec{
			name: insecureCookieName, sameSite: http.SameSiteLaxMode, secure: false, insecureAllowed: true,
		}))
		f.p.diagnose()
		out := f.logs.String()
		if !strings.Contains(out, "level=WARN") || !strings.Contains(out, gateNamespace+"."+gateInsecureItem) {
			t.Errorf("the diagnostics do not warn about insecure use:\n%s", out)
		}
	})

	t.Run("a secure deployment warns about neither", func(t *testing.T) {
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
	if spec.name != hostCookieName || !spec.secure {
		t.Errorf("the cookie is %q secure=%v, want the host-only Secure cookie", spec.name, spec.secure)
	}
}

// A private but not loopback address over http is not this machine and is not
// treated as secure: it needs allow-insecure, as any other http address does.
func TestAPrivateAddressOverHttpIsNotTreatedAsSecure(t *testing.T) {
	if _, err := (moduleConfig{SameSite: "lax"}).resolve(gateMount{PublicURL: "http://10.0.0.5"}); !errors.Is(err, authn.ErrInsecureConfig) {
		t.Errorf("an http private address resolved with %v, want ErrInsecureConfig", err)
	}
	got, err := (moduleConfig{SameSite: "lax"}).resolve(gateMount{PublicURL: "http://10.0.0.5", AllowInsecure: true})
	noErr(t, err)
	if got.name != insecureCookieName || got.secure {
		t.Errorf("with allow-insecure the private http address gave %q secure=%v, want the insecure cookie", got.name, got.secure)
	}
}
