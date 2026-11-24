package login

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

const (
	flowCookieName  = "__Host-authn-flow"
	resetCookieName = "__Host-authn-reset"
)

func cookieNamed(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	var found []*http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the reply set %d cookies named %s, want one; headers %v", len(found), name, rec.Header())
	}
	return found[0]
}

func holdsHostCookie(t *testing.T, c *http.Cookie, sameSite http.SameSite) {
	t.Helper()
	if !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" || c.SameSite != sameSite {
		t.Errorf("the cookie %s is Secure=%v HttpOnly=%v Path=%q Domain=%q SameSite=%v, want Secure, HttpOnly, "+
			"Path=/, no Domain and SameSite %v", c.Name, c.Secure, c.HttpOnly, c.Path, c.Domain, c.SameSite, sameSite)
	}
}

// TestTheFlowCookieIsASecureHostCookieWhenSetAndWhenCleared holds that the flow cookie is written and cleared with the same attributes.
func TestTheFlowCookieIsASecureHostCookieWhenSetAndWhenCleared(t *testing.T) {
	r := newRig(t, &fakeMethod{name: "password", begin: func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("credentials", nil,
			authn.Field{Name: "identifier", Kind: authn.FieldText, Required: true}), nil
	}})
	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", start.Code, start.Body.String())
	}
	set := cookieNamed(t, start, flowCookieName)
	holdsHostCookie(t, set, http.SameSiteLaxMode)
	if header := start.Header().Get("Set-Cookie"); !strings.Contains(header, "; SameSite=Lax") {
		t.Errorf("the flow cookie is written as %q, want SameSite=Lax", header)
	}
	if set.Value == "" || set.MaxAge <= 0 {
		t.Errorf("the flow cookie has value %q and Max-Age %d, want a token and a positive Max-Age", set.Value, set.MaxAge)
	}

	del := r.send(http.MethodDelete, "/api/v1/auth/flows/current", "", "Cookie", flowCookieName+"="+set.Value)
	if del.Code != http.StatusNoContent {
		t.Fatalf("DELETE flows/current answered %d: %s", del.Code, del.Body.String())
	}
	cleared := cookieNamed(t, del, flowCookieName)
	holdsHostCookie(t, cleared, http.SameSiteLaxMode)
	if cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Errorf("the clearing flow cookie has value %q and Max-Age %d, want no value and a negative Max-Age",
			cleared.Value, cleared.MaxAge)
	}
}

// TestTheResetCookieIsASecureHostCookieWhenSetAndWhenCleared holds that the reset cookie is Strict, written and cleared the same way.
func TestTheResetCookieIsASecureHostCookieWhenSetAndWhenCleared(t *testing.T) {
	r := newResetRig(t)
	start := r.startReset(`{"channel":"email","identifier":"nobody@example.com"}`)
	if start.Code != http.StatusAccepted {
		t.Fatalf("starting an email reset answered %d: %s", start.Code, start.Body.String())
	}
	set := cookieNamed(t, start, resetCookieName)
	holdsHostCookie(t, set, http.SameSiteStrictMode)
	if set.Value == "" || set.MaxAge <= 0 {
		t.Errorf("the reset cookie has value %q and Max-Age %d, want a token and a positive Max-Age", set.Value, set.MaxAge)
	}

	del := r.send(http.MethodDelete, resetCurrentPath, "", "Cookie", resetCookieName+"="+set.Value)
	if del.Code != http.StatusNoContent {
		t.Fatalf("DELETE password-reset answered %d: %s", del.Code, del.Body.String())
	}
	cleared := cookieNamed(t, del, resetCookieName)
	holdsHostCookie(t, cleared, http.SameSiteStrictMode)
	if cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Errorf("the clearing reset cookie has value %q and Max-Age %d, want no value and a negative Max-Age",
			cleared.Value, cleared.MaxAge)
	}
}
