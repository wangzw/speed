package cookiename

import (
	"net/http"
	"strings"
	"testing"
)

// TestNameFollowsThePublicURLsScheme holds the name each kind of public URL gives:
// http the bare stem, https __Host- or __Secure- with a Domain.
func TestNameFollowsThePublicURLsScheme(t *testing.T) {
	cases := []struct {
		name, publicURL, domain, want string
	}{
		{"localhost over http", "http://localhost", "", "authn-sid"},
		{"localhost over http on a port", "http://localhost:8080", "", "authn-sid"},
		{"an IPv4 loopback address over http", "http://127.0.0.1", "", "authn-sid"},
		{"the IPv6 loopback address over http", "http://[::1]:8080", "", "authn-sid"},
		{"a scheme written in upper case", "HTTP://localhost", "", "authn-sid"},
		{"http with a domain", "http://localhost", "localhost", "authn-sid"},
		{"http on a host that is not a loopback one", "http://app.example.com", "", "authn-sid"},
		{"http on a private address", "http://192.168.1.10", "", "authn-sid"},
		{"https", "https://app.example.com", "", "__Host-authn-sid"},
		{"https on localhost", "https://localhost", "", "__Host-authn-sid"},
		{"https on a loopback address", "https://127.0.0.1:8443", "", "__Host-authn-sid"},
		{"https with a domain", "https://app.example.com", "example.com", "__Secure-authn-sid"},
		{"no public URL", "", "", "__Host-authn-sid"},
		{"a value that is not a URL", "http://[::1", "", "__Host-authn-sid"},
		{"another scheme", "ftp://localhost", "", "__Host-authn-sid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Name(tc.publicURL, Session, tc.domain); got != tc.want {
				t.Errorf("Name(%q, %q, %q) is %q, want %q", tc.publicURL, Session, tc.domain, got, tc.want)
			}
		})
	}
}

// TestSecureFollowsThePublicURLsScheme holds that https writes Secure and http
// writes none, whatever the host.
func TestSecureFollowsThePublicURLsScheme(t *testing.T) {
	cases := []struct {
		name, publicURL string
		want            bool
	}{
		{"localhost over http", "http://localhost", false},
		{"localhost over http on a port", "http://localhost:8080", false},
		{"an IPv4 loopback address over http", "http://127.0.0.1", false},
		{"the IPv6 loopback address over http", "http://[::1]:8080", false},
		{"a scheme written in upper case", "HTTP://localhost", false},
		{"http on a host that is not a loopback one", "http://app.example.com", false},
		{"http on a private address", "http://192.168.1.10", false},
		{"https", "https://app.example.com", true},
		{"https on localhost", "https://localhost", true},
		{"https on a loopback address", "https://127.0.0.1:8443", true},
		{"no public URL", "", true},
		{"a value that is not a URL", "http://[::1", true},
		{"another scheme", "ftp://localhost", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Secure(tc.publicURL); got != tc.want {
				t.Errorf("Secure(%q) is %v, want %v", tc.publicURL, got, tc.want)
			}
			if prefixed := Name(tc.publicURL, Session, "") != Session; prefixed != Secure(tc.publicURL) {
				t.Errorf("at %q the name is prefixed %v and Secure is %v; the two must agree",
					tc.publicURL, prefixed, Secure(tc.publicURL))
			}
		})
	}
}

// TestTheSchemeAloneDecides holds that the host takes no part: http gives the bare
// name, https __Host-, over every host.
func TestTheSchemeAloneDecides(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "[::1]:8080", "app.example.com", "192.168.1.10:8080"} {
		for scheme, want := range map[string]struct {
			name   string
			secure bool
		}{
			"http":  {Session, false},
			"https": {HostPrefix + Session, true},
		} {
			publicURL := scheme + "://" + host
			if got := Name(publicURL, Session, ""); got != want.name {
				t.Errorf("Name(%q) is %q, want %q", publicURL, got, want.name)
			}
			if got := Secure(publicURL); got != want.secure {
				t.Errorf("Secure(%q) is %v, want %v", publicURL, got, want.secure)
			}
		}
	}
}

// TestEveryStemTakesTheSameRule holds that the flow and reset cookies follow the
// session cookie's rule: one public URL decides every name.
func TestEveryStemTakesTheSameRule(t *testing.T) {
	for _, stem := range []string{Session, Flow, Reset} {
		if got := Name("http://localhost:8080", stem, ""); got != stem {
			t.Errorf("on an http public URL %s is named %q, want the bare stem", stem, got)
		}
		if got := Name("https://app.example.com", stem, ""); got != HostPrefix+stem {
			t.Errorf("on https %s is named %q, want %q", stem, got, HostPrefix+stem)
		}
	}
}

// TestCookieBuildsEveryAttributeFromThePublicURLAndTheSpec holds the cookie's name,
// Secure, HttpOnly, Path and the spec's own attributes.
func TestCookieBuildsEveryAttributeFromThePublicURLAndTheSpec(t *testing.T) {
	cases := []struct {
		name, publicURL string
		spec            Spec
		wantName        string
		wantSecure      bool
	}{
		{
			"localhost over http", "http://localhost:8080",
			Spec{Stem: Flow, Value: "v", MaxAge: 600, SameSite: http.SameSiteLaxMode},
			"authn-flow", false,
		},
		{
			"an IPv4 loopback address over http", "http://127.0.0.1:9000",
			Spec{Stem: Reset, Value: "v", MaxAge: 2400, SameSite: http.SameSiteStrictMode},
			"authn-reset", false,
		},
		{
			"the IPv6 loopback address over http, cleared", "http://[::1]:8080",
			Spec{Stem: Session, MaxAge: -1, SameSite: http.SameSiteStrictMode},
			"authn-sid", false,
		},
		{
			"http on a host that is not a loopback one", "http://app.example.com",
			Spec{Stem: Flow, Value: "v", MaxAge: 600, SameSite: http.SameSiteLaxMode},
			"authn-flow", false,
		},
		{
			"https", "https://app.example.com",
			Spec{Stem: Flow, Value: "v", MaxAge: 600, SameSite: http.SameSiteLaxMode},
			"__Host-authn-flow", true,
		},
		{
			"https on localhost", "https://localhost:8443",
			Spec{Stem: Reset, Value: "v", MaxAge: 2400, SameSite: http.SameSiteStrictMode},
			"__Host-authn-reset", true,
		},
		{
			"https on localhost, cleared", "https://localhost:8443",
			Spec{Stem: Reset, MaxAge: -1, SameSite: http.SameSiteStrictMode},
			"__Host-authn-reset", true,
		},
		{
			"https with a domain", "https://app.example.com",
			Spec{Stem: Session, Value: "v", Domain: "example.com", SameSite: http.SameSiteLaxMode},
			"__Secure-authn-sid", true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Cookie(tc.publicURL, tc.spec)
			if c.Name != tc.wantName || c.Secure != tc.wantSecure {
				t.Errorf("Cookie at %q is %q with Secure %v, want %q with Secure %v",
					tc.publicURL, c.Name, c.Secure, tc.wantName, tc.wantSecure)
			}
			if !c.HttpOnly || c.Path != "/" {
				t.Errorf("Cookie at %q has HttpOnly %v and Path %q, want HttpOnly and /", tc.publicURL, c.HttpOnly, c.Path)
			}
			if c.Value != tc.spec.Value || c.Domain != tc.spec.Domain || c.MaxAge != tc.spec.MaxAge ||
				c.SameSite != tc.spec.SameSite {
				t.Errorf("Cookie at %q carries value %q, Domain %q, Max-Age %d and SameSite %v, want %q, %q, %d and %v",
					tc.publicURL, c.Value, c.Domain, c.MaxAge, c.SameSite,
					tc.spec.Value, tc.spec.Domain, tc.spec.MaxAge, tc.spec.SameSite)
			}

			line := c.String()
			parsed, err := http.ParseSetCookie(line)
			if err != nil {
				t.Fatalf("Cookie at %q serialises as %q, which does not parse: %v", tc.publicURL, line, err)
			}
			if hasSecure := strings.Contains(line, "; Secure"); hasSecure != tc.wantSecure || parsed.Secure != tc.wantSecure {
				t.Errorf("Cookie at %q serialises as %q, whose Secure is %v, want %v",
					tc.publicURL, line, parsed.Secure, tc.wantSecure)
			}
			if parsed.Name != tc.wantName || !parsed.HttpOnly || parsed.Path != "/" ||
				parsed.Domain != tc.spec.Domain || parsed.SameSite != tc.spec.SameSite {
				t.Errorf("Cookie at %q serialises as %q, want %s with HttpOnly, Path=/, Domain %q and %v",
					tc.publicURL, line, tc.wantName, tc.spec.Domain, tc.spec.SameSite)
			}
		})
	}
}

// TestLoopbackHost holds the host test the startup admission of an http public
// URL uses.
func TestLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "LocalHost": true, "127.0.0.1": true, "127.255.255.254": true, "::1": true,
		"": false, "localhost.": false, "app.localhost.example": false, "10.0.0.1": false, "::": false,
		"0.0.0.0": false, "example.com": false,
	} {
		if got := LoopbackHost(host); got != want {
			t.Errorf("LoopbackHost(%q) is %v, want %v", host, got, want)
		}
	}
}
