package login

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// cookieNamed reads the one cookie of a reply that carries name.
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

// holdsHostCookie checks the attributes the __Host- prefix requires and the ones
// the binding cookies carry besides: Secure, a path of "/", no Domain, HttpOnly,
// and the SameSite the cookie is written with. The attributes do not depend on
// the deployment: a loopback http deployment gets the same cookie an https one
// does.
func holdsHostCookie(t *testing.T, c *http.Cookie, sameSite http.SameSite) {
	t.Helper()
	if !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" || c.SameSite != sameSite {
		t.Errorf("the cookie %s is Secure=%v HttpOnly=%v Path=%q Domain=%q SameSite=%v, want Secure, HttpOnly, "+
			"Path=/, no Domain and SameSite %v", c.Name, c.Secure, c.HttpOnly, c.Path, c.Domain, c.SameSite, sameSite)
	}
}

// TestTheFlowCookieIsASecureHostCookieWhenSetAndWhenCleared holds the flow
// binding's carrier: the start writes __Host-authn-flow with the attributes its
// prefix requires, and the delete that abandons the flow clears it with the same
// ones, so the browser replaces the cookie it holds instead of keeping it.
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
	holdsHostCookie(t, set, http.SameSiteNoneMode)
	if set.Value == "" || set.MaxAge <= 0 {
		t.Errorf("the flow cookie has value %q and Max-Age %d, want a token and a positive Max-Age", set.Value, set.MaxAge)
	}

	del := r.send(http.MethodDelete, "/api/v1/auth/flows/current", "", "Cookie", flowCookieName+"="+set.Value)
	if del.Code != http.StatusNoContent {
		t.Fatalf("DELETE flows/current answered %d: %s", del.Code, del.Body.String())
	}
	cleared := cookieNamed(t, del, flowCookieName)
	holdsHostCookie(t, cleared, http.SameSiteNoneMode)
	if cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Errorf("the clearing flow cookie has value %q and Max-Age %d, want no value and a negative Max-Age",
			cleared.Value, cleared.MaxAge)
	}
}

// TestTheResetCookieIsASecureHostCookieWhenSetAndWhenCleared holds the reset
// binding's carrier the same way: __Host-authn-reset, SameSite=Strict, written by
// the start and cleared by the delete with the same attributes.
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
