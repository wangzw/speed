package invite

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheResendRouteKeepsTheTenantInTheRowKey holds that a tenant's route cannot
// act on another tenant's invitation: tenant-b's identifier under tenant-a's
// path is the bare 404, and tenant-b's row keeps its token, its send record and
// its state.
func TestTheResendRouteKeepsTheTenantInTheRowKey(t *testing.T) {
	h := newHarness(t)
	foreign := h.issue("tenant-b", "bob@example.com")
	before := h.row(foreign.Invitation.ID)
	h.asAdmin()

	w := h.request(http.MethodPost, pathAdmin+"/"+foreign.Invitation.ID+"/resend", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("the 404 carries the body %q, want none", w.Body.String())
	}
	after := h.row(foreign.Invitation.ID)
	if after.TokenDigest != before.TokenDigest {
		t.Error("the crossed resend replaced tenant-b's token digest")
	}
	if after.SendCount != before.SendCount {
		t.Errorf("send_count = %d, want the untouched %d", after.SendCount, before.SendCount)
	}
	if !after.LastSentAt.Equal(before.LastSentAt) {
		t.Errorf("the crossed resend moved last_sent_at from %v to %v", before.LastSentAt, after.LastSentAt)
	}
	if after.Status != statusPending {
		t.Errorf("tenant-b's row reads status %d, want pending", after.Status)
	}
}

// TestTheResendRouteDoesNotActWhenTheMandatoryAuditFails holds the audit's place
// in the admission order: the row is written before the action, so a journal that
// refuses it answers 503 and the invitation is not resent.
func TestTheResendRouteDoesNotActWhenTheMandatoryAuditFails(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	before := h.row(issued.Invitation.ID)
	h.audit.fails(errors.New("the journal is down"))
	h.asAdmin()

	w := h.request(http.MethodPost, pathAdmin+"/"+issued.Invitation.ID+"/resend", nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", w.Code, w.Body.String())
	}
	if len(h.audit.entries()) != 0 {
		t.Errorf("a refused audit left %d recorded rows, want none", len(h.audit.entries()))
	}
	after := h.row(issued.Invitation.ID)
	if after.TokenDigest != before.TokenDigest {
		t.Error("the resend replaced the token digest although the mandatory audit failed")
	}
	if after.SendCount != before.SendCount {
		t.Errorf("send_count = %d, want the unchanged %d", after.SendCount, before.SendCount)
	}
}

// TestTheResendRouteRefusedByTheDeciderWritesNothing holds the decision's place:
// a refusal is 403, the Decider was asked for membership.manage on the invitation
// identifier, and neither an audit row nor a token change follows.
func TestTheResendRouteRefusedByTheDeciderWritesNothing(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	before := h.row(issued.Invitation.ID)
	h.decider.allow = false
	h.asAdmin()

	w := h.request(http.MethodPost, pathAdmin+"/"+issued.Invitation.ID+"/resend", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if len(h.audit.entries()) != 0 {
		t.Errorf("a refused resend wrote %d audit rows, want none", len(h.audit.entries()))
	}
	dec, ok := h.decider.last()
	if !ok || dec.Action != authn.ActionMembershipManage || dec.Tenant != tenant ||
		dec.Target != issued.Invitation.ID {
		t.Errorf("decision = %+v, want membership.manage in %q on %q", dec, tenant, issued.Invitation.ID)
	}
	after := h.row(issued.Invitation.ID)
	if after.TokenDigest != before.TokenDigest || after.SendCount != before.SendCount {
		t.Error("the refused resend changed the row")
	}
}

// TestTheResendRouteIsGatedForAnUnverifiedOperator holds the signup gate on the
// resend face: a pending registration answers 403 contact-required before the
// Decider and the audit, and the row is untouched.
func TestTheResendRouteIsGatedForAnUnverifiedOperator(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	before := h.row(issued.Invitation.ID)
	h.verification.state = authn.SignupStatus{State: authn.SignupPending}
	h.asAdmin()

	w := h.request(http.MethodPost, pathAdmin+"/"+issued.Invitation.ID+"/resend", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "urn:speed:authn:contact-required") {
		t.Errorf("body %s does not carry contact-required", w.Body.String())
	}
	if h.decider.count() != 0 {
		t.Errorf("the Decider was asked %d times, want none after the gate", h.decider.count())
	}
	if len(h.audit.entries()) != 0 {
		t.Errorf("a gated resend wrote %d audit rows, want none", len(h.audit.entries()))
	}
	after := h.row(issued.Invitation.ID)
	if after.TokenDigest != before.TokenDigest || after.SendCount != before.SendCount {
		t.Error("the gated resend changed the row")
	}
}

// TestTheResendRouteAnswersABare404ForAnUnknownIdentifier holds the not-found
// answer on this route: no problem document, because an identifier naming no
// invitation of the tenant tells the caller nothing it may rely on.
func TestTheResendRouteAnswersABare404ForAnUnknownIdentifier(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()

	w := h.request(http.MethodPost, pathAdmin+"/no-such-invitation/resend", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("the 404 carries the body %q, want none", w.Body.String())
	}
}

// TestTheResendRouteCarriesNoTokenWhenTheNoticeWasSent holds the token hand-over
// rule on the resend answer: a notice that reached the address means the
// recipient holds the link, so the answer reports sent and omits the token field
// altogether. The notice itself carries the new token, whose digest is the row's.
func TestTheResendRouteCarriesNoTokenWhenTheNoticeWasSent(t *testing.T) {
	h := newHarness(t)
	notifier := &fakeNotifier{delivery: authn.InviteSent}
	h.p.notifier = notifier
	issued := h.issue(tenant, "alice@example.com")
	h.asAdmin()

	w := h.request(http.MethodPost, pathAdmin+"/"+issued.Invitation.ID+"/resend", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var answered issuedAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	answered.raw = w.Body.String()
	if answered.EmailDelivery != authn.InviteSent.String() {
		t.Errorf("email_delivery = %q, want %q", answered.EmailDelivery, authn.InviteSent.String())
	}
	if answered.Token != "" {
		t.Errorf("the answer carries the token %q although the notice was sent", answered.Token)
	}
	if strings.Contains(answered.raw, `"token"`) {
		t.Errorf("the sent answer declares the token field at all: %s", answered.raw)
	}
	notice, ok := notifier.last()
	if !ok {
		t.Fatal("the resend called no notifier")
	}
	if notice.Token == "" {
		t.Fatal("the notice carries no token")
	}
	if got, want := tokenDigest(notice.Token), h.row(issued.Invitation.ID).TokenDigest; got != want {
		t.Errorf("the notice's token digest %q is not the row's %q", got, want)
	}
	if got := h.row(issued.Invitation.ID).SendCount; got != 2 {
		t.Errorf("send_count = %d, want 2", got)
	}
}
