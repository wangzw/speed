package mount

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// TestOriginIsTheSchemeAndTheHostAlone holds what an origin is made of: the form
// an Origin header takes, whatever else the URL it is read from carries.
func TestOriginIsTheSchemeAndTheHostAlone(t *testing.T) {
	for _, c := range []struct {
		name string
		url  string
		want string
	}{
		{"no URL has no origin", "", ""},
		{"an https host", "https://example.com", "https://example.com"},
		{"a port is part of the origin", "https://example.com:8443", "https://example.com:8443"},
		{"an http loopback host", "http://localhost:8080", "http://localhost:8080"},
		{"an IPv6 host keeps its brackets", "http://[::1]:8080", "http://[::1]:8080"},
		{"a path is not part of it", "https://example.com/app", "https://example.com"},
		{"a scheme is lower-cased by the parser", "HTTPS://example.com", "https://example.com"},
		{"the https default port is left out", "https://example.com:443", "https://example.com"},
		{"the http default port is left out", "http://example.com:80", "http://example.com"},
		{"the other scheme's default port stays", "https://example.com:80", "https://example.com:80"},
		{"the other scheme's default port stays for http", "http://example.com:443", "http://example.com:443"},
		{"a leading zero in the port is not part of it", "https://example.com:0443", "https://example.com"},
		{"the host is lower-cased", "https://Example.COM", "https://example.com"},
		{"a trailing dot on the host is dropped", "https://example.com.", "https://example.com"},
		{"an IPv6 host is lower-cased and its default port left out", "http://[FE80::1]:80", "http://[fe80::1]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := Origin(c.url)
			if err != nil {
				t.Fatalf("Origin(%q) failed: %v", c.url, err)
			}
			if got != c.want {
				t.Errorf("Origin(%q) = %q, want %q", c.url, got, c.want)
			}
		})
	}
}

// TestOriginRefusesWhatIsNotAnHTTPURL holds that a value that cannot name an
// origin is an error and not an empty origin, which would read as "no public
// URL" and trust nothing without saying so.
func TestOriginRefusesWhatIsNotAnHTTPURL(t *testing.T) {
	for _, c := range []struct{ name, url string }{
		{"no scheme", "example.com"},
		{"no host", "https://"},
		{"another scheme", "ftp://example.com"},
		{"an opaque URL", "mailto:a@example.com"},
		{"one the parser cannot read", "://example.com"},
		{"a control character in the host", "https://exa\x7fmple.com"},
		{"an empty port", "https://example.com:"},
		{"port zero", "https://example.com:0"},
		{"a port out of range", "https://example.com:99999"},
		{"user information", "https://u:p@example.com"},
		{"a query", "https://example.com?x=1"},
		{"an empty query", "https://example.com?"},
		{"a fragment", "https://example.com#f"},
		{"a wildcard host", "https://*.example.com"},
		{"a host that is not ASCII", "https://b\u00fccher.example"},
		{"a zoned address", "http://[fe80::1%25eth0]"},
		{"only a dot for a host", "https://."},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got, err := Origin(c.url); err == nil {
				t.Errorf("Origin(%q) = %q with no error", c.url, got)
			}
		})
	}
}

// TestTrustedOriginsListsThePublicOneFirstAndEachOtherOnce holds the set the
// check is built from: the public origin is always in it when there is one, what
// is trusted besides is in it as written, and nothing is in it twice.
func TestTrustedOriginsListsThePublicOneFirstAndEachOtherOnce(t *testing.T) {
	for _, c := range []struct {
		name    string
		public  string
		trusted []string
		want    []string
	}{
		{"nothing at all", "", nil, []string{}},
		{"the public origin alone", "https://app.example.com/x", nil, []string{"https://app.example.com"}},
		{"trusted origins alone", "", []string{"https://a.example", "http://b.example:8080"}, []string{"https://a.example", "http://b.example:8080"}},
		{"both, the public origin first", "https://app.example.com", []string{"https://a.example"}, []string{"https://app.example.com", "https://a.example"}},
		{"the public origin repeated in the list", "https://app.example.com", []string{"https://a.example", "https://app.example.com"}, []string{"https://app.example.com", "https://a.example"}},
		{"an origin listed twice", "", []string{"https://a.example", "https://a.example"}, []string{"https://a.example"}},
		{"another case is the same origin", "", []string{"https://a.example", "https://A.example"}, []string{"https://a.example"}},
		{"a default port is the same origin", "", []string{"https://a.example", "https://a.example:443"}, []string{"https://a.example"}},
		{"an entry is written in the form a browser sends", "https://app.example.com", []string{"HTTPS://Sibling.Example.COM:443"}, []string{"https://app.example.com", "https://sibling.example.com"}},
		{"the public origin in another spelling", "https://App.Example.com:443", []string{"https://app.example.com"}, []string{"https://app.example.com"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := TrustedOrigins(c.public, c.trusted)
			if err != nil {
				t.Fatalf("TrustedOrigins failed: %v", err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("TrustedOrigins(%q, %v) = %v, want %v", c.public, c.trusted, got, c.want)
			}
		})
	}
}

// TestTrustedOriginsDoesNotWriteIntoWhatItWasGiven holds that the list a caller
// passed in is not the list the origin is appended to: the configuration slice
// is shared with whoever else reads it.
func TestTrustedOriginsDoesNotWriteIntoWhatItWasGiven(t *testing.T) {
	trusted := make([]string, 1, 4)
	trusted[0] = "https://a.example"
	if _, err := TrustedOrigins("https://app.example.com", trusted); err != nil {
		t.Fatalf("TrustedOrigins failed: %v", err)
	}
	if got := trusted[:2][1]; got != "" {
		t.Errorf("the caller's backing array now holds %q beyond its list", got)
	}
}

// TestTrustedOriginsRefusesAnEntryThatIsNotAnOrigin holds that a list entry is
// judged on its own and is not skipped, and that what is said about it does not
// repeat the value, which may carry user information.
func TestTrustedOriginsRefusesAnEntryThatIsNotAnOrigin(t *testing.T) {
	for _, entry := range []string{
		"", "a.example", "https://a.example/x", "https://a.example/", "https://*.a.example",
		"https://u:pw-secret@a.example", "https://a.example:", "https://b\u00fccher.example", "ftp://a.example",
	} {
		got, err := TrustedOrigins("", []string{"https://ok.example", entry})
		if err == nil {
			t.Errorf("TrustedOrigins accepted the entry %q and gave %v", entry, got)
			continue
		}
		if strings.Contains(err.Error(), "pw-secret") {
			t.Errorf("the error repeats the entry: %v", err)
		}
	}
}

// TestTrustedOriginsRefusesAPublicURLThatHasNoOrigin holds that the public URL
// is judged here too: a caller that did not validate it first is not handed a set
// that quietly lacks the one origin that is always trusted.
func TestTrustedOriginsRefusesAPublicURLThatHasNoOrigin(t *testing.T) {
	if got, err := TrustedOrigins("not a url", []string{"https://a.example"}); err == nil {
		t.Errorf("TrustedOrigins gave %v with no error for a public URL with no origin", got)
	}
}

// crossSite is a state-changing request a browser sent from another site, with
// the headers it would have put on it.
func crossSite(origin string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "https://app.example.com/auth/x", nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Origin", origin)
	return r
}

// TestCrossOriginTrustsThePublicOriginAndTheListedOnesAndNoOther holds the check
// that is built: each origin of the set passes, any other does not. The refusals
// are what keep an implementation that trusted everything from passing.
func TestCrossOriginTrustsThePublicOriginAndTheListedOnesAndNoOther(t *testing.T) {
	check, err := CrossOrigin("https://app.example.com", []string{"https://sibling.example.com"})
	if err != nil {
		t.Fatalf("CrossOrigin failed: %v", err)
	}
	for _, c := range []struct {
		origin string
		passes bool
	}{
		{"https://app.example.com", true},
		{"https://sibling.example.com", true},
		{"https://evil.example", false},
		{"http://app.example.com", false},
		{"https://app.example.com:8443", false},
		{"https://x.app.example.com", false},
	} {
		err := check.Check(crossSite(c.origin))
		if (err == nil) != c.passes {
			t.Errorf("a cross-site request from %q: Check = %v, want pass %v", c.origin, err, c.passes)
		}
	}
}

// TestCrossOriginRefusesAnUnusablePublicURL holds that a public URL that names
// no origin fails the build of the check; swallowing it would hand back a check
// that trusts nothing, with no sign at start-up.
func TestCrossOriginRefusesAnUnusablePublicURL(t *testing.T) {
	for _, publicURL := range []string{
		"ftp://app.example.com", "https://", "not a url", "mailto:a@b.example",
		"https://*.example.com", "https://user:pw@app.example.com", "https://app.example.com:",
		"https://app.example.com:0", "https://app.example.com:65536", "https://b\u00fccher.example",
		"https://app.example.com?x=1", "https://app.example.com#x", "https://[fe80::1%25eth0]",
	} {
		if check, err := CrossOrigin(publicURL, nil); err == nil {
			t.Errorf("CrossOrigin built %v over the public URL %q", check, publicURL)
		}
		if got, err := TrustedOrigins(publicURL, []string{"https://sibling.example.com"}); err == nil {
			t.Errorf("TrustedOrigins gave %v over the public URL %q", got, publicURL)
		}
	}
}

// TestCrossOriginTrustsTheOriginAsABrowserSendsIt holds the point of the
// canonical form: a public URL or entry written with a default port or capitals
// still trusts the origin header the browser sends.
func TestCrossOriginTrustsTheOriginAsABrowserSendsIt(t *testing.T) {
	check, err := CrossOrigin("https://App.Example.com:443/ui", []string{"HTTPS://Sibling.example.com:443"})
	if err != nil {
		t.Fatalf("CrossOrigin failed: %v", err)
	}
	for _, origin := range []string{"https://app.example.com", "https://sibling.example.com"} {
		if err := check.Check(crossSite(origin)); err != nil {
			t.Errorf("a cross-site request from %q was refused: %v", origin, err)
		}
	}
}

// TestCrossOriginWithNoPublicURLTrustsOnlyTheList holds the other half of the set
// being what was given: with no public URL no origin is trusted by default.
func TestCrossOriginWithNoPublicURLTrustsOnlyTheList(t *testing.T) {
	check, err := CrossOrigin("", nil)
	if err != nil {
		t.Fatalf("CrossOrigin failed: %v", err)
	}
	if err := check.Check(crossSite("https://app.example.com")); err == nil {
		t.Error("a cross-site request passed a check built with no public URL and no list")
	}
}

// TestCrossOriginRefusesAnOriginTheLibraryCannotTrust holds that an entry that
// is not an origin fails the build of the check, and is not left out of it: a
// list with a dropped entry would read as a list that was applied.
func TestCrossOriginRefusesAnOriginTheLibraryCannotTrust(t *testing.T) {
	for _, c := range []struct{ name, origin string }{
		{"no scheme", "a.example"},
		{"a path", "https://a.example/x"},
		{"empty", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if check, err := CrossOrigin("https://app.example.com", []string{c.origin}); err == nil {
				t.Errorf("CrossOrigin built %v over the origin %q", check, c.origin)
			}
		})
	}
}

// TestEachCrossOriginCheckIsItsOwn holds that two checks built from one
// configuration do not share a bypass table: the callback pattern the Mounter
// adds to its own is not a hole in the gateway's.
func TestEachCrossOriginCheckIsItsOwn(t *testing.T) {
	mounter, err := CrossOrigin("https://app.example.com", nil)
	if err != nil {
		t.Fatalf("CrossOrigin failed: %v", err)
	}
	gateway, err := CrossOrigin("https://app.example.com", nil)
	if err != nil {
		t.Fatalf("CrossOrigin failed: %v", err)
	}
	mounter.AddInsecureBypassPattern("POST /auth/x")
	if err := mounter.Check(crossSite("https://evil.example")); err != nil {
		t.Fatalf("the check that holds the pattern refused the request it bypasses: %v", err)
	}
	if err := gateway.Check(crossSite("https://evil.example")); err == nil {
		t.Error("the pattern added to one check bypasses the other")
	}
}
