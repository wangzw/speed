package selfservice_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

const newPassword = `{"current_password":"old secret","new_password":"correct horse battery"}`

func cookieValue(w *httptest.ResponseRecorder, name string) (string, bool) {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c.Value, true
		}
	}
	return "", false
}

// TestAPasswordChangeSetsThenRotatesThenRevokesTheOthers holds the success path
// call by call and in order. The write comes first because a revocation made
// before it would sign the account's other sessions out for a change that then
// failed; the cookie comes last, after the last call that can fail.
func TestAPasswordChangeSetsThenRotatesThenRevokesTheOthers(t *testing.T) {
	h := newHarness(t)
	w := h.put("/password", newPassword)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT /password = %d %s, want 204", w.Code, w.Body)
	}
	want := []string{"mfa.Factors", "passwords.Set", "sessions.Rotate", "sessions.RevokeAccount", "sessions.SetCookie"}
	if got := h.log.names(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if len(h.pw.sets) != 1 || h.pw.sets[0] != (setCall{account, "correct horse battery", authn.SetPasswordOptions{Current: "old secret"}}) {
		t.Errorf("Set was called with %+v", h.pw.sets)
	}
	if len(h.sessions.rotated) != 1 || h.sessions.rotated[0] != session {
		t.Errorf("Rotate was called for %v, want the current session", h.sessions.rotated)
	}
	if len(h.sessions.revoked) != 1 || h.sessions.revoked[0] != (revokeCall{account, session, authn.ReasonPasswordChange}) {
		t.Errorf("RevokeAccount was called with %+v, want the account, the current session as the exception and the reason password-change", h.sessions.revoked)
	}
	if v, ok := cookieValue(w, "sid"); !ok || v != "rotated-token" {
		t.Errorf("the response carries cookie %q (present %v), want the rotated token", v, ok)
	}
}

// TestTheHandlerDoesNotPublishThePasswordChangedEvent holds that the event of a
// changed password is the password module's: it is published by Passwords.Set
// and a second one from the handler would be a duplicate. The audit row is the
// handler's.
func TestTheHandlerDoesNotPublishThePasswordChangedEvent(t *testing.T) {
	h := newHarness(t)
	h.put("/password", newPassword)
	if events := h.bus.published(); len(events) != 0 {
		t.Errorf("the handler published %v", events)
	}
	rows := h.audit.entries()
	if len(rows) != 1 || rows[0].Action != "authn.password.change" || rows[0].Outcome != "success" || rows[0].Object != "account:"+string(account) {
		t.Errorf("audit rows = %+v, want one authn.password.change success for the account", rows)
	}
}

// TestACurrentPasswordIsOptional holds that an account that never had a password
// can set its first one: the current password is passed on as given, empty.
func TestACurrentPasswordIsOptional(t *testing.T) {
	h := newHarness(t)
	if w := h.put("/password", `{"new_password":"first password"}`); w.Code != http.StatusNoContent {
		t.Fatalf("PUT /password = %d %s", w.Code, w.Body)
	}
	if got := h.pw.sets[0].opt; got != (authn.SetPasswordOptions{}) {
		t.Errorf("options = %+v, want the zero value", got)
	}
}

// TestATokenSessionGetsNoCookie holds that a session that has no cookie, whose
// rotation hands back no token, is not written one.
func TestATokenSessionGetsNoCookie(t *testing.T) {
	h := newHarness(t)
	h.sessions.issued = authn.Issued{ID: session}
	w := h.put("/password", newPassword)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT /password = %d", w.Code)
	}
	if h.log.count("sessions.SetCookie") != 0 || w.Header().Get("Set-Cookie") != "" {
		t.Error("a session with no token was written a cookie")
	}
	if h.log.count("sessions.RevokeAccount") != 1 {
		t.Error("the other sessions were not revoked")
	}
}

// TestAFailedPasswordWriteRevokesNothing holds each answer the write can give and
// that none of them rotates, revokes, writes a cookie or leaves a success row.
func TestAFailedPasswordWriteRevokesNothing(t *testing.T) {
	cases := []struct {
		err   error
		code  int
		kind  string
		retry string
	}{
		{authn.ErrPasswordPolicy, http.StatusBadRequest, "bad-request", ""},
		{fmt.Errorf("wrapped: %w", authn.ErrPasswordPolicy), http.StatusBadRequest, "bad-request", ""},
		{authn.ErrIdentifierRequired, http.StatusConflict, "login-identifier-required", ""},
		{authn.ErrInvalidCredentials, http.StatusUnauthorized, "login-failed", ""},
		{&authn.ThrottledError{RetryAfter: 7 * time.Second}, http.StatusTooManyRequests, "throttled", "7"},
		{authn.ErrBusy, http.StatusServiceUnavailable, "unavailable", "1"},
		{authn.ErrUnavailable, http.StatusServiceUnavailable, "unavailable", ""},
		{errors.New("driver: connection reset"), http.StatusServiceUnavailable, "unavailable", ""},
		{authn.ErrAccountNotFound, http.StatusUnauthorized, "", ""},
	}
	for _, c := range cases {
		t.Run(c.err.Error(), func(t *testing.T) {
			h := newHarness(t)
			h.pw.err = c.err
			w := h.put("/password", newPassword)
			if w.Code != c.code || problemType(t, w) != c.kind {
				t.Fatalf("answer = %d %s, want %d %q", w.Code, w.Body, c.code, c.kind)
			}
			if got := w.Header().Get("Retry-After"); got != c.retry {
				t.Errorf("Retry-After = %q, want %q", got, c.retry)
			}
			want := []string{"mfa.Factors", "passwords.Set"}
			if got := h.log.names(); !slices.Equal(got, want) {
				t.Errorf("calls = %v, want %v: a failed write is followed by no session call", got, want)
			}
			if w.Header().Get("Set-Cookie") != "" || len(h.audit.entries()) != 0 {
				t.Error("a failed write wrote a cookie or an audit row")
			}
		})
	}
}

// TestAFailedRotationStillRevokesTheOtherSessions holds that the half of the
// settling that protects the account is not skipped because the other half
// failed, that no cookie is written for a token that was not issued, and that
// the answer is the 503 of a state that is in force and unfinished.
func TestAFailedRotationStillRevokesTheOtherSessions(t *testing.T) {
	h := newHarness(t)
	h.sessions.rotateErr = authn.ErrUnavailable
	w := h.put("/password", newPassword)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("answer = %d, want 503", w.Code)
	}
	if h.log.count("sessions.RevokeAccount") != 1 {
		t.Error("the other sessions were not revoked after Rotate failed")
	}
	if h.log.count("sessions.SetCookie") != 0 {
		t.Error("a cookie was written for a token that was never issued")
	}
	if len(h.audit.entries()) != 1 {
		t.Error("the change was in force and left no audit row")
	}
}

// TestAFailedRotationWritesNoCookieEvenWithAReturnedToken holds the guard's
// other half: when the rotation reports an error, no cookie is written even
// though the same call also handed back a token, because the failure is what
// says the new token does not stand and the old one may still be valid.
func TestAFailedRotationWritesNoCookieEvenWithAReturnedToken(t *testing.T) {
	h := newHarness(t)
	h.sessions.rotateErr = authn.ErrUnavailable
	if h.sessions.issued.Token == "" {
		t.Fatal("this case needs the store to hand back a token with the error")
	}
	w := h.put("/password", newPassword)
	if h.log.count("sessions.SetCookie") != 0 || w.Header().Get("Set-Cookie") != "" {
		t.Errorf("a cookie was written although the rotation failed: %q", w.Header().Get("Set-Cookie"))
	}
}

// TestAFailedRevocationStillWritesTheRotatedToken holds that once the token was
// rotated the response carries the new one, because the old one no longer holds
// and the caller would otherwise be signed out of the session it just proved.
func TestAFailedRevocationStillWritesTheRotatedToken(t *testing.T) {
	h := newHarness(t)
	h.sessions.revokeErr = authn.ErrUnavailable
	w := h.put("/password", newPassword)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("answer = %d, want 503", w.Code)
	}
	if v, ok := cookieValue(w, "sid"); !ok || v != "rotated-token" {
		t.Errorf("cookie = %q (present %v), want the rotated token", v, ok)
	}
}

// TestASessionThatIsGoneIsTheGatewaysAnswer holds that a session revoked between
// the request's authentication and its rotation is a 401, not a 503 that invites
// a retry on a session that will never be valid again.
func TestASessionThatIsGoneIsTheGatewaysAnswer(t *testing.T) {
	h := newHarness(t)
	h.sessions.rotateErr = authn.ErrSessionRevoked
	if w := h.put("/password", newPassword); w.Code != http.StatusUnauthorized {
		t.Fatalf("answer = %d, want 401", w.Code)
	}
}

// TestAPasswordRequestBodyIsHeldToTheSharedDecoder holds that the body is read by
// the shared decoder, which is where the media type, the unknown fields and the
// trailing data are refused, and that a password that is not given is refused
// here. None reaches the password module.
func TestAPasswordRequestBodyIsHeldToTheSharedDecoder(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		header map[string]string
		code   int
	}{
		{"no new password", `{"current_password":"old"}`, nil, http.StatusBadRequest},
		{"an empty new password", `{"new_password":""}`, nil, http.StatusBadRequest},
		{"an unknown field", `{"new_password":"x","admin":true}`, nil, http.StatusBadRequest},
		{"not an object", `["x"]`, nil, http.StatusBadRequest},
		{"trailing data", `{"new_password":"x"} {}`, nil, http.StatusBadRequest},
		{"form data", `new_password=x`, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, http.StatusUnsupportedMediaType},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			w := h.do(request{method: http.MethodPut, path: "/password", body: c.body, header: c.header})
			if w.Code != c.code {
				t.Fatalf("answer = %d %s, want %d", w.Code, w.Body, c.code)
			}
			if h.log.count("passwords.Set") != 0 {
				t.Error("a request that was refused reached the password module")
			}
		})
	}
}

// TestNoPasswordIsInAnAnswerOrALogLine drives a password and the current one
// through every failure the write can give and holds that neither is in the
// answer or in anything logged.
func TestNoPasswordIsInAnAnswerOrALogLine(t *testing.T) {
	for _, err := range []error{authn.ErrPasswordPolicy, authn.ErrInvalidCredentials, authn.ErrUnavailable, errors.New("driver: broken pipe")} {
		h := newHarness(t)
		h.pw.err = err
		w := h.put("/password", newPassword)
		for _, secret := range []string{"correct horse battery", "old secret"} {
			if strings.Contains(w.Body.String(), secret) || strings.Contains(h.logs.String(), secret) {
				t.Errorf("with %v, %q is in the answer %q or the log %q", err, secret, w.Body, h.logs)
			}
		}
	}
}
