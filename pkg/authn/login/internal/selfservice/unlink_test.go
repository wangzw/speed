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

// TestRemovingAnIdentifierRemovesTheCallersOwn holds the success path: the
// identifier is checked against the account's own, removed for the account, and
// the answer is a 204.
func TestRemovingAnIdentifierRemovesTheCallersOwn(t *testing.T) {
	h := newHarness(t)
	h.richAccount()
	w := h.delete("/identifiers/ident-2")
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE /identifiers/ident-2 = %d %s, want 204", w.Code, w.Body)
	}
	want := []string{"mfa.Factors acct-1", "accounts.Identifiers acct-1", "admin.RemoveIdentifier acct-1 ident-2"}
	if got := h.log.all(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

// TestAnIdentifierThatIsNotTheCallersIsNotRemoved holds that an identifier the
// account does not hold, whether it does not exist or belongs to another
// account, is refused the same way before the write is asked for, so that the
// route cannot be used to remove or to probe another account's identifiers.
func TestAnIdentifierThatIsNotTheCallersIsNotRemoved(t *testing.T) {
	h := newHarness(t)
	h.richAccount()
	for _, id := range []string{"ident-of-another-account", "ident-1%20", "IDENT-1", "link-1"} {
		w := h.delete("/identifiers/" + id)
		if w.Code != http.StatusBadRequest || problemType(t, w) != "bad-request" {
			t.Errorf("DELETE /identifiers/%s = %d %s, want 400 bad-request", id, w.Code, w.Body)
		}
	}
	if h.log.count("admin.RemoveIdentifier") != 0 {
		t.Error("an identifier the account does not hold was passed to the write")
	}
}

// TestRemovingAnIdentifierMapsTheAccountModulesAnswers holds each answer the
// removal can give. The refusal to leave a password account with no login name
// is a 409 login-identifier-required, and the refusal to leave it with no way
// to sign in at all is its own 409 last-login-method. A non-last-way conflict
// keeps the generic conflict code.
func TestRemovingAnIdentifierMapsTheAccountModulesAnswers(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
		kind string
	}{
		{authn.ErrIdentifierRequired, http.StatusConflict, "login-identifier-required"},
		{authn.ErrLastLoginMethod, http.StatusConflict, "last-login-method"},
		{authn.ErrLastFactor, http.StatusConflict, "conflict"},
		{authn.ErrBindingConflict, http.StatusConflict, "conflict"},
		{authn.ErrUnavailable, http.StatusServiceUnavailable, "unavailable"},
		{errors.New("driver: down"), http.StatusServiceUnavailable, "unavailable"},
	} {
		h := newHarness(t)
		h.richAccount()
		h.admin.removeErr = c.err
		w := h.delete("/identifiers/ident-1")
		if w.Code != c.code || problemType(t, w) != c.kind {
			t.Errorf("with %v the answer = %d %s, want %d %q", c.err, w.Code, w.Body, c.code, c.kind)
		}
	}
}

// TestTheIdentifiersThatCannotBeListedAreA503 holds that an unreadable list is
// not an empty one.
func TestTheIdentifiersThatCannotBeListedAreA503(t *testing.T) {
	h := newHarness(t)
	h.accounts.err = authn.ErrUnavailable
	if w := h.delete("/identifiers/ident-1"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("answer = %d, want 503", w.Code)
	}
	if h.log.count("admin.RemoveIdentifier") != 0 {
		t.Error("a removal was asked for with the list unread")
	}
}

// TestUnlinkingRotatesRevokesTheOthersAndPublishes holds the success path of
// DELETE /links/:id, with the order the design gives: the unbinding, then the
// settling of the sessions, and the event with the provider's class and no
// subject, and the audit row.
func TestUnlinkingRotatesRevokesTheOthersAndPublishes(t *testing.T) {
	h := newHarness(t)
	h.richAccount()
	w := h.delete("/links/link-1")
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE /links/link-1 = %d %s, want 204", w.Code, w.Body)
	}
	want := []string{"mfa.Factors acct-1", "accounts.Externals acct-1", "admin.UnlinkExternal acct-1 link-1", "sessions.Rotate sess-1", "sessions.RevokeAccount self", "sessions.SetCookie"}
	if got := h.log.all(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if got := h.sessions.revoked; len(got) != 1 || got[0] != (revokeCall{account, session, authn.ReasonSelf}) {
		t.Errorf("RevokeAccount was called with %+v", got)
	}
	events := h.bus.published()
	if len(events) != 1 || events[0].Type != authn.EventBindingChanged {
		t.Fatalf("events = %+v, want authn.binding.changed", events)
	}
	if p, ok := events[0].Payload.(authn.BindingChanged); !ok || p != (authn.BindingChanged{AccountID: string(account), ProviderClass: "oidc", Change: "unlinked", Source: "explicit"}) {
		t.Errorf("payload = %+v", events[0].Payload)
	}
	rows := h.audit.entries()
	if len(rows) != 1 || rows[0].Action != "authn.binding.unlink" || rows[0].Outcome != "success" || rows[0].Object != string(account) || rows[0].Detail["provider"] != "oidc" {
		t.Errorf("audit rows = %+v", rows)
	}
	if v, ok := cookieValue(w, "sid"); !ok || v != "rotated-token" {
		t.Errorf("cookie = %q (present %v)", v, ok)
	}
}

// TestTheLastWayToLogInIsNotUnlinked holds the refusal of the account module as
// a 409 last-login-method, and that a refused unbinding revokes nothing,
// rotates nothing, publishes nothing and leaves no success row.
func TestTheLastWayToLogInIsNotUnlinked(t *testing.T) {
	h := newHarness(t)
	h.richAccount()
	h.admin.unlinkErr = authn.ErrLastLoginMethod
	w := h.delete("/links/link-1")
	if w.Code != http.StatusConflict || problemType(t, w) != "last-login-method" {
		t.Fatalf("answer = %d %s, want 409 last-login-method", w.Code, w.Body)
	}
	for _, name := range []string{"sessions.Rotate", "sessions.RevokeAccount", "sessions.SetCookie"} {
		if h.log.count(name) != 0 {
			t.Errorf("%s was called after a refused unbinding", name)
		}
	}
	if len(h.bus.published()) != 0 || len(h.audit.entries()) != 0 || w.Header().Get("Set-Cookie") != "" {
		t.Error("a refused unbinding published an event, wrote a row or wrote a cookie")
	}
}

// TestAnIdPsMultiFactorDoesNotLetAnAccountWithFactorsUnlink holds the weaker of
// the two proofs out: an account that has a factor, held to a local multi-factor
// authentication, is refused for an identity whose second factor an IdP only
// declared, and the unbinding is not attempted. The same identity may unlink on
// an account with no factor, where a fresh first factor is what is asked.
func TestAnIdPsMultiFactorDoesNotLetAnAccountWithFactorsUnlink(t *testing.T) {
	h := newHarness(t)
	h.richAccount()
	h.activeFactor()
	h.as(idpMulti(0)...)
	w := h.delete("/links/link-1")
	if w.Code != http.StatusUnauthorized || problemType(t, w) != "insufficient_user_authentication" {
		t.Fatalf("answer = %d %s, want the step-up 401", w.Code, w.Body)
	}
	if h.log.count("admin.UnlinkExternal") != 0 {
		t.Error("the unbinding was attempted")
	}
	rows := h.audit.entries()
	if len(rows) != 1 || rows[0].Action != "authn.binding.unlink" || rows[0].Outcome != "denied" {
		t.Errorf("audit rows = %+v, want one denied row", rows)
	}

	h.mfa.factors = nil
	if w := h.delete("/links/link-1"); w.Code != http.StatusNoContent {
		t.Errorf("with no factor the same identity got %d, want 204", w.Code)
	}
}

// TestABindingThatIsNotTheCallersIsNotUnlinked holds the same ownership check as
// for an identifier.
func TestABindingThatIsNotTheCallersIsNotUnlinked(t *testing.T) {
	h := newHarness(t)
	h.richAccount()
	for _, id := range []string{"link-of-another-account", "ident-1"} {
		if w := h.delete("/links/" + id); w.Code != http.StatusBadRequest {
			t.Errorf("DELETE /links/%s = %d, want 400", id, w.Code)
		}
	}
	if h.log.count("admin.UnlinkExternal") != 0 {
		t.Error("a binding the account does not hold was unlinked")
	}
}

// TestUnlinkingMapsTheAccountModulesAnswers holds the other answers.
func TestUnlinkingMapsTheAccountModulesAnswers(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
	}{
		{authn.ErrUnavailable, http.StatusServiceUnavailable},
		{errors.New("driver: down"), http.StatusServiceUnavailable},
		{authn.ErrAccountNotFound, http.StatusUnauthorized},
	} {
		h := newHarness(t)
		h.richAccount()
		h.admin.unlinkErr = c.err
		w := h.delete("/links/link-1")
		if w.Code != c.code {
			t.Errorf("with %v the answer = %d, want %d", c.err, w.Code, c.code)
		}
		if h.log.count("sessions.RevokeAccount") != 0 || len(h.bus.published()) != 0 {
			t.Errorf("with %v sessions were revoked or an event published", c.err)
		}
	}
}

// TestTheProviderClassIsInTheClosedSet holds the class of each provider
// namespace of the account design, and that a provider outside them is unlinked
// without an event, which is logged, rather than published with a value the
// payload does not allow.
func TestTheProviderClassIsInTheClosedSet(t *testing.T) {
	for provider, class := range map[string]string{
		"google": "google", "github": "github", "wechat": "wechat", "wechat-openid:wx123": "wechat",
		"oidc:0198f0aa": "oidc", "saml:0198f0aa": "saml",
	} {
		h := newHarness(t)
		h.accounts.externals = []authn.ExternalBinding{{ID: "link-1", Provider: provider, Subject: "s"}}
		if w := h.delete("/links/link-1"); w.Code != http.StatusNoContent {
			t.Fatalf("%s: answer = %d", provider, w.Code)
		}
		events := h.bus.published()
		if len(events) != 1 {
			t.Fatalf("%s: events = %+v", provider, events)
		}
		if p, _ := events[0].Payload.(authn.BindingChanged); p.ProviderClass != class {
			t.Errorf("%s: class = %q, want %q", provider, p.ProviderClass, class)
		}
	}
	for _, provider := range []string{"facebook", "wechat-extra", "oidc", "saml"} {
		h := newHarness(t)
		h.accounts.externals = []authn.ExternalBinding{{ID: "link-1", Provider: provider, Subject: "s"}}
		if w := h.delete("/links/link-1"); w.Code != http.StatusNoContent {
			t.Fatalf("%s: answer = %d", provider, w.Code)
		}
		if len(h.bus.published()) != 0 {
			t.Errorf("%s: an event was published for a provider outside the classes", provider)
		}
		if h.log.count("sessions.RevokeAccount") != 1 {
			t.Errorf("%s: the sessions were not revoked", provider)
		}
	}
}

// TestAnEventThatCannotBePublishedDoesNotFailTheUnlinking holds that an event is
// a hint: a bus that refuses it, and an audit that cannot be written, are logged
// and the unbinding stands.
func TestAnEventThatCannotBePublishedDoesNotFailTheUnlinking(t *testing.T) {
	h := newHarness(t)
	h.richAccount()
	h.bus.err = errors.New("bus: closed")
	h.audit.err = errors.New("audit: not accepting")
	if w := h.delete("/links/link-1"); w.Code != http.StatusNoContent {
		t.Fatalf("answer = %d, want 204", w.Code)
	}
	if h.log.count("sessions.RevokeAccount") != 1 {
		t.Error("the sessions were not revoked")
	}
}

// TestTheOptionalOutputsAreOptional holds that a service with no bus, no audit and
// no settings does the work.
func TestTheOptionalOutputsAreOptional(t *testing.T) {
	h := newHarness(t, func(c *config) { c.withoutOptins = true })
	h.richAccount()
	if w := h.delete("/links/link-1"); w.Code != http.StatusNoContent {
		t.Fatalf("answer = %d, want 204", w.Code)
	}
	h.as(authntest.WithAuthTime(now.Add(-time.Hour)))
	if w := h.delete("/links/link-2"); w.Code != http.StatusUnauthorized {
		t.Fatalf("a stale proof with no settings = %d, want 401", w.Code)
	}
}
