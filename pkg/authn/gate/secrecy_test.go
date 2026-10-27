package gate

import (
	"context"
	"errors"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// recordText is everything a record says: its message and every attribute, groups
// included, as key=value.
func recordText(r slog.Record) string {
	var b strings.Builder
	b.WriteString(r.Message)
	var write func(prefix string, a slog.Attr)
	write = func(prefix string, a slog.Attr) {
		v := a.Value.Resolve()
		if v.Kind() == slog.KindGroup {
			for _, member := range v.Group() {
				write(prefix+a.Key+".", member)
			}
			return
		}
		b.WriteString(" " + prefix + a.Key + "=" + v.String())
	}
	r.Attrs(func(a slog.Attr) bool {
		write("", a)
		return true
	})
	return b.String()
}

// sharedWindow returns a stretch of the secret, twelve bytes long or the whole of
// it when it is shorter, that haystack carries, and whether there is one. Looking
// for a stretch and not the whole value is what catches a record that cut the
// value short, lower-cased it or clipped it.
func sharedWindow(haystack, secret string) (string, bool) {
	n := min(12, len(secret))
	for i := 0; i+n <= len(secret); i++ {
		if window := secret[i : i+n]; strings.Contains(haystack, window) {
			return window, true
		}
		if window := strings.ToLower(secret[i : i+n]); strings.Contains(haystack, window) {
			return window, true
		}
	}
	return "", false
}

// responseText is the whole response: status line, headers and body.
func responseText(w *httptest.ResponseRecorder) string {
	res := w.Result()
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	var b strings.Builder
	for name, values := range res.Header {
		b.WriteString(name + ": " + strings.Join(values, ", ") + "\n")
	}
	b.Write(body)
	return b.String()
}

// TestNoCredentialTextReachesARecordOrTheResponse holds the rule that a credential
// is written nowhere: not in the log line a refusal produces, whatever the
// refusal is for, and not in the response. Each case sends a secret in the place
// a client can put one and drives it down one of the paths that end a request.
//
// The first group of cases puts the secret where a scheme name belongs. A client
// that leaves the scheme out writes the credential itself there, and the line
// that says no backend takes the scheme is the one place the gateway used to
// copy the word.
func TestNoCredentialTextReachesARecordOrTheResponse(t *testing.T) {
	const (
		jwt       = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.c2lnbmF0dXJlLXNlY3JldC1ieXRlcw"
		hexKey    = "4f9c1e7a2b6d4c0f9a8b7c6d5e4f3a2b1c0d9e8f"
		mixedKey  = "Sk_Live_4eC39HqLyjWDarjtT1zdp7dc"
		refresh   = "spd_rt_Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MA"
		valueText = "c2VjcmV0LXZhbHVlLTEyMzQ1Njc4OTA"
	)
	header := func(format string) func(*nethttp.Request, string) *nethttp.Request {
		return func(r *nethttp.Request, secret string) *nethttp.Request {
			r.Header.Set(authHeader, strings.Replace(format, "%s", secret, 1))
			return r
		}
	}
	cookie := func(r *nethttp.Request, secret string) *nethttp.Request {
		return withCookie(r, "sid", secret)
	}
	crossSite := func(r *nethttp.Request, secret string) *nethttp.Request {
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		return cookie(r, secret)
	}
	twoCookies := func(r *nethttp.Request, secret string) *nethttp.Request {
		return withCookie(cookie(r, secret), "sid", secret+"-second")
	}
	for _, c := range []struct {
		name   string
		secret string
		// answer is what the backend says about the credential.
		answer func(*fakeVerifier) authn.Verdict
		edit   func(*nethttp.Request, string) *nethttp.Request
	}{
		{"a bare JWT in the Authorization header", jwt, nil, header("%s")},
		{"a bare hexadecimal key in the Authorization header", hexKey, nil, header("%s")},
		{"a bare mixed-case key in the Authorization header", mixedKey, nil, header("%s")},
		{"a bare refresh token in the Authorization header", refresh, nil, header("%s")},
		{"a credential after a tab and not a space", hexKey, nil, header("Bearer\t%s")},
		{"a credential before an unknown scheme-like word and a space", mixedKey, nil, header("%s payload")},
		{"an unknown scheme with a value", valueText, nil, header("Digest %s")},
		{"a very long first word", strings.Repeat("A", 5000), nil, header("%s payload")},
		{"a cookie refused by the cross-origin check", mixedKey, nil, crossSite},
		{"two cookies of one name", mixedKey, nil, twoCookies},
		{"a cookie the backend refuses", mixedKey, func(*fakeVerifier) authn.Verdict { return authn.Denied(errors.New("no such session")) }, cookie},
		{"a Bearer token the backend refuses", mixedKey, func(*fakeVerifier) authn.Verdict { return authn.Denied(errors.New("expired")) }, header("Bearer %s")},
		{"a Basic credential the backend refuses", valueText, func(*fakeVerifier) authn.Verdict { return authn.Denied(errors.New("wrong password")) }, header("Basic %s")},
		{"a credential the backend cannot answer about", mixedKey, func(*fakeVerifier) authn.Verdict { return authn.Unavailable(errors.New("store down")) }, header("Bearer %s")},
		{"a credential the backend returns no verdict for", mixedKey, func(*fakeVerifier) authn.Verdict { return authn.Verdict{} }, header("Bearer %s")},
		{"a credential whose principal fails the exit check", mixedKey, func(f *fakeVerifier) authn.Verdict {
			p := principalOf(f, "x")
			p.Account = "static.root"
			return authn.Authenticated(p)
		}, header("Bearer %s")},
		{"a credential whose principal impersonates where that is not admitted", mixedKey, func(f *fakeVerifier) authn.Verdict {
			p := principalOf(f, "customer-1")
			p.Actor, p.Session, p.Scope = "admin-9", "0198f0aa-0000-7000-8000-000000000002", "tenant-a"
			return authn.Authenticated(p)
		}, header("Bearer %s")},
	} {
		t.Run(c.name, func(t *testing.T) {
			token := schemeBackend("token", "bearer")
			token.accepts.Impersonation = true
			session := cookieBackend("session", "sid")
			basic := basicBackend("static", "static.")
			for _, f := range []*fakeVerifier{token, session, basic} {
				if c.answer != nil {
					f.verify = func(context.Context, authn.Credential) authn.Verdict { return c.answer(f) }
				}
			}
			var cleared atomic.Int64
			g := testGateway(t, []string{"public"}, token, clearingVerifier{fakeVerifier: session, cleared: &cleared}, basic)
			sealed(t, g, routerWith("public"))

			h := &recorder{}
			r := c.edit(recorded(h, "POST", "/x"), c.secret)
			engine, _ := layerOn(g, "public")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, r)

			if w.Code != nethttp.StatusUnauthorized && w.Code != nethttp.StatusServiceUnavailable {
				t.Fatalf("answered %d, want a refusal or an outage: nothing here would be checked", w.Code)
			}
			h.mu.Lock()
			records := append([]slog.Record(nil), h.records...)
			h.mu.Unlock()
			if len(records) == 0 {
				t.Fatal("the refusal wrote no record, so nothing here was checked")
			}
			haystacks := []string{responseText(w)}
			for _, rec := range records {
				haystacks = append(haystacks, recordText(rec))
			}
			for _, text := range haystacks {
				if window, found := sharedWindow(text, c.secret); found {
					t.Errorf("%q, a stretch of the credential, is written in %q", window, text)
				}
			}
		})
	}
}

// TestTheLengthOfAnUnclaimedSchemeIsAllTheRecordSays holds what the record of an
// unclaimed scheme does carry: that a scheme was there, how long the word was,
// and whether a space followed it, which is what tells a client that sent a scheme
// from one that sent a bare credential.
func TestTheLengthOfAnUnclaimedSchemeIsAllTheRecordSays(t *testing.T) {
	for _, c := range []struct {
		name      string
		header    string
		length    string
		separated string
	}{
		{"a word and a value", "Digest abc", "6", "true"},
		{"a bare word", "abcdefghij", "10", "false"},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
			sealed(t, g, routerWith("public"))
			_, rec := conclusionOf(t, g, "public", func(r *nethttp.Request) *nethttp.Request {
				r.Header.Set(authHeader, c.header)
				return r
			})
			if got := attrText(rec, "scheme_length"); got != c.length {
				t.Errorf("the record carries scheme_length %q, want %q", got, c.length)
			}
			if got := attrText(rec, "scheme_separated"); got != c.separated {
				t.Errorf("the record carries scheme_separated %q, want %q", got, c.separated)
			}
		})
	}
}

// TestTheCookieNameIsReadableThroughTheRealRedaction holds that the attribute
// naming a cookie survives the gateway's own key rules. The key rules mask every
// record written under a credential key, and the cookie a warning names is a
// name and not a value; the startup warning about a cookie without the host
// prefix exists to say which one, and masked it says nothing.
func TestTheCookieNameIsReadableThroughTheRealRedaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.json")
	body := `{"authn":{"gate":{"endpoints":["public"]}},` +
		`"log":{"outputs":[{"to":"file","format":"json","path":"` + path + `"}]}}`
	err := assembly{
		body:     body,
		backends: []authn.Verifier{cookieBackend("session", "plainsid")},
		router:   routerWith("public"),
		modules:  []core.Module{log.Module()},
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log output: %v", err)
	}
	if !strings.Contains(string(written), `"cookie_name":"plainsid"`) {
		t.Errorf("the warning does not name the cookie in the output:\n%s", written)
	}
}

// TestAConfigurationErrorNeverRepeatsWhatMayHoldASecret holds that the error for a
// public URL or a trusted origin written in the wrong form does not carry the
// user information, the query or the fragment of what was written: a password put
// in a URL is a mistake, and the refusal must not copy it into every place a
// startup failure is written to.
func TestAConfigurationErrorNeverRepeatsWhatMayHoldASecret(t *testing.T) {
	const secret = "hunter2-password"
	for name, edit := range map[string]func(*moduleConfig){
		"user information in the public URL": func(c *moduleConfig) {
			c.Mount.PublicURL = "https://admin:" + secret + "@example.com"
		},
		"a query in the public URL": func(c *moduleConfig) {
			c.Mount.PublicURL = "https://example.com?token=" + secret
		},
		"a fragment in the public URL": func(c *moduleConfig) {
			c.Mount.PublicURL = "https://example.com#" + secret
		},
		"user information in an http public URL": func(c *moduleConfig) {
			c.Mount.PublicURL = "http://admin:" + secret + "@example.com:8080"
		},
		"a public URL that does not parse": func(c *moduleConfig) {
			c.Mount.PublicURL = "https://admin:" + secret + "@exa mple.com:port"
		},
		"a trusted origin with a path": func(c *moduleConfig) {
			c.TrustedOrigins = []string{"https://example.com/" + secret}
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := configDefaults
			c.Endpoints = []string{"public"}
			edit(&c)
			_, err := c.resolve()
			if !errors.Is(err, authn.ErrInvalidConfig) && !errors.Is(err, authn.ErrInsecureConfig) {
				t.Fatalf("resolve returned %v, want a refusal", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the error repeats what was written: %v", err)
			}
		})
	}
}

// TestAnAbsentResolutionLayerListIsNotAShapeTheGatewayCouldNotRead holds the
// difference between a configuration nobody wrote and one the gateway could not
// decode. The tenant resolution layer declares its endpoint list as required, so
// a command that includes the layer and leaves the list out decodes to the
// missing-required error: that resolves a tenant on no endpoint, so nothing
// conflicts and nothing needs a warning, which is for a shape that cannot be read.
func TestAnAbsentResolutionLayerListIsNotAShapeTheGatewayCouldNotRead(t *testing.T) {
	for name, c := range map[string]struct {
		body     string
		warnings int
	}{
		"the list is not written":           {`{"authn":{"gate":{"endpoints":["public"]}}}`, 0},
		"the list is written, and holds it": {`{"authn":{"gate":{"endpoints":["public"]}},"tenant":{"resolve":{"endpoints":["admin"]}}}`, 0},
		"the list is the wrong type":        {`{"authn":{"gate":{"endpoints":["public"]}},"tenant":{"resolve":{"endpoints":5}}}`, 1},
	} {
		t.Run(name, func(t *testing.T) {
			gateModule := Module()
			reader := authntest.ConfigReaderAll(t, c.body, gateModule, resolutionLayer(""))
			reg := core.New()
			reg.Register(config.Module())
			reg.Supply(reader, config.ModuleName)

			h := &recorder{}
			g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
			g.logger = slog.New(h)
			g.resolutionEndpoints(reg)
			if got := len(warnings(h)); got != c.warnings {
				t.Errorf("%d warning(s) written, want %d: %v", got, c.warnings, h.recordsOf())
			}
		})
	}
}

// TestAnAmbiguousLoggingCapabilityStopsInitToo holds that a failure to take the
// logging capability up that is not its absence ends Init with the failure, as
// it ends New: reading it as absence would leave the scheme rule unregistered
// without a word.
func TestAnAmbiguousLoggingCapabilityStopsInitToo(t *testing.T) {
	reg := core.New()
	for _, name := range []string{"stand-in.log-a", "stand-in.log-b"} {
		reg.Register(standIn(name, (*log.Logger)(nil)))
		reg.Supply(recordingLogging{logger: quiet()}, name)
	}
	g := testGateway(t, []string{"public"}, basicBackend("static", "static."))
	if err := g.takeLogger(reg); !errors.Is(err, core.ErrAmbiguousProvider) {
		t.Errorf("takeLogger returned %v, want the ambiguity carried out", err)
	}
}
