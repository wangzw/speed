package login

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/cookiename"
)

// cookieOrigin is one public URL with the names the flow and reset cookies take
// under it and the names of the other form, which a request may carry and the
// engine must not read.
type cookieOrigin struct {
	name, publicURL       string
	flow, reset           string
	otherFlow, otherReset string
	loopback              bool
}

var cookieOrigins = []cookieOrigin{
	{
		name: "localhost over http", publicURL: "http://localhost:8080",
		flow: "authn-flow", reset: "authn-reset",
		otherFlow: flowCookieName, otherReset: resetCookieName, loopback: true,
	},
	{
		name: "an IPv4 loopback address over http", publicURL: "http://127.0.0.1:9000",
		flow: "authn-flow", reset: "authn-reset",
		otherFlow: flowCookieName, otherReset: resetCookieName, loopback: true,
	},
	{
		name: "an https origin", publicURL: "https://app.example.com",
		flow: flowCookieName, reset: resetCookieName,
		otherFlow: "authn-flow", otherReset: "authn-reset",
	},
	{
		name: "https on localhost", publicURL: "https://localhost:8443",
		flow: flowCookieName, reset: resetCookieName,
		otherFlow: "authn-flow", otherReset: "authn-reset",
	},
}

// TestEveryCookieTheEngineWritesIsNamedForThePublicURL holds each set and clear to the name and Secure its public URL gives.
func TestEveryCookieTheEngineWritesIsNamedForThePublicURL(t *testing.T) {
	for _, origin := range cookieOrigins {
		t.Run(origin.name, func(t *testing.T) {
			var written []string

			r := newRigAt(t, origin.publicURL, &redirectMethod{})
			r.allow("acct1")
			start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
			if start.Code != http.StatusOK {
				t.Fatalf("POST flows answered %d: %s", start.Code, start.Body.String())
			}
			flow := cookieNamed(t, start, origin.flow)
			callback := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie",
				origin.flow+"="+flow.Value)
			if callback.Code != http.StatusSeeOther {
				t.Fatalf("the callback answered %d, want 303: %s", callback.Code, callback.Body.String())
			}
			if len(r.sessions.created) != 1 {
				t.Fatalf("the callback made %d sessions, want one, so it did not read the flow cookie",
					len(r.sessions.created))
			}
			written = append(written, start.Header().Values("Set-Cookie")...)
			written = append(written, callback.Header().Values("Set-Cookie")...)

			again := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
			if again.Code != http.StatusOK {
				t.Fatalf("POST flows answered %d: %s", again.Code, again.Body.String())
			}
			abandoned := r.send(http.MethodDelete, "/api/v1/auth/flows/current", "", "Cookie",
				origin.flow+"="+cookieNamed(t, again, origin.flow).Value)
			if abandoned.Code != http.StatusNoContent {
				t.Fatalf("DELETE flows/current answered %d: %s", abandoned.Code, abandoned.Body.String())
			}
			cookieNamed(t, abandoned, origin.flow)
			written = append(written, again.Header().Values("Set-Cookie")...)
			written = append(written, abandoned.Header().Values("Set-Cookie")...)

			rr := newResetRigAt(t, origin.publicURL)
			begun := rr.startReset(`{"channel":"email","identifier":"nobody@example.com"}`)
			if begun.Code != http.StatusAccepted {
				t.Fatalf("starting an email reset answered %d: %s", begun.Code, begun.Body.String())
			}
			reset := cookieNamed(t, begun, origin.reset)
			deleted := rr.send(http.MethodDelete, resetCurrentPath, "", "Cookie", origin.reset+"="+reset.Value)
			if deleted.Code != http.StatusNoContent {
				t.Fatalf("DELETE password-reset answered %d: %s", deleted.Code, deleted.Body.String())
			}
			cookieNamed(t, deleted, origin.reset)
			written = append(written, begun.Header().Values("Set-Cookie")...)
			written = append(written, deleted.Header().Values("Set-Cookie")...)

			sameSite := map[string]http.SameSite{origin.flow: http.SameSiteLaxMode, origin.reset: http.SameSiteStrictMode}
			set, cleared := map[string]bool{}, map[string]bool{}
			for _, line := range written {
				parsed := (&http.Response{Header: http.Header{"Set-Cookie": {line}}}).Cookies()
				if len(parsed) != 1 {
					t.Errorf("the Set-Cookie %q does not parse as one cookie", line)
					continue
				}
				c := parsed[0]
				if c.MaxAge > 0 {
					set[c.Name] = true
				} else if c.MaxAge < 0 {
					cleared[c.Name] = true
				}
				if hasSecure := strings.Contains(line, "; Secure"); hasSecure != !origin.loopback || c.Secure != !origin.loopback {
					t.Errorf("on %s the Set-Cookie %q carries Secure %v, want %v", origin.publicURL, line,
						c.Secure, !origin.loopback)
				}
				if origin.loopback && strings.HasPrefix(c.Name, "__") {
					t.Errorf("the cookie %q carries a prefix on %s", c.Name, origin.publicURL)
				}
				if !origin.loopback && !strings.HasPrefix(c.Name, cookiename.HostPrefix) {
					t.Errorf("the cookie %q does not begin with %s on %s", c.Name, cookiename.HostPrefix, origin.publicURL)
				}
				want, known := sameSite[c.Name]
				if !known {
					t.Errorf("the engine wrote a cookie %q that is neither the flow nor the reset cookie", c.Name)
					continue
				}
				if !c.HttpOnly || c.Path != "/" || c.Domain != "" || c.SameSite != want {
					t.Errorf("the cookie %s is HttpOnly=%v Path=%q Domain=%q SameSite=%v, want HttpOnly, Path=/, "+
						"no Domain and SameSite %v", c.Name, c.HttpOnly, c.Path, c.Domain, c.SameSite, want)
				}
			}
			for _, name := range []string{origin.flow, origin.reset} {
				if !set[name] || !cleared[name] {
					t.Errorf("the engine set %s %v and cleared it %v, want both, so that each write is held",
						name, set[name], cleared[name])
				}
			}
		})
	}
}

// TestABindingUnderTheOtherFormsNameIsNotFound holds that a token under the other form's cookie name finds no binding.
func TestABindingUnderTheOtherFormsNameIsNotFound(t *testing.T) {
	for _, origin := range cookieOrigins {
		t.Run(origin.name, func(t *testing.T) {
			r := newRigAt(t, origin.publicURL, promptMethod())
			start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
			if start.Code != http.StatusOK {
				t.Fatalf("POST flows answered %d: %s", start.Code, start.Body.String())
			}
			token := cookieNamed(t, start, origin.flow).Value
			if got := r.send(http.MethodGet, "/api/v1/auth/flows/current", "", "Cookie", origin.flow+"="+token); got.Code != http.StatusOK {
				t.Fatalf("the flow read under %s answered %d, want 200: %s", origin.flow, got.Code, got.Body.String())
			}
			if got := r.send(http.MethodGet, "/api/v1/auth/flows/current", "", "Cookie", origin.otherFlow+"="+token); got.Code != http.StatusUnauthorized {
				t.Errorf("the flow read under %s answered %d, want 401: %s", origin.otherFlow, got.Code, got.Body.String())
			}

			rr := newResetRigAt(t, origin.publicURL)
			begun := rr.startReset(`{"channel":"email","identifier":"nobody@example.com"}`)
			if begun.Code != http.StatusAccepted {
				t.Fatalf("starting an email reset answered %d: %s", begun.Code, begun.Body.String())
			}
			secret := cookieNamed(t, begun, origin.reset).Value
			if got := rr.send(http.MethodGet, resetCurrentPath, "", "Cookie", origin.reset+"="+secret); got.Code != http.StatusOK {
				t.Fatalf("the reset read under %s answered %d, want 200: %s", origin.reset, got.Code, got.Body.String())
			}
			if got := rr.send(http.MethodGet, resetCurrentPath, "", "Cookie", origin.otherReset+"="+secret); got.Code != http.StatusUnauthorized {
				t.Errorf("the reset read under %s answered %d, want 401: %s", origin.otherReset, got.Code, got.Body.String())
			}
		})
	}
}

func promptMethod() *fakeMethod {
	return &fakeMethod{name: "password", begin: func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("credentials", nil,
			authn.Field{Name: "identifier", Kind: authn.FieldText, Required: true}), nil
	}}
}
