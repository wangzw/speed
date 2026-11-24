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

var errInviterUnreadable = errors.New("the account store could not answer")

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

// TestTheIssueAnswerNamesAReadableInviter checks that the issue answer carries
// the inviter's display name and username.
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

// TestTheIssueAnswerKeepsTheTokenWhenTheInviterCannotBeRead checks that an
// unreadable inviter still leaves the one-time token in the answer.
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

// TestTheResendAnswerKeepsTheTokenWhenTheInviterCannotBeRead checks that a resend
// whose inviter cannot be read still answers its new token.
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

// TestTheListOmitsTheInviterWhenTheAccountCannotBeRead checks that one unreadable
// inviter loses both names while the rest of the page keeps theirs.
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

// TestTheListKeepsDisplayNamesWhenTheUsernameReadFails checks that a failed bulk
// read keeps the display names and omits the usernames.
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

func assertNoInviterNames(t *testing.T, body string) {
	t.Helper()
	for _, field := range []string{"invited_by_name", "invited_by_username"} {
		if strings.Contains(body, `"`+field+`"`) {
			t.Errorf("the answer carries %s although the account could not be read: %s", field, body)
		}
	}
}
