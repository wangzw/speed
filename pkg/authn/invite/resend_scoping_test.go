package invite

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheResendRouteKeepsTheTenantInTheRowKey checks that another tenant's
// invitation under this tenant's path is a bare 404 and untouched.
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

// TestTheResendRouteDoesNotActWhenTheMandatoryAuditFails checks that a failed
// mandatory audit answers 503 and leaves the row unresent.
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

// TestTheResendRouteRefusedByTheDeciderWritesNothing checks that a decider
// refusal is a 403 and writes nothing.
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

// TestTheResendRouteIsGatedForAnUnverifiedOperator checks that a pending
// registration is refused by the signup gate before the Decider and the audit.
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

// TestTheResendRouteAnswersABare404ForAnUnknownIdentifier checks that an unknown
// identifier is a bare 404.
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

// TestTheResendRouteCarriesNoTokenWhenTheNoticeWasSent checks that a sent notice
// omits the token from the resend answer.
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
