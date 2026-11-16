package invite

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// errInviterUnreadable is the failure a case scripts for the account read that
// resolves an inviter's name after the invitation was written.
var errInviterUnreadable = errors.New("the account store could not answer")

// invitationByEmail finds the invitation a page holds for an address.
func invitationByEmail(t *testing.T, page AuthnInvitePage, email string) AuthnInviteInvitation {
	t.Helper()
	for _, invitation := range page.Invitations {
		if invitation.Email == email {
			return invitation
		}
	}
	t.Fatalf("the page holds no invitation for %s", email)
	return AuthnInviteInvitation{}
}

// TestTheIssueAnswerNamesAReadableInviter is the control the failure cases are
// read against: with the reads answering, both name keys carry the issuer's
// display name and username.
func TestTheIssueAnswerNamesAReadableInviter(t *testing.T) {
	h := newHarness(t)
	h.accounts.named(adminID, "Ada Admin")
	h.accounts.user(adminID, "ada")
	h.asAdmin()

	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var answered issuedAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if got := answered.Invitation.InvitedByName; got != "Ada Admin" {
		t.Errorf("invited_by_name = %q, want the inviter's display name", got)
	}
	if got := answered.Invitation.InvitedByUsername; got != "ada" {
		t.Errorf("invited_by_username = %q, want the inviter's username", got)
	}
}

// TestTheIssueAnswerKeepsTheTokenWhenTheInviterCannotBeRead holds the one-time
// token against a read failure that happens after the invitation is settled:
// failing the answer would discard the only copy of the acceptance link. The
// inviter's names are omitted and the failure is logged. The acting operator
// issues as the impersonated account, so the failed read is the inviter's
// alone and the signup gate's own read still answers.
func TestTheIssueAnswerKeepsTheTokenWhenTheInviterCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.accounts.named(adminID, "Ada Admin")
	h.accounts.user(adminID, "ada")
	h.mounter.As(
		authntest.WithAccount(adminID),
		authntest.WithActor("acct-operator"),
		authntest.WithSession(authn.SessionID("session-1")),
		authntest.WithScope(tenant),
	)
	h.accounts.failsFor(adminID, errInviterUnreadable)

	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 after the invitation was written: %s", w.Code, w.Body.String())
	}
	var answered issuedAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if answered.Token == "" {
		t.Error("the answer carries no token although the notice was not sent")
	}
	assertNoInviterNames(t, w.Body.String())
	if lines := h.logs.lines("level=ERROR", "the view names no inviter"); len(lines) != 1 {
		t.Errorf("the log holds %d Error lines for the unreadable inviter, want one:\n%s", len(lines), h.logs.String())
	}
}

// TestTheResendAnswerKeepsTheTokenWhenTheInviterCannotBeRead holds the same rule
// on a resend, where the cost of failing the answer is higher: the resend has
// already replaced the token, so the caller would hold neither the old link nor
// the new one.
func TestTheResendAnswerKeepsTheTokenWhenTheInviterCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.accounts.named("acct-inviter", "Ivy Inviter")
	h.accounts.user("acct-inviter", "ivy")
	issued := h.issueAs("acct-inviter", tenant, "alice@example.com", "")
	h.asAdmin()
	h.accounts.failsFor("acct-inviter", errInviterUnreadable)

	w := h.request(http.MethodPost, pathAdmin+"/"+issued.Invitation.ID+"/resend", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after the resend was written: %s", w.Code, w.Body.String())
	}
	var answered AuthnInviteIssued
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if answered.Token == "" {
		t.Error("the answer carries no token although the notice was not sent")
	}
	assertNoInviterNames(t, w.Body.String())
	if lines := h.logs.lines("level=ERROR", "the view names no inviter"); len(lines) != 1 {
		t.Errorf("the log holds %d Error lines for the unreadable inviter, want one:\n%s", len(lines), h.logs.String())
	}
}

// TestTheListOmitsTheInviterWhenTheAccountCannotBeRead holds the same rule on
// the management list, and its counterpart: one inviter whose read fails loses
// both names, while another invitation on the same page still carries them.
func TestTheListOmitsTheInviterWhenTheAccountCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.accounts.named("acct-a", "Alex Able")
	h.accounts.user("acct-a", "alexa")
	h.accounts.named("acct-b", "Blair Bright")
	h.accounts.user("acct-b", "blairb")
	h.issueAs("acct-a", tenant, "alice@example.com", "")
	h.clock.advance(time.Minute)
	h.issueAs("acct-b", tenant, "bob@example.com", "")
	h.asAdmin()

	// The control: with every read answering, both invitations are named.
	w := h.request(http.MethodGet, pathAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var named AuthnInvitePage
	if err := json.Unmarshal(w.Body.Bytes(), &named); err != nil {
		t.Fatalf("decoding the page: %v", err)
	}
	if got := invitationByEmail(t, named, "alice@example.com").InvitedByName; got != "Alex Able" {
		t.Errorf("alice's inviter = %q, want the readable display name", got)
	}
	if got := invitationByEmail(t, named, "bob@example.com").InvitedByUsername; got != "blairb" {
		t.Errorf("bob's inviter username = %q, want the readable username", got)
	}

	// One inviter's read fails: the page still answers, that invitation loses
	// both names, and the other keeps its own.
	h.accounts.failsFor("acct-b", errInviterUnreadable)
	w = h.request(http.MethodGet, pathAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a page whose inviter cannot be named: %s", w.Code, w.Body.String())
	}
	var page AuthnInvitePage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decoding the page: %v", err)
	}
	if len(page.Invitations) != 2 {
		t.Fatalf("invitations = %d, want the two written", len(page.Invitations))
	}
	readable := invitationByEmail(t, page, "alice@example.com")
	if readable.InvitedByName != "Alex Able" || readable.InvitedByUsername != "alexa" {
		t.Errorf("the readable inviter = (%q, %q), want both names kept while the other read fails",
			readable.InvitedByName, readable.InvitedByUsername)
	}
	unreadable := invitationByEmail(t, page, "bob@example.com")
	if unreadable.InvitedByName != "" || unreadable.InvitedByUsername != "" {
		t.Errorf("the unreadable inviter = (%q, %q), want both names omitted",
			unreadable.InvitedByName, unreadable.InvitedByUsername)
	}
	if lines := h.logs.lines("level=ERROR", unreadable.ID); len(lines) != 1 {
		t.Errorf("the log holds %d Error lines naming %s, want one:\n%s", len(lines), unreadable.ID, h.logs.String())
	}
}

// TestTheListKeepsDisplayNamesWhenTheUsernameReadFails holds the bulk read's
// failure: a page that already resolved each inviter's display name keeps them
// and omits only the usernames.
func TestTheListKeepsDisplayNamesWhenTheUsernameReadFails(t *testing.T) {
	h := newHarness(t)
	h.accounts.named(adminID, "Ada Admin")
	h.accounts.user(adminID, "ada")
	h.issue(tenant, "alice@example.com")
	h.asAdmin()
	h.accounts.failsMany(errInviterUnreadable)

	w := h.request(http.MethodGet, pathAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when only the usernames cannot be read: %s", w.Code, w.Body.String())
	}
	var page AuthnInvitePage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decoding the page: %v", err)
	}
	if len(page.Invitations) != 1 {
		t.Fatalf("invitations = %d, want the one written", len(page.Invitations))
	}
	invitation := page.Invitations[0]
	if invitation.InvitedByName != "Ada Admin" {
		t.Errorf("invited_by_name = %q, want the display name kept", invitation.InvitedByName)
	}
	if invitation.InvitedByUsername != "" {
		t.Errorf("invited_by_username = %q, want it omitted", invitation.InvitedByUsername)
	}
	if strings.Contains(w.Body.String(), `"invited_by_username"`) {
		t.Errorf("the answer carries invited_by_username although the bulk read failed: %s", w.Body.String())
	}
	if lines := h.logs.lines("level=ERROR", "the views name no username"); len(lines) != 1 {
		t.Errorf("the log holds %d Error lines for the failed bulk read, want one:\n%s", len(lines), h.logs.String())
	}
}

// assertNoInviterNames holds that neither name key reaches the answer, so a
// client sees an absent name rather than an empty one.
func assertNoInviterNames(t *testing.T, body string) {
	t.Helper()
	for _, field := range []string{"invited_by_name", "invited_by_username"} {
		if strings.Contains(body, `"`+field+`"`) {
			t.Errorf("the answer carries %s although the account could not be read: %s", field, body)
		}
	}
}
