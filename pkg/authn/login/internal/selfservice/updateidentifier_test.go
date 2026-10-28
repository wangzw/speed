package selfservice_test

import (
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// axesAccount gives the account a username, an unproved phone number and two
// email addresses, one proved and one only asserted, with their axes set the way
// the account module would have written them.
func (h *harness) axesAccount() {
	h.accounts.identifiers = []authn.Identifier{
		{
			ID: "ident-1", Kind: authn.IdentifierUsername, Value: "ada",
			VerifiedAt: now.Add(-time.Hour), VerifiedVia: authn.VerifiedViaAdmin, SignIn: true,
		},
		{
			ID: "ident-phone", Kind: authn.IdentifierPhone, Value: "+8613800000000",
			VerifiedAt: now.Add(-time.Minute), VerifiedVia: authn.VerifiedViaSMSCode,
		},
		{
			ID: "ident-proved", Kind: authn.IdentifierEmail, Value: "ada@example.com",
			VerifiedAt: now.Add(-time.Minute), VerifiedVia: authn.VerifiedViaEmailCode, Contact: true,
		},
		{
			ID: "ident-unproved", Kind: authn.IdentifierEmail, Value: "asserted@example.com",
			VerifiedAt: now.Add(-time.Minute), VerifiedVia: authn.VerifiedViaAdmin,
		},
	}
}

// TestChangingOnlyTheContactNeedsNoFreshAuthentication holds the lighter half of
// the route: a contact is not a way in, so writing it asks for CSRF (which the
// mounter applies to every write) but not for a fresh authentication, and it
// neither rotates the session nor revokes the others.
func TestChangingOnlyTheContactNeedsNoFreshAuthentication(t *testing.T) {
	h := newHarness(t)
	h.axesAccount()
	// A first factor older than the freshness window, which a sign-in change
	// would refuse.
	h.as(authntest.WithAuthTime(now.Add(-30 * time.Minute)))
	w := h.put("/identifiers/ident-unproved", `{"contact":true}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT /identifiers/ident-unproved = %d %s, want 204", w.Code, w.Body)
	}
	want := []string{"accounts.Identifiers acct-1", "admin.SetIdentifierFlags acct-1 ident-unproved"}
	if got := h.log.all(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if len(h.admin.flags) != 1 {
		t.Fatalf("SetIdentifierFlags was called with %+v, want once", h.admin.flags)
	}
	if got := h.admin.flags[0].flags; !got.Contact || got.SignIn {
		t.Errorf("the flags written are %+v, want contact true and the row's sign-in left false", got)
	}
	if len(h.sessions.rotated) != 0 || len(h.sessions.revoked) != 0 {
		t.Errorf("a contact change rotated or revoked sessions: %v %v", h.sessions.rotated, h.sessions.revoked)
	}
}

// TestChoosingAnIdentifierAsASignInWayRotatesTheSession holds the heavier half: a
// sign-in change asks for fresh authentication, writes both axes as they now
// stand, and, when the row becomes a sign-in way, rotates the current session and
// revokes the account's others.
func TestChoosingAnIdentifierAsASignInWayRotatesTheSession(t *testing.T) {
	h := newHarness(t)
	h.axesAccount()
	w := h.put("/identifiers/ident-proved", `{"sign_in":true}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT /identifiers/ident-proved = %d %s, want 204", w.Code, w.Body)
	}
	want := []string{
		"mfa.Factors acct-1", "accounts.Identifiers acct-1", "admin.SetIdentifierFlags acct-1 ident-proved",
		"sessions.Rotate sess-1", "sessions.RevokeAccount self", "sessions.SetCookie",
	}
	if got := h.log.all(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if len(h.admin.flags) != 1 || h.admin.flags[0].flags != (authn.IdentifierFlags{Contact: true, SignIn: true}) {
		t.Errorf("the flags written are %+v, want the contact left true beside sign_in", h.admin.flags)
	}
	if len(h.sessions.revoked) != 1 || h.sessions.revoked[0] != (revokeCall{account, session, authn.ReasonSelf}) {
		t.Errorf("RevokeAccount was called with %+v", h.sessions.revoked)
	}
	if v, ok := cookieValue(w, "sid"); !ok || v != "rotated-token" {
		t.Errorf("cookie = %q (present %v), want the rotated token", v, ok)
	}
}

// TestChoosingAPhoneAsASignInWayRotatesTheSession holds the same rotation for a
// proved phone number: the requirement names a verified email address or phone
// number, and either choice is a new way in.
func TestChoosingAPhoneAsASignInWayRotatesTheSession(t *testing.T) {
	h := newHarness(t)
	h.axesAccount()
	w := h.put("/identifiers/ident-phone", `{"sign_in":true}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT /identifiers/ident-phone = %d %s, want 204", w.Code, w.Body)
	}
	if len(h.admin.flags) != 1 || h.admin.flags[0].flags != (authn.IdentifierFlags{SignIn: true}) {
		t.Errorf("the flags written are %+v, want sign_in true with the contact left false", h.admin.flags)
	}
	if len(h.sessions.rotated) != 1 || h.sessions.rotated[0] != session {
		t.Errorf("Rotate was called with %v, want the current session", h.sessions.rotated)
	}
	if len(h.sessions.revoked) != 1 || h.sessions.revoked[0] != (revokeCall{account, session, authn.ReasonSelf}) {
		t.Errorf("RevokeAccount was called with %+v, want the account's other sessions", h.sessions.revoked)
	}
	if v, ok := cookieValue(w, "sid"); !ok || v != "rotated-token" {
		t.Errorf("cookie = %q (present %v), want the rotated token", v, ok)
	}
}

// TestChoosingTheDefaultContactDoesNotRotate the session holds that the default
// contact is not a rotation point: it asks for fresh authentication, writes the
// choice, and leaves the session as it is.
func TestChoosingTheDefaultContactDoesNotRotateTheSession(t *testing.T) {
	h := newHarness(t)
	h.axesAccount()
	// A second proved email address to move the choice to.
	h.accounts.identifiers = append(h.accounts.identifiers, authn.Identifier{
		ID: "ident-proved-two", Kind: authn.IdentifierEmail, Value: "second@example.com",
		VerifiedAt: now.Add(-time.Minute), VerifiedVia: authn.VerifiedViaEmailCode,
	})
	w := h.put("/identifiers/ident-proved-two", `{"default_contact":true}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT /identifiers/ident-proved-two = %d %s, want 204", w.Code, w.Body)
	}
	want := []string{
		"mfa.Factors acct-1", "accounts.Identifiers acct-1", "admin.SetDefaultContact acct-1 ident-proved-two",
	}
	if got := h.log.all(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if len(h.sessions.rotated) != 0 || len(h.sessions.revoked) != 0 {
		t.Errorf("a default-contact change rotated or revoked sessions: %v %v", h.sessions.rotated, h.sessions.revoked)
	}
}

// TestUpdatingAnIdentifierRefusesWhatTheAxesDoNotAllow holds the refusals the
// route decides itself, before the account module is asked: an unproved row
// cannot become a sign-in way, only a proved email address can be the default
// contact, a username has no two axes, an identifier the account does not hold
// is answered the same way as one that does not exist, and a body that changes
// nothing is malformed.
func TestUpdatingAnIdentifierRefusesWhatTheAxesDoNotAllow(t *testing.T) {
	for _, c := range []struct {
		name string
		id   string
		body string
	}{
		{"an unproved sign-in", "ident-unproved", `{"sign_in":true}`},
		{"an unproved default contact", "ident-unproved", `{"default_contact":true}`},
		{"a phone as the default contact", "ident-phone", `{"default_contact":true}`},
		{"a username", "ident-1", `{"contact":true}`},
		{"an identifier the account does not hold", "ident-of-another", `{"contact":true}`},
		{"a body that changes nothing", "ident-proved", `{}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.axesAccount()
			w := h.put("/identifiers/"+c.id, c.body)
			if w.Code != http.StatusBadRequest || problemType(t, w) != "bad-request" {
				t.Fatalf("answer = %d %s, want 400 bad-request", w.Code, w.Body)
			}
			if len(h.admin.flags) != 0 || len(h.admin.defaults) != 0 {
				t.Errorf("a refused change reached the account module: %+v %+v", h.admin.flags, h.admin.defaults)
			}
			if len(h.sessions.rotated) != 0 || len(h.sessions.revoked) != 0 {
				t.Error("a refused change settled the sessions")
			}
		})
	}
}

// TestASignInChangeAsksForFreshAuthentication holds that the freshness a change
// of what proves the account asks for is decided from the body: the same stale
// identity that may write a contact is refused the step-up when it asks to change
// sign-in or the default contact.
func TestASignInChangeAsksForFreshAuthentication(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
	}{
		{"sign-in", `{"sign_in":true}`},
		{"the default contact", `{"default_contact":true}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.axesAccount()
			h.as(authntest.WithAuthTime(now.Add(-30 * time.Minute)))
			w := h.put("/identifiers/ident-proved", c.body)
			if w.Code != http.StatusUnauthorized || problemType(t, w) != "insufficient_user_authentication" {
				t.Fatalf("answer = %d %s, want 401 insufficient_user_authentication", w.Code, w.Body)
			}
			if got := h.log.names(); !slices.Equal(got, factorsOnly) {
				t.Errorf("calls after a step-up refusal = %v, want only %v", got, factorsOnly)
			}
		})
	}
}

// TestUpdatingAnIdentifierMapsTheAccountModulesAnswers holds each answer the
// write can give: the two lock-out refusals are their own 409 codes, an argument
// the account module refuses is a 400, and a dependency that cannot answer is a
// 503.
func TestUpdatingAnIdentifierMapsTheAccountModulesAnswers(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
		kind string
	}{
		{authn.ErrIdentifierRequired, http.StatusConflict, "login-identifier-required"},
		{authn.ErrLastLoginMethod, http.StatusConflict, "last-login-method"},
		{authn.ErrInvalidArgument, http.StatusBadRequest, "bad-request"},
		{authn.ErrUnavailable, http.StatusServiceUnavailable, "unavailable"},
		{errors.New("driver: down"), http.StatusServiceUnavailable, "unavailable"},
	} {
		h := newHarness(t)
		h.axesAccount()
		h.admin.flagsErr = c.err
		w := h.put("/identifiers/ident-proved", `{"sign_in":true}`)
		if w.Code != c.code || problemType(t, w) != c.kind {
			t.Errorf("with %v the answer = %d %s, want %d %q", c.err, w.Code, w.Body, c.code, c.kind)
		}
		if len(h.sessions.rotated) != 0 {
			t.Error("a refused write rotated the session")
		}
	}
}

// TestTheIdentifiersAnUpdateCannotReadAreA503 holds that an unreadable list is
// not an empty one, and that nothing is written from it.
func TestTheIdentifiersAnUpdateCannotReadAreA503(t *testing.T) {
	h := newHarness(t)
	h.accounts.err = authn.ErrUnavailable
	w := h.put("/identifiers/ident-1", `{"contact":true}`)
	if w.Code != http.StatusServiceUnavailable || problemType(t, w) != "unavailable" {
		t.Fatalf("answer = %d %s, want 503 unavailable", w.Code, w.Body)
	}
	if len(h.admin.flags) != 0 {
		t.Error("a write was asked for with the list unread")
	}
}
